package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/shell"
)

func TestActivateZshPrintPrintsBlockToStdout(t *testing.T) {
	nemHomeDir := t.TempDir()

	out, errb, err := runNem(t, nemHomeDir, "activate", "zsh", "--print")
	if err != nil {
		t.Fatalf("activate --print: %v\n%s", err, errb)
	}
	if out != shell.HookBlock(shell.Zsh) {
		t.Fatalf("stdout does not match HookBlock:\n%s", out)
	}
}

func TestActivateZshInstallsIntoRcAndDeactivateRemoves(t *testing.T) {
	nemHomeDir := t.TempDir()
	rcHome := t.TempDir()
	t.Setenv("HOME", rcHome)

	orig := stdoutIsTTY
	stdoutIsTTY = func() bool { return true }
	defer func() { stdoutIsTTY = orig }()

	_, errb, err := runNem(t, nemHomeDir, "activate", "zsh")
	if err != nil {
		t.Fatalf("activate: %v\n%s", err, errb)
	}
	if !strings.Contains(errb, "Activated nem for zsh") {
		t.Fatalf("missing activation narration: %q", errb)
	}

	rcPath := filepath.Join(rcHome, ".zshrc")
	data, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf("read rc: %v", err)
	}
	if !strings.Contains(string(data), shell.BeginMarker) {
		t.Fatalf("rc file missing installed block:\n%s", data)
	}

	_, errb2, err := runNem(t, nemHomeDir, "deactivate", "zsh")
	if err != nil {
		t.Fatalf("deactivate: %v\n%s", err, errb2)
	}
	if !strings.Contains(errb2, "Deactivated nem for zsh") {
		t.Fatalf("missing deactivation narration: %q", errb2)
	}

	data2, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf("read rc after deactivate: %v", err)
	}
	if strings.Contains(string(data2), shell.BeginMarker) {
		t.Fatalf("rc file still contains block after deactivate:\n%s", data2)
	}
}

func TestActivateFishUnsupported(t *testing.T) {
	nemHomeDir := t.TempDir()

	if _, _, err := runNem(t, nemHomeDir, "activate", "fish", "--print"); err == nil {
		t.Fatal("want error for fish shell")
	}
}
