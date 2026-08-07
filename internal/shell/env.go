// Package shell renders eval-able shell scripts that apply a composed
// environment (see internal/envx) to an interactive shell, following the
// save/restore contract that lets leaving a project restore prior state
// exactly.
package shell

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vi-dev/nem-cli/internal/envx"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// Dialect selects the shell syntax EnvScript renders.
type Dialect int

const (
	// Bash renders a script for bash.
	Bash Dialect = iota
	// Zsh renders a script for zsh; its body is identical to Bash's.
	Zsh
	// Fish is reserved; EnvScript rejects it until fish support lands.
	Fish
)

const managedKeysVar = "NEM_MANAGED_KEYS"

// EnvScript renders a script that applies res to the shell environment
// described by getenv, implementing the save/restore contract: a
// variable entering the managed set has its pre-nem value recorded
// before being overwritten, and a variable leaving the managed set is
// restored to that recorded value (or unset, if it had none). A
// variable name — whether from res.Vars or read back out of a prior
// NEM_MANAGED_KEYS — is only ever emitted bare (in command position) if
// it matches spec.EnvNameRE; anything else is silently dropped, since a
// name can't be meaningfully quoted where it appears. Every emitted
// value is single-quoted so no manifest or environment content reaches
// the shell parser unescaped.
func EnvScript(d Dialect, res envx.Result, getenv func(string) (string, bool)) (string, error) {
	switch d {
	case Bash, Zsh:
	case Fish:
		return "", errors.New("fish is not supported yet")
	default:
		return "", fmt.Errorf("unsupported shell dialect: %d", int(d))
	}

	newKeys := make(map[string]bool, len(res.Vars))
	newKeyList := make([]string, 0, len(res.Vars))

	var b strings.Builder
	for _, v := range res.Vars {
		if !spec.EnvNameRE.MatchString(v.Name) {
			continue
		}
		newKeys[v.Name] = true
		newKeyList = append(newKeyList, v.Name)
		writeSave(&b, v.Name, getenv)
		fmt.Fprintf(&b, "export %s=%s\n", v.Name, quote(v.Value))
	}

	priorList, _ := getenv(managedKeysVar)
	seen := make(map[string]bool)
	for _, k := range strings.Fields(priorList) {
		if !spec.EnvNameRE.MatchString(k) || seen[k] {
			continue
		}
		seen[k] = true
		if newKeys[k] {
			continue
		}
		writeRestore(&b, k, getenv)
	}

	writePath(&b, res.Path)

	fmt.Fprintf(&b, "export %s=%s\n", managedKeysVar, quote(strings.Join(newKeyList, " ")))

	return b.String(), nil
}

// writeSave records name's pre-nem value the first time it becomes
// managed: NEM_SAVED__<name> holds the current value (when set) and
// NEM_SAVED__<name>_SET records whether it was set at all. A var that
// was already managed in a prior eval (its _SET marker already present)
// is left alone so its recorded original is never overwritten with
// nem's own live value.
func writeSave(b *strings.Builder, name string, getenv func(string) (string, bool)) {
	savedSetName := "NEM_SAVED__" + name + "_SET"
	if _, ok := getenv(savedSetName); ok {
		return
	}

	savedName := "NEM_SAVED__" + name
	current, isSet := getenv(name)
	if isSet {
		fmt.Fprintf(b, "export %s=%s\n", savedName, quote(current))
		fmt.Fprintf(b, "export %s=%s\n", savedSetName, quote("1"))
		return
	}
	fmt.Fprintf(b, "export %s=%s\n", savedSetName, quote("0"))
}

// writeRestore returns name to its recorded pre-nem state and clears
// its save bookkeeping. Callers must have already validated name.
func writeRestore(b *strings.Builder, name string, getenv func(string) (string, bool)) {
	savedName := "NEM_SAVED__" + name
	savedSetName := savedName + "_SET"

	if wasSet, _ := getenv(savedSetName); wasSet == "1" {
		saved, _ := getenv(savedName)
		fmt.Fprintf(b, "export %s=%s\n", name, quote(saved))
	} else {
		fmt.Fprintf(b, "unset %s\n", name)
	}
	fmt.Fprintf(b, "unset %s %s\n", savedName, savedSetName)
}

// writePath emits PATH as paths prepended to the shell's stable
// original PATH, restoring plain NEM_ORIGINAL_PATH when paths is empty
// so leaving a project removes its bin dirs exactly as save/restore
// does for regular vars. It first self-establishes NEM_ORIGINAL_PATH
// with the unset-only form (${NEM_ORIGINAL_PATH-$PATH}, no colon) so a
// genuinely empty saved original is never mistaken for unset; every
// later reference uses the colon form so an unset OR empty
// NEM_ORIGINAL_PATH still falls back to the live PATH. Because the
// self-establish line only sets NEM_ORIGINAL_PATH the first time it
// runs and never folds paths into it, repeated evals never grow PATH.
// Each path is single-quoted individually and the NEM_ORIGINAL_PATH
// expansion is double-quoted so it still expands; adjacent quoted
// segments concatenate into a single shell word.
func writePath(b *strings.Builder, paths []string) {
	b.WriteString(`export NEM_ORIGINAL_PATH="${NEM_ORIGINAL_PATH-$PATH}"` + "\n")

	if len(paths) == 0 {
		b.WriteString(`export PATH="${NEM_ORIGINAL_PATH:-$PATH}"` + "\n")
		return
	}

	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = quote(p)
	}
	fmt.Fprintf(b, "export PATH=%s:\"${NEM_ORIGINAL_PATH:-$PATH}\"\n", strings.Join(quoted, ":"))
}

// quote renders s as a single-quoted POSIX shell word. An embedded
// single quote is escaped by closing the quoted string, inserting a
// backslash-escaped quote, and reopening the quoted string, so the
// value can never be interpreted as shell syntax regardless of its
// content.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
