package install_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/spec"
)

func pkgWith(actions ...spec.Action) *spec.Package {
	return &spec.Package{Name: "test", Install: actions}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeArtifact(t *testing.T, dir string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, "artifact")
	writeFile(t, path, data)
	return path
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to not exist, stat err = %v", path, err)
	}
}

func TestRunActionsCopyArtifactToken(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, []byte("artifact-bytes"))

	pkg := pkgWith(spec.Action{Copy: &spec.CopyAction{Src: "{{.Artifact}}", Dst: "bin/tool"}})
	if err := install.RunActions(pkg, staging, artifact, "v1", spec.Current()); err != nil {
		t.Fatalf("RunActions: %v", err)
	}

	dst := filepath.Join(staging, "bin", "tool")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "artifact-bytes" {
		t.Fatalf("dst content = %q, want %q", got, "artifact-bytes")
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("dst mode = %v, want 0644 (default)", info.Mode().Perm())
	}
}

func TestRunActionsCopyStagingRelative(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	writeFile(t, filepath.Join(staging, "src.txt"), []byte("hello"))
	artifact := writeArtifact(t, tmp, []byte("unused"))

	pkg := pkgWith(spec.Action{Copy: &spec.CopyAction{Src: "src.txt", Dst: "out/dst.txt", Mode: 0o600}})
	if err := install.RunActions(pkg, staging, artifact, "v1", spec.Current()); err != nil {
		t.Fatalf("RunActions: %v", err)
	}

	dst := filepath.Join(staging, "out", "dst.txt")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("dst content = %q, want %q", got, "hello")
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("dst mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestRunActionsCopyDstContainmentViolation(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, []byte("data"))

	pkg := pkgWith(spec.Action{Copy: &spec.CopyAction{Src: "{{.Artifact}}", Dst: "../evil"}})
	err := install.RunActions(pkg, staging, artifact, "v1", spec.Current())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "escapes staging dir") {
		t.Fatalf("error = %v, want containment error", err)
	}
	if !strings.HasPrefix(err.Error(), "install[0]:") {
		t.Fatalf("error = %v, want install[0]: prefix", err)
	}
	mustNotExist(t, filepath.Join(tmp, "evil"))
}

func TestRunActionsCopySrcContainmentViolation(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, []byte("data"))

	pkg := pkgWith(spec.Action{Copy: &spec.CopyAction{Src: "../evil", Dst: "dst.txt"}})
	err := install.RunActions(pkg, staging, artifact, "v1", spec.Current())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "escapes staging dir") {
		t.Fatalf("error = %v, want containment error", err)
	}
}

func TestRunActionsMove(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	writeFile(t, filepath.Join(staging, "src.txt"), []byte("move-me"))
	artifact := writeArtifact(t, tmp, []byte("unused"))

	pkg := pkgWith(spec.Action{Move: &spec.MoveAction{Src: "src.txt", Dst: "dst.txt"}})
	if err := install.RunActions(pkg, staging, artifact, "v1", spec.Current()); err != nil {
		t.Fatalf("RunActions: %v", err)
	}

	mustNotExist(t, filepath.Join(staging, "src.txt"))
	got, err := os.ReadFile(filepath.Join(staging, "dst.txt"))
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "move-me" {
		t.Fatalf("dst content = %q, want %q", got, "move-me")
	}
}

func TestRunActionsMoveSrcContainmentViolation(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, []byte("unused"))

	pkg := pkgWith(spec.Action{Move: &spec.MoveAction{Src: "../evil", Dst: "dst.txt"}})
	err := install.RunActions(pkg, staging, artifact, "v1", spec.Current())
	if err == nil || !strings.Contains(err.Error(), "escapes staging dir") {
		t.Fatalf("error = %v, want containment error", err)
	}
}

func TestRunActionsMoveDstContainmentViolation(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	writeFile(t, filepath.Join(staging, "src.txt"), []byte("data"))
	artifact := writeArtifact(t, tmp, []byte("unused"))

	pkg := pkgWith(spec.Action{Move: &spec.MoveAction{Src: "src.txt", Dst: "../evil"}})
	err := install.RunActions(pkg, staging, artifact, "v1", spec.Current())
	if err == nil || !strings.Contains(err.Error(), "escapes staging dir") {
		t.Fatalf("error = %v, want containment error", err)
	}
	mustNotExist(t, filepath.Join(tmp, "evil"))
}

func TestRunActionsMkdir(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, []byte("unused"))

	pkg := pkgWith(spec.Action{Mkdir: "a/b/c"})
	if err := install.RunActions(pkg, staging, artifact, "v1", spec.Current()); err != nil {
		t.Fatalf("RunActions: %v", err)
	}
	info, err := os.Stat(filepath.Join(staging, "a", "b", "c"))
	if err != nil {
		t.Fatalf("stat mkdir target: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("mkdir target is not a directory")
	}
}

func TestRunActionsMkdirContainmentViolation(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, []byte("unused"))

	pkg := pkgWith(spec.Action{Mkdir: "../evil"})
	err := install.RunActions(pkg, staging, artifact, "v1", spec.Current())
	if err == nil || !strings.Contains(err.Error(), "escapes staging dir") {
		t.Fatalf("error = %v, want containment error", err)
	}
	mustNotExist(t, filepath.Join(tmp, "evil"))
}

func TestRunActionsSkipsNonMatchingPlatform(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, []byte("unused"))

	pkg := pkgWith(
		spec.Action{Mkdir: "always"},
		spec.Action{Mkdir: "darwin-only", Platforms: []spec.Platform{{OS: "darwin"}}},
		spec.Action{Mkdir: "linux-any", Platforms: []spec.Platform{{OS: "linux"}}},
		spec.Action{Mkdir: "../evil", Platforms: []spec.Platform{{OS: "darwin", Arch: "arm64"}}},
	)
	if err := install.RunActions(pkg, staging, artifact, "v1", spec.Platform{OS: "linux", Arch: "amd64"}); err != nil {
		t.Fatalf("RunActions: %v", err)
	}

	for _, dir := range []string{"always", "linux-any"} {
		if _, err := os.Stat(filepath.Join(staging, dir)); err != nil {
			t.Fatalf("matching action %s did not run: %v", dir, err)
		}
	}
	mustNotExist(t, filepath.Join(staging, "darwin-only"))
	mustNotExist(t, filepath.Join(tmp, "evil"))
}

func TestRunActionsErrorsWhenAllActionsFiltered(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, []byte("unused"))

	pkg := pkgWith(spec.Action{Mkdir: "a", Platforms: []spec.Platform{{OS: "darwin"}}})
	err := install.RunActions(pkg, staging, artifact, "v1", spec.Platform{OS: "linux", Arch: "amd64"})
	if err == nil || !strings.Contains(err.Error(), "linux/amd64") {
		t.Fatalf("want no-applicable-action error naming the platform, got %v", err)
	}
	mustNotExist(t, filepath.Join(staging, "a"))
}

func TestRunActionsSkipKeepsManifestIndexInErrors(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, []byte("unused"))

	pkg := pkgWith(
		spec.Action{Mkdir: "skipped", Platforms: []spec.Platform{{OS: "darwin"}}},
		spec.Action{Mkdir: "../evil"},
	)
	err := install.RunActions(pkg, staging, artifact, "v1", spec.Platform{OS: "linux", Arch: "amd64"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "install[1]:") {
		t.Fatalf("error = %v, want install[1]: prefix despite skipped action", err)
	}
}

func TestRunActionsEmptyActionErrors(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, []byte("unused"))

	pkg := pkgWith(spec.Action{})
	err := install.RunActions(pkg, staging, artifact, "v1", spec.Current())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "install[0]: empty action" {
		t.Fatalf("error = %q, want %q", err.Error(), "install[0]: empty action")
	}
}

func TestRunActionsLoopWrapsFailingIndex(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, []byte("unused"))

	pkg := pkgWith(
		spec.Action{Mkdir: "a"},
		spec.Action{Mkdir: "../evil"},
	)
	err := install.RunActions(pkg, staging, artifact, "v1", spec.Current())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "install[1]:") {
		t.Fatalf("error = %v, want install[1]: prefix", err)
	}
	if _, statErr := os.Stat(filepath.Join(staging, "a")); statErr != nil {
		t.Fatalf("first action's effect missing: %v", statErr)
	}
}

func TestRunActionsExpandsTemplates(t *testing.T) {
	staging := t.TempDir()
	plat := spec.Current()
	writeFile(t, filepath.Join(staging, "d", "tool-"+plat.Arch), []byte("bin"))
	artifact := writeArtifact(t, t.TempDir(), []byte("unused"))

	pkg := pkgWith(spec.Action{Copy: &spec.CopyAction{Src: "d/tool-{{.Arch}}", Dst: "bin/tool-{{.Version}}", Mode: 0o755}})
	if err := install.RunActions(pkg, staging, artifact, "9.9.9", plat); err != nil {
		t.Fatalf("RunActions: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "bin", "tool-9.9.9")); err != nil {
		t.Fatalf("expected expanded copy dst: %v", err)
	}
}
