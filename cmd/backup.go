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
	"time"

	"github.com/blak0p/bunkerctl/internal/archive"
	"github.com/blak0p/bunkerctl/internal/cleaner"
	"github.com/blak0p/bunkerctl/internal/compress"
	"github.com/blak0p/bunkerctl/internal/config"
	"github.com/blak0p/bunkerctl/internal/manifest"
	"github.com/blak0p/bunkerctl/internal/metadata"
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

// backupDestPathFn returns the destination .bunker file path for a given
// container name. Overridable via the BUNKERCTL_BACKUP_DIR env var (used as the
// parent dir) for tests; defaults to ~/.bunkerctl/backups/bunker-<name>-<ts>.bunker.
var backupDestPathFn = defaultBackupDestPath

// backupFormat is the podman save --format value. Defaults to "docker-archive";
// tests override it. PR 5 will expose this as a --format CLI flag.
var backupFormat = "docker-archive"

// cliFormat holds the value of the --format CLI flag for the current backup
// invocation. When non-empty it overrides backupFormat. It is reset to "" in
// resetBackupFlags (called before every runBackup) so flags from a previous
// Execute() do not leak into the next test.
var cliFormat string

// cliOutput holds the value of the --output CLI flag for the current backup
// invocation. When non-empty it overrides backupDestPathFn (the path is used
// as-is, relative to the current working directory if not absolute). Reset to
// "" in resetBackupFlags.
var cliOutput string

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

// resolveFormat returns the effective podman save format for the current
// invocation: the --format CLI flag value (cliFormat) when it was set to
// something other than the default, otherwise the backupFormat seam (which
// tests override directly). Because cobra parses --format into cliFormat with
// a default of "docker-archive", cliFormat is always non-empty after parse;
// we prefer cliFormat so an explicit --format=docker-archive behaves the same
// as the default, and tests that call setBackupFormat still drive the seam
// when no --format flag was passed.
func resolveFormat() string {
	if cliFormat != "" {
		return cliFormat
	}
	return backupFormat
}

// resolveOutput returns the effective destination .bunker path override for
// the current invocation: the --output CLI flag value (cliOutput) when set,
// otherwise "" (meaning the backupDestPathFn seam / default path applies).
// A relative --output path is resolved against the current working directory.
func resolveOutput() string {
	if cliOutput == "" {
		return ""
	}
	if filepath.IsAbs(cliOutput) {
		return cliOutput
	}
	cwd, err := os.Getwd()
	if err != nil {
		return cliOutput
	}
	return filepath.Join(cwd, cliOutput)
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
// editable manifest step. PR 4 adds cache cleaning and archive production.
// PR 5 adds the --format and --output CLI flags, help text, and examples.
var backupCmd = &cobra.Command{
	Use:   "backup [name]",
	Short: "Backup a Podman bunker",
	Long: `Backup a Podman distrobox bunker to a portable .bunker archive.

The bunker includes the container's image, the explicitly installed packages
(from the detected package manager), and the preserve-list from the config file.
Cache directories are cleaned before packaging.`,
	Example: `  bunkerctl backup                          # interactive chooser
  bunkerctl backup mybunker                  # explicit name
  bunkerctl backup mybunker --format=oci-archive
  bunkerctl backup mybunker --output=/tmp/bunker.bunker`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBackup(cmd, args)
	},
}

func init() {
	rootCmd.AddCommand(backupCmd)
	backupCmd.Flags().StringVar(&cliFormat, "format", "docker-archive",
		"podman save format: docker-archive (default) or oci-archive")
	backupCmd.Flags().StringVar(&cliOutput, "output", "",
		"write the .bunker archive to this path (default: ~/.bunkerctl/backups/)")
}

// runBackup implements the engine-check + selection + staging + detection +
// manifest phases. Archive production (commit/save/compress) arrives in a
// later PR; for now the pipeline ends after the manifest is confirmed.
func runBackup(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	runner := resolveRunner()

	// 0. Resolve and validate CLI flags (--format, --output) before any
	// pipeline work so an invalid value fails fast with a clear message.
	format := resolveFormat()
	if !podman.AllowedSaveFormat(format) {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: invalid format %q (allowed: docker-archive, oci-archive)\n", format)
		return fmt.Errorf("invalid format %q", format)
	}
	outputPath := resolveOutput()

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
	// Deduplicate overlapping managers: on modern Fedora, both `dnf` and
	// `dnf5` are present, and `dnf` is typically a wrapper around dnf5 —
	// listing both reads the same DB twice and duplicates the manifest. Prefer
	// the modern manager (dnf5) when both are detected.
	managers = dedupeManagers(managers)
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
		// Defense in depth: if a manager's lister ever returns duplicates
		// (e.g. a quirky upstream command), the manifest must not contain
		// duplicate keys. The TOML writer would error out, the re-parse
		// would fail, and the user would lose the backup.
		allPkgs = dedupeStrings(allPkgs)
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
	fmt.Fprintf(cmd.OutOrStdout(), "backup of %s prepared in %s\n", name, stagingDir)

	// 8. Cache cleaning — only known cache paths for detected managers, only
	// AFTER the manifest is confirmed.
	if len(managers) > 0 {
		clnr := cleaner.DefaultCleaner{}
		if err := clnr.Clean(ctx, runner, name, managers); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "error: cleaning caches: %v\n", err)
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "cleaned %d cache dirs\n", len(managers))
	}

	// 9. Archive production — commit container, save image, write metadata,
	// compress the staging tree into the dest .bunker file.
	inspectResult, err := runner.Inspect(ctx, name)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: inspecting container before archive: %v\n", err)
		return err
	}
	backupDate := time.Now()
	destPath := outputPath
	if destPath == "" {
		destPath = backupDestPathFn(name)
	}
	producer := archive.DefaultProducer{
		Compressor: compress.ZstdTar{},
		MetaWriter: metadata.JSONWriter{},
	}
	if _, err := producer.Produce(ctx, runner, inspectResult, stagingDir, destPath, archive.ProduceOptions{
		Format:        format,
		BackupDate:    backupDate,
		Version:       Version,
		Managers:      managerNames(managers),
		PreserveCount: stagedCount,
	}); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: producing archive: %v\n", err)
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "backup created: %s\n", destPath)
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

// defaultBackupDestPath returns the default destination .bunker path for a
// container: $BUNKERCTL_BACKUP_DIR (or ~/.bunkerctl/backups) +
// bunker-<name>-<timestamp>.bunker. The parent dir is created if missing.
func defaultBackupDestPath(name string) string {
	dir := os.Getenv("BUNKERCTL_BACKUP_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			dir = filepath.Join(home, ".bunkerctl", "backups")
		} else {
			dir = os.TempDir()
		}
	}
	_ = os.MkdirAll(dir, 0o755)
	ts := time.Now().Format("20060102-150405")
	return filepath.Join(dir, "bunker-"+name+"-"+ts+".bunker")
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

// dedupeManagers removes overlapping managers from a detector result, keeping
// the canonical (most modern) member of each family. Today: when BOTH `dnf`
// and `dnf5` are detected, prefer dnf5 and drop dnf — on Fedora 40+ dnf is a
// thin wrapper around dnf5 and they read the same package database, so listing
// both duplicates every package in the manifest. Other managers pass through
// untouched. Order is preserved.
func dedupeManagers(in []packages.Manager) []packages.Manager {
	hasDnf5 := false
	for _, m := range in {
		if m == packages.ManagerDnf5 {
			hasDnf5 = true
			break
		}
	}
	out := make([]packages.Manager, 0, len(in))
	for _, m := range in {
		if m == packages.ManagerDnf && hasDnf5 {
			// dnf is a wrapper for dnf5 on modern Fedora; drop it.
			continue
		}
		out = append(out, m)
	}
	return out
}

// dedupeStrings returns a new slice with the unique strings from in, preserving
// first-occurrence order. Used to defend the manifest writer against duplicate
// keys (TOML forbids them) when an upstream lister command returns duplicate
// lines for any reason.
func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// managerNames converts a slice of packages.Manager to a slice of plain string
// names ("dnf5", "apt", ...) suitable for embedding in metadata.json. The
// returned slice preserves the order of the input.
func managerNames(in []packages.Manager) []string {
	out := make([]string, 0, len(in))
	for _, m := range in {
		out = append(out, string(m))
	}
	return out
}
