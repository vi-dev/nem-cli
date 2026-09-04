package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// version, buildTime, and channel are set at build time via -ldflags
// "-X main.version=v1.2.3 -X main.buildTime=2026-08-06T08:15:00Z
// -X main.channel=stable". channel stays empty on builds outside the
// release pipelines (e.g. make install).
var (
	version   = "dev"
	buildTime = "unknown"
	channel   = ""
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of nem",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			console.Data("%s", versionInfo())
			return nil
		},
	}
}

func versionInfo() string {
	goVersion := runtime.Version()
	if info, ok := debug.ReadBuildInfo(); ok {
		goVersion = info.GoVersion
	}
	commit, modified := vcsRevision()
	if commit == "" {
		commit = "unknown"
	}
	if modified {
		commit += " (modified)"
	}

	lines := []string{fmt.Sprintf("nem %s", version)}
	if channel != "" {
		lines = append(lines, fmt.Sprintf("Channel: %s", channel))
	}
	lines = append(lines,
		fmt.Sprintf("Built: %s", buildTime),
		fmt.Sprintf("Commit: %s", commit),
		fmt.Sprintf("Go: %s", goVersion),
		fmt.Sprintf("Platform: %s/%s", runtime.GOOS, runtime.GOARCH),
	)
	return strings.Join(lines, "\n") + "\n"
}

// vcsRevision reports the commit the binary was built from and whether the
// working tree was modified, per the embedded build info. rev is empty when
// the build carries no VCS stamp.
func vcsRevision() (rev string, modified bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	return rev, modified
}
