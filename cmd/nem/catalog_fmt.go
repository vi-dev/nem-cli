package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/spec"
)

func newCatalogFmtCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "fmt [dir|pkg.yaml]",
		Short: "Rewrite package manifests to canonical form",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "."
			if len(args) == 1 {
				target = args[0]
			}
			paths, err := manifestPaths(target)
			if err != nil {
				return err
			}
			dirty := 0
			for _, p := range paths {
				data, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				formatted, err := spec.Format(data)
				if err != nil {
					return fmt.Errorf("%s: %w", p, err)
				}
				if bytes.Equal(data, formatted) {
					continue
				}
				dirty++
				if check {
					fmt.Fprintln(cmd.OutOrStdout(), p)
					continue
				}
				if err := writeManifest(p, formatted); err != nil {
					return err
				}
				console.Success("Formatted %s", p)
			}
			if check && dirty > 0 {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "list non-canonical manifests and exit 1 instead of rewriting")
	return cmd
}

// manifestPaths resolves target — a catalog dir laid out as
// pkgs/<name>/pkg.yaml, or a single manifest file — to manifest paths.
func manifestPaths(target string) ([]string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{target}, nil
	}
	pkgsDir := filepath.Join(target, "pkgs")
	entries, err := os.ReadDir(pkgsDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pkgsDir, err)
	}
	var out []string
	for _, de := range entries {
		if !de.IsDir() {
			continue
		}
		p := filepath.Join(pkgsDir, de.Name(), "pkg.yaml")
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no package manifests under %s", target)
	}
	return out, nil
}

// writeManifest replaces path's content atomically: write a temp file in
// the same directory, then rename over the original.
func writeManifest(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".pkg-*.yaml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
