package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// version and buildTime are set at build time via
// -ldflags "-X main.version=v1.2.3 -X main.buildTime=2026-08-06T08:15:00Z".
var (
	version   = "dev"
	buildTime = "unknown"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of nem",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), versionInfo())
			return err
		},
	}
}

func versionInfo() string {
	commit := "unknown"
	modified := false
	goVersion := runtime.Version()

	if info, ok := debug.ReadBuildInfo(); ok {
		goVersion = info.GoVersion
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				commit = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
	}
	if modified {
		commit += " (modified)"
	}

	return strings.Join([]string{
		fmt.Sprintf("nem %s", version),
		fmt.Sprintf("Built: %s", buildTime),
		fmt.Sprintf("Commit: %s", commit),
		fmt.Sprintf("Go: %s", goVersion),
		fmt.Sprintf("Platform: %s/%s", runtime.GOOS, runtime.GOARCH),
	}, "\n") + "\n"
}
