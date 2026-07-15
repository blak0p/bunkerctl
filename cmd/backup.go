package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// backupCmd is the `bunkerctl backup` command. PR 1 ships only the skeleton;
// the engine check and selection logic arrive in PR 2.
var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Backup a Podman bunker",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "backup: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(backupCmd)
}