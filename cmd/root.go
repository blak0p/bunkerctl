// Package cmd contains the cobra command tree for bunkerctl.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version is the bunkerctl build version, bound to the root command's
// --version flag. It is overridable at link time via -ldflags. The value
// "0.1.0" marks the first user-visible release of bunkerctl with the
// backup-core feature (SDD 1). Future restore (SDD 2) / upgrade (SDD 3)
// releases will bump this.
var Version = "0.1.0"

// rootCmd is the bunkerctl root command.
var rootCmd = &cobra.Command{
	Use:     "bunkerctl",
	Short:   "Backup and restore Podman distrobox bunkers",
	Version: Version,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

// Execute runs the root command and exits with a non-zero code on error.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
