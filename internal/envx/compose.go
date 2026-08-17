package envx

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// Var is one composed environment variable. Source names where the final
// value came from: the exporting package's name, or "nem.toml" for a
// manifest [env] entry.
type Var struct{ Name, Value, Source string }

// Result is the outcome of composing a directory's environment.
type Result struct {
	Vars       []Var    // final vars, one per name, sorted by name
	Path       []string // absolute bin dirs to prepend, ordered, deduped
	LoaderVar  string   // platform loader var name (DYLD_/LD_LIBRARY_PATH)
	LoaderPath []string // absolute lib dirs to prepend, ordered, deduped
	Warnings   []string // reserved names skipped, broken templates, etc.
}

// Compose builds the environment for a directory from project+global
// manifests, their lock closures, and installed-package metadata. h
// resolves each lock entry's absolute install directory. metaLookup
// returns the install Meta for (name,version) or (nil,false). getenv reads
// the invoking process env, both for [env] expansion and for resolving
// nem-managed keys to their saved pre-nem originals.
func Compose(project, global *project.Manifest, projectLock, globalLock *project.Lockfile, h home.Home,
	metaLookup func(name, version string) (*install.Meta, bool),
	getenv func(string) (string, bool)) Result {

	var warnings []string

	resolvedProject, w := resolveEntries(orderedLockEntries(projectLock), metaLookup, h)
	warnings = append(warnings, w...)
	resolvedGlobal, w := resolveEntries(orderedLockEntries(globalLock), metaLookup, h)
	warnings = append(warnings, w...)

	path := buildPath(resolvedProject, resolvedGlobal)
	loaderPath := buildLoaderPath(resolvedProject, resolvedGlobal)

	vars := map[string]string{}
	sources := map[string]string{}
	applyPackageExports(resolvedGlobal, vars, sources, &warnings)
	applyPackageExports(resolvedProject, vars, sources, &warnings)

	lookup := savedOriginalLookup(managedKeys(vars, global.Env, project.Env), getenv)
	applyEnvSection(global.Env, vars, sources, &warnings, lookup)
	applyEnvSection(project.Env, vars, sources, &warnings, lookup)

	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)

	result := Result{Path: path, LoaderVar: loaderPathVar(), LoaderPath: loaderPath, Warnings: warnings}
	for _, name := range names {
		result.Vars = append(result.Vars, Var{Name: name, Value: vars[name], Source: sources[name]})
	}
	return result
}

// ComposeScope is Compose restricted to one layer: only scope's [env]
// entries and scope's locked packages' exports appear in the result, but
// [env] references still resolve as in the full composition — a variable
// managed by either layer expands to its saved pre-nem original, not
// nem's own live value. Only scope-layer warnings are reported.
func ComposeScope(scope, other *project.Manifest, scopeLock, otherLock *project.Lockfile, h home.Home,
	metaLookup func(name, version string) (*install.Meta, bool),
	getenv func(string) (string, bool)) Result {

	var warnings []string

	resolvedScope, w := resolveEntries(orderedLockEntries(scopeLock), metaLookup, h)
	warnings = append(warnings, w...)
	resolvedOther, _ := resolveEntries(orderedLockEntries(otherLock), metaLookup, h)

	vars := map[string]string{}
	sources := map[string]string{}
	applyPackageExports(resolvedScope, vars, sources, &warnings)

	otherVars := map[string]string{}
	var otherWarnings []string
	applyPackageExports(resolvedOther, otherVars, map[string]string{}, &otherWarnings)

	managed := managedKeys(vars, scope.Env, other.Env)
	for name := range otherVars {
		managed[name] = true
	}
	lookup := savedOriginalLookup(managed, getenv)
	applyEnvSection(scope.Env, vars, sources, &warnings, lookup)

	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)

	result := Result{
		Path:       buildPath(resolvedScope, nil),
		LoaderVar:  loaderPathVar(),
		LoaderPath: buildLoaderPath(resolvedScope, nil),
		Warnings:   warnings,
	}
	for _, name := range names {
		result.Vars = append(result.Vars, Var{Name: name, Value: vars[name], Source: sources[name]})
	}
	return result
}

// resolvedEntry pairs a lock entry with its install metadata and absolute
// install directory, both resolved once and reused for PATH assembly and
// package env exports.
type resolvedEntry struct {
	name, version string
	installDir    string
	meta          *install.Meta
	onPath        bool
	onLoaderPath  bool
}

// orderedLockEntries returns lock's entries valid on the current platform:
// direct entries alphabetically by name, then indirect entries
// alphabetically by name.
func orderedLockEntries(lock *project.Lockfile) []project.LockEntry {
	current := spec.Current().String()
	var direct, indirect []project.LockEntry
	for _, e := range lock.Packages {
		if !containsString(e.Platforms, current) {
			continue
		}
		if e.Direct {
			direct = append(direct, e)
		} else {
			indirect = append(indirect, e)
		}
	}
	sort.Slice(direct, func(i, j int) bool { return direct[i].Name < direct[j].Name })
	sort.Slice(indirect, func(i, j int) bool { return indirect[i].Name < indirect[j].Name })
	return append(direct, indirect...)
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// resolveEntries looks up each entry's install metadata and install dir,
// warning and skipping an entry that fails either.
func resolveEntries(entries []project.LockEntry, metaLookup func(name, version string) (*install.Meta, bool), h home.Home) ([]resolvedEntry, []string) {
	var out []resolvedEntry
	var warnings []string
	for _, e := range entries {
		meta, ok := metaLookup(e.Name, e.Version)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("no install metadata for %s@%s", e.Name, e.Version))
			continue
		}
		installDir, err := h.PackageDir(e.Name, e.Version)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("install dir for %s@%s: %v", e.Name, e.Version, err))
			continue
		}
		out = append(out, resolvedEntry{
			name: e.Name, version: e.Version, installDir: installDir, meta: meta,
			onPath: e.OnPath, onLoaderPath: e.OnLoaderPath,
		})
	}
	return out, warnings
}

// buildPath assembles PATH dirs from project entries then global entries,
// each contributing its Bins joined onto its install dir, deduplicated
// preserving first occurrence — so a project entry always shadows a
// same-named global one. Entries not marked onPath (link-only deps, whose
// libraries load but whose binaries never join PATH) contribute nothing.
func buildPath(projectEntries, globalEntries []resolvedEntry) []string {
	seen := map[string]bool{}
	var out []string
	for _, entries := range [][]resolvedEntry{projectEntries, globalEntries} {
		for _, r := range entries {
			if !r.onPath {
				continue
			}
			for _, bin := range r.meta.Bins {
				dir := filepath.Join(r.installDir, bin)
				if seen[dir] {
					continue
				}
				seen[dir] = true
				out = append(out, dir)
			}
		}
	}
	return out
}

// buildLoaderPath assembles loader-search dirs from on-loader-path entries,
// each contributing its Libs joined onto its install dir, project before
// global, deduplicated preserving first occurrence.
func buildLoaderPath(projectEntries, globalEntries []resolvedEntry) []string {
	seen := map[string]bool{}
	var out []string
	for _, entries := range [][]resolvedEntry{projectEntries, globalEntries} {
		for _, r := range entries {
			if !r.onLoaderPath {
				continue
			}
			for _, lib := range r.meta.Libs {
				dir := filepath.Join(r.installDir, lib)
				if seen[dir] {
					continue
				}
				seen[dir] = true
				out = append(out, dir)
			}
		}
	}
	return out
}

// loaderPathVar is the platform's dynamic-linker search variable: nem sets
// this one directly from buildLoaderPath, never via a package export.
func loaderPathVar() string {
	if spec.Current().OS == "darwin" {
		return "DYLD_LIBRARY_PATH"
	}
	return "LD_LIBRARY_PATH"
}

// applyPackageExports renders each entry's raw env templates against its
// install dir and version, writing results into vars (last writer wins)
// and recording the exporting package's name in sources.
func applyPackageExports(entries []resolvedEntry, vars, sources map[string]string, warnings *[]string) {
	current := spec.Current()
	for _, r := range entries {
		for _, export := range r.meta.Env {
			if !platformIncludes(export.Platforms, current) {
				continue
			}
			if IsReserved(export.Name) {
				*warnings = append(*warnings, fmt.Sprintf("reserved env var %q from %s skipped", export.Name, r.name))
				continue
			}
			value, err := renderEnvTemplate(export.Value, r.installDir, r.version)
			if err != nil {
				*warnings = append(*warnings, fmt.Sprintf("reinstall %s: %v", r.name, err))
				continue
			}
			vars[export.Name] = value
			sources[export.Name] = r.name
		}
	}
}

// platformIncludes reports whether a package export's platform constraint
// (empty = unconstrained) covers current.
func platformIncludes(constraints []spec.Platform, current spec.Platform) bool {
	if len(constraints) == 0 {
		return true
	}
	for _, c := range constraints {
		if c.Matches(current) {
			return true
		}
	}
	return false
}

// envTemplateCtx is the template context for a package env export's raw
// value: InstallDir and Version only.
type envTemplateCtx struct {
	InstallDir string
	Version    string
}

var envTemplateFuncs = template.FuncMap{
	"trimPrefix": func(s, prefix string) string { return strings.TrimPrefix(s, prefix) },
	"trimSuffix": func(s, suffix string) string { return strings.TrimSuffix(s, suffix) },
	"replace":    func(s, old, new string) string { return strings.ReplaceAll(s, old, new) },
}

// renderEnvTemplate expands a package env export's raw value template
// against InstallDir and Version, erroring on any unresolved field.
func renderEnvTemplate(tmpl, installDir, version string) (string, error) {
	t, err := template.New("").Funcs(envTemplateFuncs).Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", tmpl, err)
	}
	var b strings.Builder
	if err := t.Execute(&b, envTemplateCtx{InstallDir: installDir, Version: version}); err != nil {
		return "", fmt.Errorf("expand template %q: %w", tmpl, err)
	}
	return b.String(), nil
}

// managedKeys is the union of names nem is about to set: already-composed
// package exports (exported) plus names declared across the given [env]
// sections — the keys whose references must resolve to their saved
// pre-nem original rather than nem's own live value or one composed
// earlier in this Compose call.
func managedKeys(exported map[string]string, envs ...[]project.EnvVar) map[string]bool {
	keys := make(map[string]bool, len(exported))
	for name := range exported {
		keys[name] = true
	}
	for _, list := range envs {
		for _, e := range list {
			keys[e.Name] = true
		}
	}
	return keys
}

// savedOriginalLookup resolves a name referenced inside an [env] value
// template: a managed key resolves to its saved pre-nem original (via the
// NEM_SAVED__<K>/NEM_SAVED__<K>_SET bookkeeping) so a self-referential
// value like FOO="$FOO:x" is idempotent across repeated composition;
// anything else resolves to the current process env.
func savedOriginalLookup(managed map[string]bool, getenv func(string) (string, bool)) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if managed[name] {
			if _, ok := getenv("NEM_SAVED__" + name + "_SET"); ok {
				return getenv("NEM_SAVED__" + name)
			}
		}
		return getenv(name)
	}
}

// applyEnvSection expands a manifest's [env] entries via lookup, writing
// results into vars (last writer wins) with "nem.toml" recorded in
// sources; a reserved name is skipped with a warning.
func applyEnvSection(entries []project.EnvVar, vars, sources map[string]string, warnings *[]string, lookup func(string) (string, bool)) {
	for _, e := range entries {
		if IsReserved(e.Name) {
			*warnings = append(*warnings, fmt.Sprintf("reserved env var %q skipped", e.Name))
			continue
		}
		vars[e.Name] = Expand(e.Value, lookup)
		sources[e.Name] = "nem.toml"
	}
}
