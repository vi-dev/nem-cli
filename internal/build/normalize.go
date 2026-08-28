package build

import (
	"bytes"
	"debug/macho"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// normalizeOutput applies the standard post-build cleanups to an output
// tree so its contents are relocatable: libtool archives dropped, .pc
// files rewritten relative to their own location, and (darwin) dylib ids
// and in-tree references moved to @rpath. Every transform is a no-op on
// already-normalized trees, so recipes doing the same work by hand stay
// harmless.
func normalizeOutput(outDir string) error {
	if err := dropLibtoolArchives(outDir); err != nil {
		return err
	}
	if err := relocatePkgconfig(outDir); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return nil
	}
	fixes, err := planMachoFixes(outDir)
	if err != nil {
		return err
	}
	return applyMachoFixes(fixes)
}

// dropLibtoolArchives removes every *.la file: its baked libdir misleads a
// dependent's libtool once the package is installed elsewhere, and nothing
// consumes it at runtime.
func dropLibtoolArchives(outDir string) error {
	return filepath.WalkDir(outDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".la") {
			return os.Remove(path)
		}
		return nil
	})
}

// relocatePkgconfig rewrites absolute install paths in */pkgconfig/*.pc
// files: a DESTDIR-style build bakes prefix=/ (or the configure prefix),
// which would hand pkg-config consumers paths that don't exist on the
// installed machine. Values that are already relative are left alone.
func relocatePkgconfig(outDir string) error {
	return filepath.WalkDir(outDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), ".pc") ||
			filepath.Base(filepath.Dir(path)) != "pkgconfig" {
			return nil
		}
		libSeg := filepath.Base(filepath.Dir(filepath.Dir(path)))
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			lines[i] = relocatePCLine(line, libSeg)
		}
		out := strings.Join(lines, "\n")
		if out == string(data) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(path, []byte(out), info.Mode().Perm())
	})
}

// relocatePCLine rewrites one .pc variable assignment when its value is an
// absolute path. libSeg is the directory holding pkgconfig/ ("lib",
// "lib64", "share"), so libdir keeps the layout the package installed.
func relocatePCLine(line, libSeg string) string {
	key, val, ok := strings.Cut(line, "=")
	if !ok || !strings.HasPrefix(val, "/") {
		return line
	}
	switch key {
	case "prefix":
		return "prefix=${pcfiledir}/../.."
	case "exec_prefix":
		return "exec_prefix=${prefix}"
	case "libdir":
		return "libdir=${prefix}/" + libSeg
	case "includedir":
		return "includedir=${prefix}/include"
	}
	return line
}

// machoFix is the planned rewrite for one Mach-O file: a new dylib id,
// reference changes and/or added rpath entries, applied with
// install_name_tool and re-signed.
type machoFix struct {
	path    string
	id      string      // new LC_ID_DYLIB; "" keeps the current id
	changes [][2]string // old reference -> new reference
	rpaths  []string    // LC_RPATH entries to add
}

// planMachoFixes walks outDir and plans the @rpath rewrites: a dylib whose
// id is an absolute non-system path gets id @rpath/<base>, and any Mach-O
// referencing an absolute non-system path whose basename is shipped in
// this same tree gets that reference rewritten to @rpath/<base>. System
// libraries and references to other packages are never touched. Every
// @rpath reference to a shipped lib — rewritten here or linked that way by
// the build — also gets an @loader_path rpath entry back to the lib's dir,
// unless one is already present.
func planMachoFixes(outDir string) ([]machoFix, error) {
	type machoInfo struct {
		path   string
		dylib  bool
		id     string
		refs   []string
		rpaths []string
	}
	var files []machoInfo
	shipped := map[string]string{} // dylib basename -> dir holding it
	err := filepath.WalkDir(outDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 && strings.Contains(d.Name(), ".dylib") {
			shipped[d.Name()] = filepath.Dir(path)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		f, err := macho.Open(path)
		if err != nil {
			return nil // not a Mach-O file
		}
		defer func() { _ = f.Close() }()
		info := machoInfo{path: path, dylib: f.Type == macho.TypeDylib}
		if info.dylib {
			if info.id, err = dylibID(f); err != nil {
				return fmt.Errorf("read dylib id of %s: %w", path, err)
			}
			shipped[filepath.Base(path)] = filepath.Dir(path)
		}
		if info.refs, err = f.ImportedLibraries(); err != nil {
			return fmt.Errorf("read imports of %s: %w", path, err)
		}
		for _, l := range f.Loads {
			if rp, ok := l.(*macho.Rpath); ok {
				info.rpaths = append(info.rpaths, rp.Path)
			}
		}
		files = append(files, info)
		return nil
	})
	if err != nil {
		return nil, err
	}

	var fixes []machoFix
	for _, mf := range files {
		fix := machoFix{path: mf.path}
		if mf.dylib && strings.HasPrefix(mf.id, "/") && !systemLib(mf.id) {
			fix.id = "@rpath/" + filepath.Base(mf.id)
		}
		var inTree []string // basenames this file will reference via @rpath
		for _, ref := range mf.refs {
			base := filepath.Base(ref)
			switch {
			case strings.HasPrefix(ref, "@rpath/"):
				inTree = append(inTree, base)
			case strings.HasPrefix(ref, "/") && !systemLib(ref) && shipped[base] != "":
				fix.changes = append(fix.changes, [2]string{ref, "@rpath/" + base})
				inTree = append(inTree, base)
			}
		}
		have := map[string]bool{}
		for _, rp := range mf.rpaths {
			have[rp] = true
		}
		for _, base := range inTree {
			dir, ok := shipped[base]
			if !ok {
				continue
			}
			rel, err := filepath.Rel(filepath.Dir(mf.path), dir)
			if err != nil {
				return nil, fmt.Errorf("relate %s to %s: %w", mf.path, dir, err)
			}
			entry := "@loader_path"
			if rel != "." {
				entry += "/" + rel
			}
			if !have[entry] {
				have[entry] = true
				fix.rpaths = append(fix.rpaths, entry)
			}
		}
		if fix.id != "" || len(fix.changes) > 0 || len(fix.rpaths) > 0 {
			fixes = append(fixes, fix)
		}
	}
	sort.Slice(fixes, func(i, j int) bool { return fixes[i].path < fixes[j].path })
	return fixes, nil
}

func systemLib(path string) bool {
	return strings.HasPrefix(path, "/usr/lib/") || strings.HasPrefix(path, "/System/")
}

// dylibID reads LC_ID_DYLIB, which debug/macho leaves as raw load-command
// bytes: cmd, cmdsize, then a dylib struct whose first field is the
// command-relative offset of the NUL-terminated name.
func dylibID(f *macho.File) (string, error) {
	const lcIDDylib = 0xd
	for _, l := range f.Loads {
		raw := l.Raw()
		if len(raw) < 12 || f.ByteOrder.Uint32(raw[0:4]) != lcIDDylib {
			continue
		}
		off := f.ByteOrder.Uint32(raw[8:12])
		if int(off) >= len(raw) {
			return "", fmt.Errorf("name offset %d out of range", off)
		}
		name := raw[off:]
		if i := bytes.IndexByte(name, 0); i >= 0 {
			name = name[:i]
		}
		return string(name), nil
	}
	return "", nil
}

// applyMachoFixes rewrites each planned file with install_name_tool and
// ad-hoc re-signs it — the edit invalidates the existing signature, and
// unsigned binaries don't run on arm64 darwin.
func applyMachoFixes(fixes []machoFix) error {
	for _, fix := range fixes {
		args := []string{}
		if fix.id != "" {
			args = append(args, "-id", fix.id)
		}
		for _, c := range fix.changes {
			args = append(args, "-change", c[0], c[1])
		}
		for _, rp := range fix.rpaths {
			args = append(args, "-add_rpath", rp)
		}
		args = append(args, fix.path)
		if out, err := exec.Command("install_name_tool", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("install_name_tool %s: %v\n%s", fix.path, err, out)
		}
		if out, err := exec.Command("codesign", "-f", "-s", "-", fix.path).CombinedOutput(); err != nil {
			return fmt.Errorf("codesign %s: %v\n%s", fix.path, err, out)
		}
	}
	return nil
}
