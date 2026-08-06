package main

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "nem",
	Short:         "Manage your development environment",
	Version:       version,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.SetVersionTemplate("{{.Version}}\n")
}
