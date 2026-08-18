package shell

import (
	"strings"
	"testing"
)

func TestHookBlockBashSavesOriginalPathOnce(t *testing.T) {
	out := HookBlock(Bash)
	if !strings.Contains(out, "export NEM_ORIGINAL_PATH=\"${NEM_ORIGINAL_PATH-$PATH}\"\n") {
		t.Errorf("missing NEM_ORIGINAL_PATH save line, got:\n%s", out)
	}
}

func TestHookBlockZshSavesOriginalPathOnce(t *testing.T) {
	out := HookBlock(Zsh)
	if !strings.Contains(out, "export NEM_ORIGINAL_PATH=\"${NEM_ORIGINAL_PATH-$PATH}\"\n") {
		t.Errorf("missing NEM_ORIGINAL_PATH save line, got:\n%s", out)
	}
}

func TestHookBlockBashRegistersPromptCommandHookWithoutClobbering(t *testing.T) {
	out := HookBlock(Bash)
	for _, want := range []string{"PROMPT_COMMAND", "__nem_hook", "${PROMPT_COMMAND:+"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in PROMPT_COMMAND chaining, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "add-zsh-hook") {
		t.Errorf("bash block must not reference add-zsh-hook, got:\n%s", out)
	}
}

func TestHookBlockZshRegistersChpwdHook(t *testing.T) {
	out := HookBlock(Zsh)
	for _, want := range []string{"autoload -Uz add-zsh-hook", "add-zsh-hook chpwd __nem_hook"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in chpwd hook registration, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "PROMPT_COMMAND") {
		t.Errorf("zsh block must not reference PROMPT_COMMAND, got:\n%s", out)
	}
}

func TestHookBlockBashHasEvalLine(t *testing.T) {
	out := HookBlock(Bash)
	if !strings.Contains(out, `eval "$(command nem env --shell bash)"`) {
		t.Errorf("missing eval line for bash, got:\n%s", out)
	}
}

func TestHookBlockZshHasEvalLine(t *testing.T) {
	out := HookBlock(Zsh)
	if !strings.Contains(out, `eval "$(command nem env --shell zsh)"`) {
		t.Errorf("missing eval line for zsh, got:\n%s", out)
	}
}

func TestHookBlockRunsHookOnceAtLoad(t *testing.T) {
	for _, d := range []Dialect{Bash, Zsh} {
		out := HookBlock(d)
		if !strings.Contains(out, "__nem_hook() {") {
			t.Fatalf("hook function definition missing, got:\n%s", out)
		}
		// A bare, unindented call activates the current directory immediately
		// at load time, distinct from the function definition and hook
		// registration lines above it.
		if !strings.Contains(out, "\n__nem_hook\n") {
			t.Errorf("expected a standalone call to __nem_hook to run at load, got:\n%s", out)
		}
	}
}

func TestHookBlockWrapsNemAndReevaluatesOnMutatingCommands(t *testing.T) {
	for _, d := range []Dialect{Bash, Zsh} {
		out := HookBlock(d)
		for _, want := range []string{"nem() {", `command nem "$@"`, `case "$1" in`, "use|unuse|lock|sync)"} {
			if !strings.Contains(out, want) {
				t.Errorf("expected %q in the nem() wrapper, got:\n%s", want, out)
			}
		}
	}
}

func TestHookBlockNemWrapperPreservesExitStatus(t *testing.T) {
	for _, d := range []Dialect{Bash, Zsh} {
		out := HookBlock(d)
		for _, want := range []string{"local __nem_rc=$?", "return $__nem_rc"} {
			if !strings.Contains(out, want) {
				t.Errorf("expected %q so the nem() wrapper propagates the wrapped command's exit status, got:\n%s", want, out)
			}
		}
	}
}

func TestHookBlockSourcesCompletionsBestEffort(t *testing.T) {
	out := HookBlock(Bash)
	if !strings.Contains(out, "source <(command nem completion bash 2>/dev/null) 2>/dev/null || true") {
		t.Errorf("expected best-effort completion sourcing, got:\n%s", out)
	}

	out = HookBlock(Zsh)
	if !strings.Contains(out, "source <(command nem completion zsh 2>/dev/null) 2>/dev/null || true") {
		t.Errorf("expected best-effort completion sourcing, got:\n%s", out)
	}
}

func TestHookBlockDoesNotContainMarkers(t *testing.T) {
	for _, d := range []Dialect{Bash, Zsh} {
		out := HookBlock(d)
		if strings.Contains(out, BeginMarker) || strings.Contains(out, EndMarker) {
			t.Errorf("HookBlock must not embed its own markers, got:\n%s", out)
		}
	}
}

func TestHookBlockUnknownDialectReturnsEmpty(t *testing.T) {
	if out := HookBlock(Fish); out != "" {
		t.Errorf("expected empty block for Fish, got:\n%s", out)
	}
	if out := HookBlock(Dialect(99)); out != "" {
		t.Errorf("expected empty block for an unrecognized dialect, got:\n%s", out)
	}
}
