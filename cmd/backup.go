// Package cmd contains the cobra command tree for bunkerctl.
package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/blak0p/bunkerctl/internal/config"
	"github.com/blak0p/bunkerctl/internal/manifest"
	"github.com/blak0p/bunkerctl/internal/packages"
	"github.com/blak0p/bunkerctl/internal/podman"
	"github.com/blak0p/bunkerctl/internal/preserve"
	"github.com/blak0p/bunkerctl/internal/staging"
	"github.com/spf13/cobra"
)

// backupRunner is the Podman Runner used by the backup command. It is a
// package-level seam so tests can inject a fake (setBackupRunner) without
// spawning a real podman process. It defaults to a real CLIRunner, lazily
// initialized on first use.
var backupRunner podman.Runner

// backupConfigPathFn returns the config file path to load. Overridable via env
// BUNKERCTL_CONFIG for tests; defaults to ~/.config/bunkerctl/config.toml.
var backupConfigPathFn = defaultConfigPath

// backupPreserveFS is the fs.FS used by the preserve expander. When nil, the
// real os.DirFS("/") is used. Tests set it to a fstest.MapFS.
var backupPreserveFS fs.FS

// backupStagingRoot is the parent directory for staging dirs. When empty, the
// system temp dir is used. Tests set it to a t.TempDir().
var backupStagingRoot string

// backupEditorFallback is the fallback editor when $EDITOR is unset. Defaults
// to "vi" in production. Tests set it to "" to assert the no-editor error.
var backupEditorFallback = "vi"

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

// defaultConfigPath returns the default config file path:
// $BUNKERCTL_CONFIG or ~/.config/bunkerctl/config.toml.
func defaultConfigPath() string {
	if v := os.Getenv("BUNKERCTL_CONFIG"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "bunkerctl", "config.toml")
}

// backupCmd is the `bunkerctl backup [name]` command.
//
// PR 2 wiring: engine availability check, explicit-name selection, and an
// interactive chooser when no name is given. PR 3 adds preserve-list staging
// (config load + glob expansion + copy), package manager detection, and the
// editable manifest step. Archive production arrives in a later PR.
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

// runBackup implements the engine-check + selection + staging + detection +
// manifest phases. Archive production (commit/save/compress) arrives in a
// later PR; for now the pipeline ends after the manifest is confirmed.
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

	// 3. Load config (missing file is OK — empty preserve-list).
	cfgPath := backupConfigPathFn()
	loader := config.FileLoader{Path: cfgPath}
	cfg, cfgErr := loader.Load()
	if cfgErr != nil && !errors.Is(cfgErr, config.ErrConfigNotFound) {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: loading config: %v\n", cfgErr)
		return cfgErr
	}

	// 4. Prepare staging.
	stager := &staging.LocalStager{Root: stagingRoot(), ContainerID: name}
	stagingDir, cleanup, err := stager.Prepare()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: preparing staging: %v\n", err)
		return err
	}
	defer cleanup()

	// 5. Expand preserve entries and copy to staging/preserve.
	entries := buildPreserveEntries(cfg)
	expander := preserve.Expander{FS: resolvePreserveFS()}
	if err := stager.Copy(ctx, entries, expander); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: staging preserve-list: %v\n", err)
		return err
	}
	stagedCount := countStaged(stagingDir)
	fmt.Fprintf(cmd.OutOrStdout(), "staged %d files\n", stagedCount)

	// 6. Detect package managers inside the container.
	detector := packages.DefaultDetector{}
	managers, err := detector.Detect(ctx, runner, name)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: detecting package managers: %v\n", err)
		return err
	}
	var allPkgs []string
	if len(managers) > 0 {
		lister := packages.DefaultLister{}
		for _, m := range managers {
			pkgs, listErr := lister.List(ctx, runner, name, m)
			if listErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: listing packages for %s: %v\n", m, listErr)
				continue
			}
			allPkgs = append(allPkgs, pkgs...)
		}
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "no package manager detected; continuing without a package list")
	}

	// 7. Write manifest, open in editor, re-parse after edit.
	manifestPath := filepath.Join(stagingDir, "manifest.toml")
	writer := manifest.TOMLWriter{}
	if err := writer.Write(manifestPath, allPkgs); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: writing manifest: %v\n", err)
		return err
	}
	editor := manifest.ShellEditor{Fallback: backupEditorFallback}
	if _, err := editor.Edit(manifestPath); err != nil {
		if errors.Is(err, manifest.ErrNoEditor) {
			fmt.Fprintln(cmd.ErrOrStderr(), "error: no editor configured; set $EDITOR or use the default vi")
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "error: editing manifest: %v\n", err)
		return err
	}
	// Re-parse the manifest after the user edits it (preserves curation).
	finalPkgs, err := manifest.Read(manifestPath)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: re-reading manifest: %v\n", err)
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "manifest confirmed: %d packages\n", len(finalPkgs))

	// Placeholder for archive production (Slice 5+).
	fmt.Fprintf(cmd.OutOrStdout(), "backup of %s prepared in %s\n", name, stagingDir)
	return nil
}

// stagingRoot returns the staging parent dir: the test seam, the BUNKERCTL_STAGING_ROOT
// env var, or the system temp dir.
func stagingRoot() string {
	if backupStagingRoot != "" {
		return backupStagingRoot
	}
	if v := os.Getenv("BUNKERCTL_STAGING_ROOT"); v != "" {
		return v
	}
	return os.TempDir()
}

// resolvePreserveFS returns the test-injected FS, or the real os.DirFS("/") for
// production use.
func resolvePreserveFS() fs.FS {
	if backupPreserveFS != nil {
		return backupPreserveFS
	}
	return os.DirFS("/")
}

// buildPreserveEntries parses each config Preserve line into a preserve.Entry,
// skipping blanks and comments.
func buildPreserveEntries(cfg config.Config) []preserve.Entry {
	var entries []preserve.Entry
	for _, line := range cfg.Preserve {
		e, err := preserve.ParseLine(line)
		if err != nil {
			continue
		}
		if e.Raw == "" {
			continue
		}
		entries = append(entries, e)
	}
	return entries
}

// countStaged counts the files under <stagingDir>/preserve/.
func countStaged(stagingDir string) int {
	preserveDir := filepath.Join(stagingDir, "preserve")
	var count int
	_ = filepath.WalkDir(preserveDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		count++
		return nil
	})
	return count
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