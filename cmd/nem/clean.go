package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/vi-dev/nem-cli/internal/clean"
	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/usage"
)

func newCleanCmd() *cobra.Command {
	var (
		age    clean.Age
		all    bool
		dryRun bool
		yes    bool
		grace  time.Duration
	)
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Reclaim disk space in NEM_HOME",
		Long: "Remove leaked build staging, leaked downloads, and partial " +
			"installs. Bare nem clean touches only this provable garbage and " +
			"never prompts, so it is safe to run unattended.\n\n" +
			"With --unused or --all, also remove installed package versions; " +
			"nem sync restores a project's tools. --unused measures the last " +
			"time nem itself resolved a version — nem env on a directory " +
			"change, nem exec, nem install, or nem catalog build — not the " +
			"last time a shell actually used it, so a shell that has not " +
			"changed directories in a while can still have a version on its " +
			"PATH that --unused would evict.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A stamp can understate a version's last real use by up to
			// usage.Debounce, so the window is padded by that amount to
			// keep "unused for W" a guarantee of at least W of real
			// disuse. Zero means the flag was not set and must stay
			// zero: it is what tells the planner to leave every version
			// alone.
			unused := time.Duration(0)
			if d := age.Duration(); d > 0 {
				unused = d + usage.Debounce
			}
			return runClean(cmd, clean.Options{
				Grace:  grace,
				Unused: unused,
				All:    all,
			}, dryRun, yes)
		},
	}
	cmd.Flags().Var(&age, "unused", "evict versions nem hasn't resolved in this long, like 30d or 12h")
	cmd.Flags().BoolVar(&all, "all", false, "remove every installed package version")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan without deleting anything")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	cmd.Flags().DurationVar(&grace, "grace", time.Hour,
		"leave recently-touched reclaimable paths alone")
	cmd.MarkFlagsMutuallyExclusive("unused", "all")
	return cmd
}

func runClean(cmd *cobra.Command, opts clean.Options, dryRun, yes bool) error {
	// Store.Versions is consulted only under --all/--unused: bare clean
	// would otherwise pay to stat every installed version just to
	// discard the result. usageIndex is loaded under the same condition
	// — Build's --all branch never reads it.
	needVersions := opts.All || opts.Unused > 0
	store, err := clean.Scan(nemHome, needVersions)
	if err != nil {
		return fmt.Errorf("scan %s: %w", nemHome.Root(), err)
	}
	idx := usage.Index{}
	if opts.Unused > 0 {
		idx = usageIndex()
	}
	plan := clean.Build(store, idx, opts, time.Now())
	if len(plan.Entries) == 0 {
		console.Success("Nothing to reclaim")
		return nil
	}

	rows := make([][]string, 0, len(plan.Entries))
	for _, e := range plan.Entries {
		rows = append(rows, []string{e.Path, e.Reason, report.FormatBytes(e.Size)})
	}
	console.Table([]string{"PATH", "REASON", "SIZE"}, rows)
	console.Info("Total: %s", report.FormatBytes(plan.Total()))

	if dryRun {
		return nil
	}
	if plan.Confirm && !yes {
		console.Blank()
		if !confirm(cmd, opts.All) {
			console.Info("Nothing removed")
			return nil
		}
	}

	task := console.Task("Reclaiming")
	var freedSoFar int64
	obs := clean.Observer{
		Removing: func(e clean.Entry) {
			task.Status(entryLabel(e))
			freedSoFar += e.Size
			task.Progress(freedSoFar, -1)
		},
		Skipped: func(e clean.Entry, reason string) {
			console.Warn("skipped %s: %s", entryLabel(e), reason)
		},
	}
	freed, err := clean.Execute(nemHome, plan, obs)
	if err != nil {
		task.Fail("Reclaim failed")
		if freed > 0 {
			console.Info("Reclaimed %s before the error below", report.FormatBytes(freed))
		}
		return err
	}
	task.Done("Reclaimed " + report.FormatBytes(freed))
	return nil
}

// entryLabel names e the way progress and skip lines show it: name/version
// for an installed package version, since that is what Key encodes, or the
// path's own base name for a tier-0 garbage entry, which carries no key.
func entryLabel(e clean.Entry) string {
	if e.Key == "" {
		return filepath.Base(e.Path)
	}
	return strings.Replace(e.Key, "@", "/", 1)
}

// confirm asks before the plan removes installed package versions. --all
// gets its own, stronger wording because it empties the store rather than
// trimming it. The question goes out through Prompt so --quiet cannot hide
// it while the read below still waits for an answer.
//
// On a real terminal, the answer is a single keystroke read in raw mode
// (see confirmRaw) rather than a line, so the command never waits on
// Enter. Any other input source — a pipe, or a test's SetIn — keeps the
// line-based read, where any input other than y/yes, including no answer
// at all, counts as a refusal.
func confirm(cmd *cobra.Command, all bool) bool {
	question := "Remove these package versions? [y/N]: "
	if all {
		question = "Remove EVERY installed package version? [y/N]: "
	}
	console.Prompt("%s", question)

	if cmd.InOrStdin() == os.Stdin {
		if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
			return confirmRaw(fd)
		}
	}

	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// confirmRaw reads exactly one keystroke from the terminal at fd without
// waiting for Enter. Raw mode disables local echo, so the key is written
// back out through Prompt once the terminal is restored, matching what a
// line-based answer would have left in the transcript. If that restore
// fails, the echo is skipped rather than printed while the terminal is
// still in raw mode, which would otherwise mangle line discipline until
// the deferred restore below eventually runs; the confirm/refuse decision
// itself does not depend on the echo and is unaffected either way. Any
// key but a bare y/Y — a read failure included — refuses, fail-closed.
func confirmRaw(fd int) bool {
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return false
	}
	defer func() { _ = term.Restore(fd, oldState) }()

	var buf [1]byte
	n, readErr := os.Stdin.Read(buf[:])
	restoreErr := term.Restore(fd, oldState)
	if readErr != nil || n == 0 {
		return false
	}

	key := buf[0]
	if restoreErr == nil {
		console.Prompt("%c", key)
	}
	return key == 'y' || key == 'Y'
}

// usageIndex is a package var so tests can supply a fixed index.
var usageIndex = func() usage.Index { return usage.Load(nemHome) }
