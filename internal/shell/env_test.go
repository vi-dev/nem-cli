package shell

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/envx"
)

func mapGetenv(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestEnvScriptNewVarPreviouslyUnsetIsSavedAsUnsetAndExported(t *testing.T) {
	res := envx.Result{Vars: []envx.Var{{Name: "FOO", Value: "bar"}}}
	getenv := mapGetenv(map[string]string{})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	if !strings.Contains(out, "export NEM_SAVED__FOO_SET='0'\n") {
		t.Errorf("missing unset-marker save line, got:\n%s", out)
	}
	if !strings.Contains(out, "export FOO='bar'\n") {
		t.Errorf("missing export of new value, got:\n%s", out)
	}
	if !strings.Contains(out, "export NEM_MANAGED_KEYS='FOO'\n") {
		t.Errorf("missing managed-keys export, got:\n%s", out)
	}
}

func TestEnvScriptNewVarPreviouslySetIsSavedAndExported(t *testing.T) {
	res := envx.Result{Vars: []envx.Var{{Name: "FOO", Value: "new"}}}
	getenv := mapGetenv(map[string]string{"FOO": "old"})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	if !strings.Contains(out, "export NEM_SAVED__FOO='old'\n") {
		t.Errorf("missing saved-original line, got:\n%s", out)
	}
	if !strings.Contains(out, "export NEM_SAVED__FOO_SET='1'\n") {
		t.Errorf("missing set-marker save line, got:\n%s", out)
	}
	if !strings.Contains(out, "export FOO='new'\n") {
		t.Errorf("missing export of new value, got:\n%s", out)
	}
}

func TestEnvScriptVarStayingManagedIsNotResaved(t *testing.T) {
	res := envx.Result{Vars: []envx.Var{{Name: "FOO", Value: "v2"}}}
	getenv := mapGetenv(map[string]string{
		"FOO":                "v1",
		"NEM_SAVED__FOO":     "pre-nem",
		"NEM_SAVED__FOO_SET": "1",
		"NEM_MANAGED_KEYS":   "FOO",
	})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	if strings.Contains(out, "NEM_SAVED__FOO=") {
		t.Errorf("must not re-save an already-managed var, got:\n%s", out)
	}
	if strings.Contains(out, "NEM_SAVED__FOO_SET=") {
		t.Errorf("must not re-save the set-marker of an already-managed var, got:\n%s", out)
	}
	if !strings.Contains(out, "export FOO='v2'\n") {
		t.Errorf("missing export of new value, got:\n%s", out)
	}
}

func TestEnvScriptLeavingRestoresPreviouslySetAndUnsetsPreviouslyUnset(t *testing.T) {
	res := envx.Result{} // nothing managed anymore
	getenv := mapGetenv(map[string]string{
		"NEM_MANAGED_KEYS":   "FOO BAR",
		"NEM_SAVED__FOO":     "origval",
		"NEM_SAVED__FOO_SET": "1",
		"NEM_SAVED__BAR_SET": "0",
	})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	if !strings.Contains(out, "export FOO='origval'\n") {
		t.Errorf("missing restore of previously-set var, got:\n%s", out)
	}
	if !strings.Contains(out, "unset BAR\n") {
		t.Errorf("missing unset of previously-unset var, got:\n%s", out)
	}
	if !strings.Contains(out, "unset NEM_SAVED__FOO NEM_SAVED__FOO_SET\n") {
		t.Errorf("missing cleared save bookkeeping for FOO, got:\n%s", out)
	}
	if !strings.Contains(out, "unset NEM_SAVED__BAR NEM_SAVED__BAR_SET\n") {
		t.Errorf("missing cleared save bookkeeping for BAR, got:\n%s", out)
	}
	if !strings.Contains(out, "export NEM_MANAGED_KEYS=''\n") {
		t.Errorf("missing empty managed-keys export, got:\n%s", out)
	}
	if !strings.Contains(out, `export NEM_ORIGINAL_PATH="${NEM_ORIGINAL_PATH-$PATH}"`+"\n") {
		t.Errorf("missing NEM_ORIGINAL_PATH self-establish line, got:\n%s", out)
	}
	if !strings.Contains(out, `export PATH="${NEM_ORIGINAL_PATH:-$PATH}"`+"\n") {
		t.Errorf("leaving must restore PATH to the original, got:\n%s", out)
	}
}

func TestEnvScriptQuotingEscapesSingleQuote(t *testing.T) {
	res := envx.Result{Vars: []envx.Var{{Name: "FOO", Value: "it's"}}}
	getenv := mapGetenv(map[string]string{})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	if !strings.Contains(out, `export FOO='it'\''s'`+"\n") {
		t.Errorf("expected POSIX-escaped single quote, got:\n%s", out)
	}
}

func TestEnvScriptQuotingNeutralizesShellInjection(t *testing.T) {
	malicious := `x'; rm -rf / ; echo $(whoami) ` + "`id`"
	res := envx.Result{Vars: []envx.Var{{Name: "FOO", Value: malicious}}}
	getenv := mapGetenv(map[string]string{})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	expected := "export FOO=" + quote(malicious) + "\n"
	if !strings.Contains(out, expected) {
		t.Errorf("expected exact single-quoted line:\n%s\ngot:\n%s", expected, out)
	}

	// The dangerous run must never appear unquoted/adjacent-to-quote-boundary
	// in a way that would let the shell parse it as a new command.
	if strings.Contains(out, "'; rm -rf / ; echo $(whoami) `id`\n") {
		t.Errorf("value escaped out of its quotes, got:\n%s", out)
	}
}

func TestEnvScriptPathPrependsToOriginalPath(t *testing.T) {
	res := envx.Result{Path: []string{"/a/bin", "/b/bin"}}
	getenv := mapGetenv(map[string]string{})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	if !strings.Contains(out, `export NEM_ORIGINAL_PATH="${NEM_ORIGINAL_PATH-$PATH}"`+"\n") {
		t.Errorf("missing NEM_ORIGINAL_PATH self-establish line, got:\n%s", out)
	}
	if !strings.Contains(out, `export PATH='/a/bin':'/b/bin':"${NEM_ORIGINAL_PATH:-$PATH}"`+"\n") {
		t.Errorf("missing correctly quoted PATH line, got:\n%s", out)
	}
}

func TestEnvScriptPathComponentIsQuoted(t *testing.T) {
	malicious := "/tmp/a'; rm -rf /"
	res := envx.Result{Path: []string{malicious}}
	getenv := mapGetenv(map[string]string{})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	expected := "export PATH=" + quote(malicious) + `:"${NEM_ORIGINAL_PATH:-$PATH}"` + "\n"
	if !strings.Contains(out, expected) {
		t.Errorf("expected exact quoted PATH line:\n%s\ngot:\n%s", expected, out)
	}
}

func TestEnvScriptEmptyPathRestoresOriginalPath(t *testing.T) {
	res := envx.Result{Vars: []envx.Var{{Name: "FOO", Value: "bar"}}}
	getenv := mapGetenv(map[string]string{})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	if !strings.Contains(out, `export NEM_ORIGINAL_PATH="${NEM_ORIGINAL_PATH-$PATH}"`+"\n") {
		t.Errorf("missing NEM_ORIGINAL_PATH self-establish line, got:\n%s", out)
	}
	if !strings.Contains(out, `export PATH="${NEM_ORIGINAL_PATH:-$PATH}"`+"\n") {
		t.Errorf("empty res.Path must restore PATH to the original, got:\n%s", out)
	}
}

func TestEnvScriptManagedKeysListReflectsNewSet(t *testing.T) {
	res := envx.Result{Vars: []envx.Var{{Name: "AAA", Value: "1"}, {Name: "BBB", Value: "2"}}}
	getenv := mapGetenv(map[string]string{})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	if !strings.Contains(out, "export NEM_MANAGED_KEYS='AAA BBB'\n") {
		t.Errorf("missing managed-keys line reflecting new set, got:\n%s", out)
	}
}

func TestEnvScriptFishReturnsError(t *testing.T) {
	out, err := EnvScript(Fish, envx.Result{}, mapGetenv(nil))
	if err == nil {
		t.Fatal("expected an error for Fish dialect, got nil")
	}
	if err.Error() != "fish is not supported yet" {
		t.Errorf("unexpected error message: %q", err.Error())
	}
	if out != "" {
		t.Errorf("expected empty script on error, got:\n%s", out)
	}
}

func TestEnvScriptUnknownDialectReturnsError(t *testing.T) {
	out, err := EnvScript(Dialect(99), envx.Result{}, mapGetenv(nil))
	if err == nil {
		t.Fatal("expected an error for an unknown dialect, got nil")
	}
	if out != "" {
		t.Errorf("expected empty script on error, got:\n%s", out)
	}
}

func TestEnvScriptRestoreRejectsHostileManagedKeyNameWithSemicolon(t *testing.T) {
	res := envx.Result{}
	getenv := mapGetenv(map[string]string{"NEM_MANAGED_KEYS": "FOO;touch /tmp/x"})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	if strings.Contains(out, "FOO;touch") || strings.Contains(out, "touch") || strings.Contains(out, ";") {
		t.Errorf("hostile managed-key name reached command position, got:\n%s", out)
	}
	if !strings.Contains(out, "export NEM_MANAGED_KEYS=''\n") {
		t.Errorf("missing empty managed-keys export, got:\n%s", out)
	}
}

func TestEnvScriptRestoreRejectsHostileManagedKeyNameNoWhitespace(t *testing.T) {
	res := envx.Result{}
	getenv := mapGetenv(map[string]string{"NEM_MANAGED_KEYS": "FOO;id>/tmp/x"})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	if strings.Contains(out, "FOO;id") || strings.Contains(out, ";") || strings.Contains(out, ">") {
		t.Errorf("hostile managed-key name reached command position, got:\n%s", out)
	}
}

func TestEnvScriptRestoreRejectsHostileManagedKeyNameCommandSubstitution(t *testing.T) {
	res := envx.Result{}
	getenv := mapGetenv(map[string]string{"NEM_MANAGED_KEYS": "$(id)"})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	if strings.Contains(out, "$(id)") || strings.Contains(out, "$(") {
		t.Errorf("hostile managed-key name reached command position, got:\n%s", out)
	}
}

func TestEnvScriptRestoreValidSpaceSeparatedNamesBothRestore(t *testing.T) {
	res := envx.Result{}
	getenv := mapGetenv(map[string]string{
		"NEM_MANAGED_KEYS": "A B",
		"NEM_SAVED__A":     "origA",
		"NEM_SAVED__A_SET": "1",
		"NEM_SAVED__B_SET": "0",
	})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	if !strings.Contains(out, "export A='origA'\n") {
		t.Errorf("missing restore of A, got:\n%s", out)
	}
	if !strings.Contains(out, "unset B\n") {
		t.Errorf("missing unset of B, got:\n%s", out)
	}
}

func TestEnvScriptRestoreDedupsRepeatedManagedKey(t *testing.T) {
	res := envx.Result{}
	getenv := mapGetenv(map[string]string{
		"NEM_MANAGED_KEYS": "A A",
		"NEM_SAVED__A_SET": "0",
	})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	if got := strings.Count(out, "unset A\n"); got != 1 {
		t.Errorf("expected exactly one restore of A, got %d, output:\n%s", got, out)
	}
	if got := strings.Count(out, "unset NEM_SAVED__A NEM_SAVED__A_SET\n"); got != 1 {
		t.Errorf("expected exactly one cleared bookkeeping for A, got %d, output:\n%s", got, out)
	}
}

func TestEnvScriptNewVarHostileNameIsSkipped(t *testing.T) {
	res := envx.Result{Vars: []envx.Var{
		{Name: "FOO;touch /tmp/x", Value: "whatever"},
		{Name: "GOOD", Value: "1"},
	}}
	getenv := mapGetenv(map[string]string{})

	out, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript: %v", err)
	}

	if strings.Contains(out, "touch") || strings.Contains(out, ";") {
		t.Errorf("hostile var name reached command position, got:\n%s", out)
	}
	if !strings.Contains(out, "export GOOD='1'\n") {
		t.Errorf("missing export of valid neighboring var, got:\n%s", out)
	}
	if !strings.Contains(out, "export NEM_MANAGED_KEYS='GOOD'\n") {
		t.Errorf("hostile name must not appear in managed-keys list, got:\n%s", out)
	}
}

func TestEnvScriptRepeatedEvalDoesNotGrowPath(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}

	res := envx.Result{Path: []string{"/a/bin", "/b/bin"}}

	firstScript, err := EnvScript(Bash, res, mapGetenv(map[string]string{"PATH": "/usr/bin:/bin"}))
	if err != nil {
		t.Fatalf("EnvScript (first eval): %v", err)
	}
	firstEnv := sourceInBash(t, bashPath, map[string]string{"PATH": "/usr/bin:/bin"}, firstScript)

	secondScript, err := EnvScript(Bash, res, mapGetenv(map[string]string{
		"PATH":              firstEnv["PATH"],
		"NEM_ORIGINAL_PATH": firstEnv["NEM_ORIGINAL_PATH"],
		"NEM_MANAGED_KEYS":  firstEnv["NEM_MANAGED_KEYS"],
	}))
	if err != nil {
		t.Fatalf("EnvScript (second eval): %v", err)
	}
	secondEnv := sourceInBash(t, bashPath, map[string]string{
		"PATH":              firstEnv["PATH"],
		"NEM_ORIGINAL_PATH": firstEnv["NEM_ORIGINAL_PATH"],
	}, secondScript)

	if secondEnv["PATH"] != firstEnv["PATH"] {
		t.Errorf("PATH grew across a second eval:\nfirst:  %s\nsecond: %s", firstEnv["PATH"], secondEnv["PATH"])
	}
	if got := strings.Count(secondEnv["PATH"], "/a/bin"); got != 1 {
		t.Errorf("expected /a/bin exactly once in PATH, found %d: %s", got, secondEnv["PATH"])
	}
	if secondEnv["NEM_ORIGINAL_PATH"] != firstEnv["NEM_ORIGINAL_PATH"] {
		t.Errorf("NEM_ORIGINAL_PATH drifted across a second eval:\nfirst:  %s\nsecond: %s", firstEnv["NEM_ORIGINAL_PATH"], secondEnv["NEM_ORIGINAL_PATH"])
	}
}

// sourceInBash runs script under bash with exactly env as its process
// environment, then reports PATH, NEM_ORIGINAL_PATH and
// NEM_MANAGED_KEYS as they stand once the script finishes.
func sourceInBash(t *testing.T, bashPath string, env map[string]string, script string) map[string]string {
	t.Helper()

	full := script +
		"printf 'PATH\x1f%s\x1e' \"$PATH\"\n" +
		"printf 'NEM_ORIGINAL_PATH\x1f%s\x1e' \"$NEM_ORIGINAL_PATH\"\n" +
		"printf 'NEM_MANAGED_KEYS\x1f%s\x1e' \"$NEM_MANAGED_KEYS\"\n"

	cmd := exec.Command(bashPath, "-c", full)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sourcing generated script failed: %v", err)
	}

	result := map[string]string{}
	for _, rec := range strings.Split(string(out), "\x1e") {
		parts := strings.SplitN(rec, "\x1f", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result
}

func TestEnvScriptBashAndZshBodiesAreIdentical(t *testing.T) {
	res := envx.Result{
		Vars: []envx.Var{{Name: "FOO", Value: "bar"}},
		Path: []string{"/a/bin"},
	}
	getenv := mapGetenv(map[string]string{"NEM_MANAGED_KEYS": "OLD", "NEM_SAVED__OLD_SET": "0"})

	bashOut, err := EnvScript(Bash, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript(Bash): %v", err)
	}
	zshOut, err := EnvScript(Zsh, res, getenv)
	if err != nil {
		t.Fatalf("EnvScript(Zsh): %v", err)
	}

	if bashOut != zshOut {
		t.Errorf("bash and zsh bodies differ:\nbash:\n%s\nzsh:\n%s", bashOut, zshOut)
	}
}
