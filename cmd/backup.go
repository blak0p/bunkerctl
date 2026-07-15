// Package cmd contains the cobra command tree for bunkerctl.
package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/blak0p/bunkerctl/internal/podman"
	"github.com/spf13/cobra"
)

// backupRunner is the Podman Runner used by the backup command. It is a
// package-level seam so tests can inject a fake (setBackupRunner) without
// spawning a real podman process. It defaults to a real CLIRunner, lazily
// initialized on first use.
var backupRunner podman.Runner

// defaultRunner returns the production Runner, lazily created.
func defaultRunner() podman.Runner {
	return podman.NewCLIRunner("")
}

// resolveRunner returns the injected runner if set, otherwise the default.
func resolveRunner() podman.Runner {
	if backupRunner != nil {
		return backupRunner
	}
	return defaultRunner()
}

// backupCmd is the `bunkerctl backup [name]` command.
//
// PR 2 wiring: engine availability check, explicit-name selection, and an
// interactive chooser when no name is given. The full backup pipeline
// (preserve, package detection, archive) arrives in later PRs.
var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Backup a Podman bunker",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBackup(cmd, args)
	},
}

func init() {
	rootCmd.AddCommand(backupCmd)
}

// runBackup implements the engine-check + selection phase. It returns a
// non-nil error (which cobra prints to stderr and exits non-zero) on failure,
// or nil on success. The backup pipeline itself arrives in a later PR; for
// now success prints a "selected: <name>" placeholder.
func runBackup(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	runner := resolveRunner()

	// 1. Engine availability check — fail fast before any selection work.
	if _, err := runner.Version(ctx); err != nil {
		if errors.Is(err, podman.ErrEngineUnavailable) {
			fmt.Fprintln(cmd.ErrOrStderr(), "error: podman engine is required but could not be reached")
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "error: %v\n", err)
		}
		return err
	}

	var name string
	if len(args) == 1 {
		// 2a. Explicit identifier path. Validate first (threat matrix), then
		// verify the container exists via Inspect.
		name = args[0]
		if err := podman.ValidateContainerName(name); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "error: invalid container name")
			return err
		}
		if _, err := runner.Inspect(ctx, name); err != nil {
			if errors.Is(err, podman.ErrContainerNotFound) {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: container %q not found\n", name)
				return err
			}
			if errors.Is(err, podman.ErrInvalidContainerName) {
				fmt.Fprintln(cmd.ErrOrStderr(), "error: invalid container name")
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "error: %v\n", err)
			return err
		}
	} else {
		// 2b. Interactive chooser path.
		containers, err := runner.List(ctx, true)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "error: listing containers: %v\n", err)
			return err
		}
		if len(containers) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "error: no containers found")
			return errors.New("no containers found")
		}
		name, err = chooseContainer(cmd, containers)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "error: %v\n", err)
			return err
		}
	}

	// Placeholder for the real backup pipeline (Slice 3+).
	fmt.Fprintf(cmd.OutOrStdout(), "selected: %s\n", name)
	return nil
}

// chooseContainer renders the list to stdout and reads a 1-based index from
// stdin. It returns the selected container's name (or ID if unnamed).
func chooseContainer(cmd *cobra.Command, containers []podman.Container) (string, error) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Available containers:")
	for i, c := range containers {
		fmt.Fprintf(out, "  %d: %s (%s) [%s]\n", i+1, displayName(c), c.Image, c.Status)
	}
	fmt.Fprintf(out, "Select a container [1-%d]: ", len(containers))

	scanner := bufio.NewScanner(cmd.InOrStdin())
	if !scanner.Scan() {
		return "", errors.New("no selection read from stdin")
	}
	raw := strings.TrimSpace(scanner.Text())
	idx, err := strconv.Atoi(raw)
	if err != nil || idx < 1 || idx > len(containers) {
		return "", fmt.Errorf("invalid selection %q", raw)
	}
	return displayName(containers[idx-1]), nil
}

// displayName returns the first name for a container, or its ID if it has no
// names.
func displayName(c podman.Container) string {
	if len(c.Names) > 0 && c.Names[0] != "" {
		return c.Names[0]
	}
	return c.ID
}