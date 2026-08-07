package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallBlockAppendsToFileWithExistingContent(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), ".bashrc")
	if err := os.WriteFile(rcPath, []byte("existing content\n"), 0o644); err != nil {
		t.Fatalf("seed rc file: %v", err)
	}

	if err := InstallBlock(rcPath, Bash); err != nil {
		t.Fatalf("InstallBlock: %v", err)
	}

	got := readFile(t, rcPath)
	if !strings.HasPrefix(got, "existing content\n") {
		t.Errorf("expected existing content preserved as a prefix, got:\n%s", got)
	}
	if strings.Count(got, BeginMarker) != 1 || strings.Count(got, EndMarker) != 1 {
		t.Errorf("expected exactly one marked block, got:\n%s", got)
	}
	if !strings.Contains(got, HookBlock(Bash)) {
		t.Errorf("expected the bash hook block body, got:\n%s", got)
	}
}

func TestInstallBlockOnMissingFileCreatesIt(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), ".bashrc")

	if err := InstallBlock(rcPath, Bash); err != nil {
		t.Fatalf("InstallBlock: %v", err)
	}

	got := readFile(t, rcPath)
	if strings.Count(got, BeginMarker) != 1 || strings.Count(got, EndMarker) != 1 {
		t.Errorf("expected exactly one marked block in newly created file, got:\n%s", got)
	}
}

func TestInstallBlockCalledTwiceIsIdempotent(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), ".bashrc")
	if err := os.WriteFile(rcPath, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed rc file: %v", err)
	}

	if err := InstallBlock(rcPath, Bash); err != nil {
		t.Fatalf("InstallBlock (first): %v", err)
	}
	if err := InstallBlock(rcPath, Bash); err != nil {
		t.Fatalf("InstallBlock (second): %v", err)
	}

	got := readFile(t, rcPath)
	if strings.Count(got, BeginMarker) != 1 || strings.Count(got, EndMarker) != 1 {
		t.Errorf("expected exactly one marked block after two installs, got:\n%s", got)
	}
	if !strings.HasPrefix(got, "before\n") {
		t.Errorf("expected pre-existing content preserved, got:\n%s", got)
	}
}

func TestInstallBlockSecondCallReplacesContentForNewDialect(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), ".rc")

	if err := InstallBlock(rcPath, Bash); err != nil {
		t.Fatalf("InstallBlock (bash): %v", err)
	}
	if err := InstallBlock(rcPath, Zsh); err != nil {
		t.Fatalf("InstallBlock (zsh): %v", err)
	}

	got := readFile(t, rcPath)
	if strings.Count(got, BeginMarker) != 1 || strings.Count(got, EndMarker) != 1 {
		t.Errorf("expected exactly one marked block, got:\n%s", got)
	}
	if strings.Contains(got, "PROMPT_COMMAND") {
		t.Errorf("expected bash-specific content replaced by zsh content, got:\n%s", got)
	}
	if !strings.Contains(got, "add-zsh-hook chpwd __nem_hook") {
		t.Errorf("expected zsh hook content present, got:\n%s", got)
	}
}

func TestInstallBlockPreservesContentAfterAnExistingBlock(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), ".rc")
	seed := "before\n" + blockFor("before\n", HookBlock(Bash)) + "after\n"
	if err := os.WriteFile(rcPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed rc file: %v", err)
	}

	if err := InstallBlock(rcPath, Zsh); err != nil {
		t.Fatalf("InstallBlock: %v", err)
	}

	got := readFile(t, rcPath)
	if !strings.HasPrefix(got, "before\n") {
		t.Errorf("expected content before the block preserved, got:\n%s", got)
	}
	if !strings.HasSuffix(got, "after\n") {
		t.Errorf("expected content after the block preserved, got:\n%s", got)
	}
	if strings.Count(got, BeginMarker) != 1 || strings.Count(got, EndMarker) != 1 {
		t.Errorf("expected exactly one marked block, got:\n%s", got)
	}
}

func TestRemoveBlockDeletesBlockAndIsExactRoundTrip(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), ".bashrc")
	if err := os.WriteFile(rcPath, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed rc file: %v", err)
	}

	if err := InstallBlock(rcPath, Bash); err != nil {
		t.Fatalf("InstallBlock: %v", err)
	}
	if err := RemoveBlock(rcPath); err != nil {
		t.Fatalf("RemoveBlock: %v", err)
	}

	got := readFile(t, rcPath)
	if got != "before\n" {
		t.Errorf("expected exact round trip back to original content, got:\n%s", got)
	}
}

func TestRemoveBlockPreservesContentBeforeAndAfterBlock(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), ".rc")
	seed := "before\n" + blockFor("before\n", HookBlock(Bash)) + "after\n"
	if err := os.WriteFile(rcPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed rc file: %v", err)
	}

	if err := RemoveBlock(rcPath); err != nil {
		t.Fatalf("RemoveBlock: %v", err)
	}

	got := readFile(t, rcPath)
	if got != "before\nafter\n" {
		t.Errorf("expected surrounding content preserved with the block removed, got:\n%s", got)
	}
}

func TestRemoveBlockNoOpWhenBlockAbsent(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), ".bashrc")
	original := "some unrelated content\nwith multiple lines\n"
	if err := os.WriteFile(rcPath, []byte(original), 0o644); err != nil {
		t.Fatalf("seed rc file: %v", err)
	}

	if err := RemoveBlock(rcPath); err != nil {
		t.Fatalf("RemoveBlock: %v", err)
	}

	got := readFile(t, rcPath)
	if got != original {
		t.Errorf("expected file untouched when no block is present, got:\n%s", got)
	}
}

func TestRemoveBlockNoOpWhenFileMissing(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), ".bashrc")

	if err := RemoveBlock(rcPath); err != nil {
		t.Fatalf("RemoveBlock: %v", err)
	}

	if _, err := os.Stat(rcPath); !os.IsNotExist(err) {
		t.Errorf("expected RemoveBlock not to create a missing file, stat err: %v", err)
	}
}

func TestInstallBlockThenRemoveBlockRoundTripsExactlyWithNoTrailingNewline(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), ".bashrc")
	if err := os.WriteFile(rcPath, []byte("before"), 0o644); err != nil {
		t.Fatalf("seed rc file: %v", err)
	}

	if err := InstallBlock(rcPath, Bash); err != nil {
		t.Fatalf("InstallBlock: %v", err)
	}
	if err := RemoveBlock(rcPath); err != nil {
		t.Fatalf("RemoveBlock: %v", err)
	}

	if got := readFile(t, rcPath); got != "before" {
		t.Errorf("expected exact round trip back to original content with no trailing newline, got %q", got)
	}
}

func TestInstallBlockOnEmptyFileLeavesNoLeadingBlankLine(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), ".bashrc")

	if err := InstallBlock(rcPath, Bash); err != nil {
		t.Fatalf("InstallBlock: %v", err)
	}

	got := readFile(t, rcPath)
	if !strings.HasPrefix(got, BeginMarker) {
		t.Errorf("expected the block to start the file directly with no leading blank line, got %q", got)
	}
}

func TestInstallBlockPreservesExistingFileMode(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), ".bashrc")
	if err := os.WriteFile(rcPath, []byte("before\n"), 0o600); err != nil {
		t.Fatalf("seed rc file: %v", err)
	}

	if err := InstallBlock(rcPath, Bash); err != nil {
		t.Fatalf("InstallBlock: %v", err)
	}

	if got := fileMode(t, rcPath); got != 0o600 {
		t.Errorf("expected mode 0o600 preserved, got %o", got)
	}
}

func TestInstallBlockReplacingAnExistingBlockAlsoPreservesMode(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), ".bashrc")
	if err := os.WriteFile(rcPath, []byte("before\n"), 0o600); err != nil {
		t.Fatalf("seed rc file: %v", err)
	}
	if err := InstallBlock(rcPath, Bash); err != nil {
		t.Fatalf("InstallBlock (first): %v", err)
	}

	if err := InstallBlock(rcPath, Zsh); err != nil {
		t.Fatalf("InstallBlock (second): %v", err)
	}

	if got := fileMode(t, rcPath); got != 0o600 {
		t.Errorf("expected mode 0o600 preserved across a block replace, got %o", got)
	}
}

func TestInstallBlockOnMissingFileUsesDefaultMode(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), ".bashrc")

	if err := InstallBlock(rcPath, Bash); err != nil {
		t.Fatalf("InstallBlock: %v", err)
	}

	if got := fileMode(t, rcPath); got != rcFileMode {
		t.Errorf("expected default mode %o for a newly created file, got %o", rcFileMode, got)
	}
}

func TestRemoveBlockPreservesExistingFileMode(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), ".bashrc")
	if err := os.WriteFile(rcPath, []byte("before\n"), 0o600); err != nil {
		t.Fatalf("seed rc file: %v", err)
	}
	if err := InstallBlock(rcPath, Bash); err != nil {
		t.Fatalf("InstallBlock: %v", err)
	}

	if err := RemoveBlock(rcPath); err != nil {
		t.Fatalf("RemoveBlock: %v", err)
	}

	if got := fileMode(t, rcPath); got != 0o600 {
		t.Errorf("expected mode 0o600 preserved, got %o", got)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
