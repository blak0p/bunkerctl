// Package cmd contains the cobra command tree for bunkerctl.
package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/blak0p/bunkerctl/internal/cleaner"
	"github.com/blak0p/bunkerctl/internal/compress"
	"github.com/blak0p/bunkerctl/internal/copy"
	"github.com/blak0p/bunkerctl/internal/curate"
	"github.com/blak0p/bunkerctl/internal/ignore"
	"github.com/blak0p/bunkerctl/internal/inspect"
	"github.com/blak0p/bunkerctl/internal/manifest"
	"github.com/blak0p/bunkerctl/internal/packages"
	"github.com/blak0p/bunkerctl/internal/pkgdetect"
	"github.com/blak0p/bunkerctl/internal/podman"
	"github.com/blak0p/bunkerctl/internal/staging"
	"github.com/spf13/cobra"
)

// --- Pipeline collaborators (overridable seams for tests) ---
//
// Each defaults to the production implementation; tests inject fakes via the
// setBackup* helpers in backup_seams_test.go. They are package-level so a single
// resetBackupFlags() call can re-establish a clean slate between Execute() runs.

// backupRunner is the Podman Runner used by the backup command. Defaults to a
// real CLIRunner, lazily initialized on first use.
var backupRunner podman.Runner

// backupStagingRoot is the parent directory for staging dirs. When empty, the
// system temp dir is used. Tests set it to a t.TempDir().
var backupStagingRoot string

// backupEditorFallback is the fallback editor when $EDITOR is unset. Defaults
// to "vi" in production. Tests set it to "" to assert the no-editor error.
var backupEditorFallback = "vi"

// backupDestPathFn returns the destination .bunker file path for a given
// container name. Defaults to ./<name>-<timestamp>.bunker in the cwd.
var backupDestPathFn = defaultBackupDestPath

// backupCopier copies files from inside the container. When nil, the pipeline
// uses copy.DefaultCopier{}.
var backupCopier copy.Copier

// backupEditor opens bunker.yaml for curation. When nil, the pipeline uses
// curate.ShellEditor{Fallback: backupEditorFallback}.
var backupEditor curate.Editor

// backupCleaner removes known cache dirs inside the container. When nil, the
// pipeline uses cleaner.DefaultCleaner{}.
var backupCleaner cleaner.Cleaner

// backupCompressor compresses the staging dir into the .bunker file. When nil,
// the pipeline uses compress.ZstdTar{}.
var backupCompressor compress.Compressor

// cliOutput holds the --output CLI flag value for the current invocation. Reset
// to "" in resetBackupFlags.
var cliOutput string

// cliNoEdit holds the --no-edit CLI flag value.
var cliNoEdit bool

// cliIgnoreExtra holds the --ignore-extra CLI flag value (comma-separated).
var cliIgnoreExtra string

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

// resolveCopier returns the injected copier or the default.
func resolveCopier() copy.Copier {
	if backupCopier != nil {
		return backupCopier
	}
	return copy.DefaultCopier{}
}

// resolveEditor returns the injected editor or the ShellEditor fallback.
func resolveEditor() curate.Editor {
	if backupEditor != nil {
		return backupEditor
	}
	return curate.ShellEditor{Fallback: backupEditorFallback}
}

// resolveCleaner returns the injected cleaner or the default.
func resolveCleaner() cleaner.Cleaner {
	if backupCleaner != nil {
		return backupCleaner
	}
	return cleaner.DefaultCleaner{}
}

// resolveCompressor returns the injected compressor or the default.
func resolveCompressor() compress.Compressor {
	if backupCompressor != nil {
		return backupCompressor
	}
	return compress.ZstdTar{}
}

// resolveOutput returns the effective destination .bunker path for the current
// invocation: the --output CLI flag value when set, otherwise "" (meaning
// backupDestPathFn applies). A relative --output is resolved against the cwd.
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

// backupCmd is the `bunkerctl backup [name]` command. It runs the 14-step v1
// pipeline: engine check → container selection → staging → inspect → user
// detection → distro detection → package detection → YAML generation → user
// curation → container-side file copy → cache cleaning → compression → output.
var backupCmd = &cobra.Command{
	Use:   "backup [name]",
	Short: "Backup a Podman bunker",
	Long: `Backup a Podman distrobox bunker to a portable .bunker archive.

The .bunker file is a zstd-compressed tar containing bunker.yaml (the manifest
with detected user, base distro, packages with versions, and file-copy config)
and a files/ tree copied from inside the container. The generated bunker.yaml
is opened in $EDITOR for curation before the archive is finalized.`,
	Example: `  bunkerctl backup                          # interactive chooser
  bunkerctl backup mybunker                  # explicit name
  bunkerctl backup mybunker --no-edit        # skip YAML curation (CI)
  bunkerctl backup mybunker --output=/tmp/b.bunker
  bunkerctl backup mybunker --ignore-extra=build,dist`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBackup(cmd, args)
	},
}

func init() {
	rootCmd.AddCommand(backupCmd)
	backupCmd.Flags().StringVarP(&cliOutput, "output", "o", "",
		"write the .bunker archive to this path (default: ./<name>-<timestamp>.bunker)")
	backupCmd.Flags().BoolVar(&cliNoEdit, "no-edit", false,
		"skip the bunker.yaml editor curation step")
	backupCmd.Flags().StringVar(&cliIgnoreExtra, "ignore-extra", "",
		"comma-separated extra ignore patterns added to the defaults for this run")
}

// resetBackupFlags clears the CLI flag bindings back to their defaults so
// successive Execute() runs in the same test process do not inherit the
// previous run's flag values. Called by executeBackup before each invocation.
func resetBackupFlags() {
	cliOutput = ""
	cliNoEdit = false
	cliIgnoreExtra = ""
}

// runBackup implements the v1 backup pipeline. Each step can fail with a clear
// error; the staging dir is cleaned up via defer after step 3.
func runBackup(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	runner := resolveRunner()

	// 1. Engine availability check — fail fast before any state is created.
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
		// 2a. Explicit identifier path.
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

	// 3. Staging dir + cleanup.
	fmt.Fprintf(cmd.OutOrStdout(), "backing up %s\n", name)
	stager := &staging.LocalStager{Root: stagingRoot(), ContainerID: name}
	stagingDir, cleanup, err := stager.Prepare()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: preparing staging: %v\n", err)
		return err
	}
	defer cleanup()

	// 4. Inspect container metadata (REQ-DETECT-1).
	inspectData, err := inspect.Fetch(ctx, runner, name)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: inspecting container: %v\n", err)
		return err
	}

	// 5. Detect user + multi-user guard (REQ-DETECT-2, REQ-DETECT-3, REQ-ERR-3).
	userInfo, err := inspect.ResolveUser(ctx, runner, name, inspectData.User)
	if err != nil {
		if errors.Is(err, inspect.ErrMultiUserAmbiguous) {
			fmt.Fprintf(cmd.ErrOrStderr(), "error: ambiguous user: %v\n", err)
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "error: detecting user: %v\n", err)
		return err
	}

	// 6. Detect base distro (REQ-YAML-3). Non-Fedora is rejected in this SDD.
	baseInfo, err := inspect.DetectBase(ctx, runner, name)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: %v\n", err)
		return err
	}

	// 7. Detect packages (REQ-DETECT-4/5/6).
	mgr, detector, err := pkgdetect.DetectManager(ctx, runner, name)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: %v\n", err)
		return err
	}
	pkgs, err := detector.Detect(ctx, runner, name)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: detecting packages: %v\n", err)
		return err
	}

	// 8. Generate bunker.yaml (REQ-YAML-1..7).
	ignorePatterns := ignore.MergePatterns(ignore.DefaultPatterns(), parseIgnoreExtra(cliIgnoreExtra))
	m := &manifest.BunkerManifest{
		FormatVersion: 1,
		Name:          name,
		Created:       time.Now().Format("2006-01-02"),
		User: manifest.UserInfo{
			Name: userInfo.Name,
			UID:  userInfo.UID,
			GID:  userInfo.GID,
			Home: userInfo.Home,
		},
		Base: manifest.BaseInfo{
			Distro:  baseInfo.Distro,
			Version: baseInfo.Version,
		},
		Packages: map[string][]manifest.Package{
			string(mgr): toManifestPackages(pkgs),
		},
		Files: manifest.FilesConfig{
			Copy:    "auto",
			CopyEtc: []string{},
			Ignore:  ignorePatterns,
		},
		Custom: manifest.CustomConfig{
			Environment: selectEnv(inspectData.Env),
		},
		Verify: manifest.VerifyConfig{Auto: true},
	}
	yamlBytes, err := manifest.Marshal(m)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: generating bunker.yaml: %v\n", err)
		return err
	}
	yamlPath := stager.BunkerYAMLPath()
	if err := os.WriteFile(yamlPath, yamlBytes, 0o644); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: writing bunker.yaml: %v\n", err)
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "generated bunker.yaml: %d packages\n", len(pkgs))

	// 9. User curation (REQ-EDIT-1/2/3).
	if !cliNoEdit {
		editor := resolveEditor()
		exitCode, eerr := editor.Edit(yamlPath)
		if eerr != nil {
			if errors.Is(eerr, curate.ErrNoEditor) {
				fmt.Fprintln(cmd.ErrOrStderr(), "error: no editor configured; set $EDITOR or use --no-edit")
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: editing bunker.yaml: %v\n", eerr)
			}
			return eerr
		}
		if exitCode != 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "error: editor exited with code %d; aborting backup\n", exitCode)
			return fmt.Errorf("editor exited with code %d", exitCode)
		}
		// Re-read + validate the edited YAML (preserves curation).
		edited, rerr := os.ReadFile(yamlPath)
		if rerr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "error: re-reading bunker.yaml: %v\n", rerr)
			return rerr
		}
		if _, verr := manifest.Unmarshal(edited); verr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "error: validating edited bunker.yaml: %v\n", verr)
			return verr
		}
	}

	// 10. Container-side file copy (REQ-COPY-1/2/3).
	copyOpts := copy.CopyOptions{
		Home:       userInfo.Home,
		CopyEtc:    m.Files.CopyEtc,
		Ignore:     ignorePatterns,
		StagingDir: stager.FilesDir(),
	}
	res, err := resolveCopier().Copy(ctx, runner, name, copyOpts)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: copying files from container: %v\n", err)
		return err
	}
	if res.Warning != "" {
		fmt.Fprintln(cmd.OutOrStdout(), res.Warning)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "copied %d bytes from container\n", res.BytesCopied)

	// 11. Cache cleaning (kept from v0.1.0).
	managers := dedupeManagers([]packages.Manager{mgr})
	if err := resolveCleaner().Clean(ctx, runner, name, managers); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: cleaning caches: %v\n", err)
		return err
	}
	if len(managers) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "cleaned %d cache dirs\n", len(managers))
	}

	// 12. Compression (REQ-COMP-1/2/3).
	destPath := resolveOutput()
	if destPath == "" {
		destPath = backupDestPathFn(name)
	}
	if err := resolveCompressor().Compress(stagingDir, destPath); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: compressing archive: %v\n", err)
		return err
	}

	// 13. Output.
	fmt.Fprintf(cmd.OutOrStdout(), "backup created: %s\n", destPath)
	return nil
}

// stagingRoot returns the staging parent dir: the test seam, the
// BUNKERCTL_STAGING_ROOT env var, or the system temp dir.
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
// container: ./<name>-<timestamp>.bunker in the current working directory.
func defaultBackupDestPath(name string) string {
	ts := time.Now().Format("20060102-150405")
	return filepath.Join(".", name+"-"+ts+".bunker")
}

// parseIgnoreExtra splits a comma-separated --ignore-extra value into patterns,
// trimming whitespace and dropping empties.
func parseIgnoreExtra(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// toManifestPackages converts pkgdetect.Package entries to manifest.Package.
func toManifestPackages(pkgs []pkgdetect.Package) []manifest.Package {
	out := make([]manifest.Package, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, manifest.Package{Name: p.Name, Version: p.Version})
	}
	return out
}

// selectEnv picks the environment variables worth recording in
// custom.environment: a small, stable subset (EDITOR, TERM, SHELL). Anything
// else from the raw inspect Env is ignored to keep the YAML readable. The map
// is always non-nil so the YAML emits an empty map when nothing matches.
func selectEnv(env []string) map[string]string {
	out := map[string]string{}
	wanted := map[string]bool{"EDITOR": true, "TERM": true, "SHELL": true}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if wanted[k] {
			out[k] = v
		}
	}
	return out
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

// displayName returns the first name for a container, or its ID if unnamed.
func displayName(c podman.Container) string {
	if len(c.Names) > 0 && c.Names[0] != "" {
		return c.Names[0]
	}
	return c.ID
}

// dedupeManagers removes overlapping managers from a detector result, keeping
// the canonical (most modern) member of each family. When BOTH dnf and dnf5
// are present, prefer dnf5 (on Fedora 40+ dnf is a thin wrapper around dnf5 and
// they read the same package database). Other managers pass through untouched.
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
			continue
		}
		out = append(out, m)
	}
	return out
}

// dedupeStrings returns a new slice with the unique strings from in, preserving
// first-occurrence order.
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
