package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/report"
)

var (
	console *report.Console
	nemHome home.Home
	ranHook bool

	flagQuiet   bool
	flagVerbose bool
	flagColor   string
)

func newRoot() *cobra.Command {
	ranHook = false
	console = nil
	nemHome = home.Home{}
	root := &cobra.Command{
		Use:           "nem",
		Short:         "Manage your development environment",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			ranHook = true
			mode, err := resolveColorMode(flagColor, os.Getenv("NO_COLOR") != "")
			if err != nil {
				ranHook = false
				return err
			}
			console = report.New(cmd.OutOrStdout(), cmd.ErrOrStderr(), report.Options{
				Quiet:   flagQuiet,
				Verbose: flagVerbose,
				Color:   mode,
				IsTTY:   term.IsTerminal(int(os.Stderr.Fd())),
			})
			nemHome = home.Resolve(os.Getenv)
			return nil
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	pf := root.PersistentFlags()
	pf.BoolVarP(&flagQuiet, "quiet", "q", false, "suppress narration output")
	pf.BoolVar(&flagVerbose, "verbose", false, "show debug output")
	pf.StringVar(&flagColor, "color", "auto", "colorize output: auto, always, or never")

	root.AddCommand(newVersionCmd(), newStatusCmd())
	return root
}

// resolveColorMode maps the --color flag to a report.Mode, downgrading auto
// to never when NO_COLOR is set (an explicit --color always still wins).
func resolveColorMode(flagColor string, noColor bool) (report.Mode, error) {
	var mode report.Mode
	switch flagColor {
	case "auto":
		mode = report.ColorAuto
	case "always":
		mode = report.ColorAlways
	case "never":
		mode = report.ColorNever
	default:
		return 0, fmt.Errorf("invalid --color value %q (want auto, always, or never)", flagColor)
	}
	if mode == report.ColorAuto && noColor {
		mode = report.ColorNever
	}
	return mode, nil
}
