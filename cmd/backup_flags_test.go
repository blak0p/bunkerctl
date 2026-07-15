package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blak0p/bunkerctl/internal/copy"
	"github.com/blak0p/bunkerctl/internal/podman"
)

// TestBackup_OutputFlag_WritesToPath verifies REQ-CLI-3: `bunkerctl backup
// --output=/tmp/foo.bunker mybunker` MUST succeed and the .bunker file MUST
// appear at exactly that path.
func TestBackup_OutputFlag_WritesToPath(t *testing.T) {
	setSafeBackupDefaults(t)
	outDir := t.TempDir()
	dest := filepath.Join(outDir, "custom.bunker")

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "mybunker", Image: "fedora:45"},
		InspectRawResult: `[{"Id":"mybunker","Image":"fedora:45","Config":{"User":"1000"},"State":{"Running":true}}]`,
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			switch strings.Join(cmd, " ") {
			case "getent passwd 1000":
				return "alejndro:x:1000:1000::/home/alejndro:/bin/bash", nil
			case "getent passwd":
				return "alejndro:x:1000:1000::/home/alejndro:/bin/bash", nil
			case "cat /etc/os-release":
				return "ID=fedora\nVERSION_ID=45\n", nil
			case "which dnf5", "which dnf":
				return "", nil
			case "dnf5 repoquery --installed", "dnf list installed":
				return "neovim-0:0.10.2-1.fc40.x86_64\n", nil
			}
			return "", nil
		},
	})

	out, err := executeBackup(t, "--output="+dest, "mybunker")
	if err != nil {
		t.Fatalf("backup --output error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "backup created:") {
		t.Errorf("output = %q, want substring 'backup created:'", out)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf(".bunker file not created at --output path %s: %v", dest, err)
	}
}

// TestBackup_OutputFlag_MissingParentDir triangulates: --output pointing at a
// path whose parent directory does not exist MUST fail with a clear error.
func TestBackup_OutputFlag_MissingParentDir(t *testing.T) {
	setSafeBackupDefaults(t)
	missing := filepath.Join(t.TempDir(), "does", "not", "exist", "out.bunker")

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "mybunker", Image: "fedora:45"},
		InspectRawResult: `[{"Id":"mybunker","Image":"fedora:45","Config":{"User":"1000"},"State":{"Running":true}}]`,
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			switch strings.Join(cmd, " ") {
			case "getent passwd 1000":
				return "alejndro:x:1000:1000::/home/alejndro:/bin/bash", nil
			case "getent passwd":
				return "alejndro:x:1000:1000::/home/alejndro:/bin/bash", nil
			case "cat /etc/os-release":
				return "ID=fedora\nVERSION_ID=45\n", nil
			case "which dnf5", "which dnf":
				return "", nil
			case "dnf5 repoquery --installed", "dnf list installed":
				return "neovim-0:0.10.2-1.fc40.x86_64\n", nil
			}
			return "", nil
		},
	})

	out, err := executeBackup(t, "--output="+missing, "mybunker")
	if err == nil {
		t.Fatalf("backup --output with missing parent returned nil error, want non-nil")
	}
	if !strings.Contains(strings.ToLower(out), "output") && !strings.Contains(strings.ToLower(out), "path") {
		t.Errorf("output = %q, want substring mentioning 'output' or 'path'", out)
	}
}

// TestBackup_NoEditFlag_SkipsEditor verifies REQ-CLI-4/REQ-EDIT-3: with
// --no-edit, the editor step is skipped and the backup completes without
// invoking $EDITOR. We assert by setting a fake editor that would fail the
// pipeline if invoked.
func TestBackup_NoEditFlag_SkipsEditor(t *testing.T) {
	setSafeBackupDefaults(t)
	// Inject an editor that, if invoked, aborts the backup.
	setBackupEditor(t, failingEditor{})

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "mybunker", Image: "fedora:45"},
		InspectRawResult: `[{"Id":"mybunker","Image":"fedora:45","Config":{"User":"1000"},"State":{"Running":true}}]`,
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			switch strings.Join(cmd, " ") {
			case "getent passwd 1000":
				return "alejndro:x:1000:1000::/home/alejndro:/bin/bash", nil
			case "getent passwd":
				return "alejndro:x:1000:1000::/home/alejndro:/bin/bash", nil
			case "cat /etc/os-release":
				return "ID=fedora\nVERSION_ID=45\n", nil
			case "which dnf5", "which dnf":
				return "", nil
			case "dnf5 repoquery --installed", "dnf list installed":
				return "neovim-0:0.10.2-1.fc40.x86_64\n", nil
			}
			return "", nil
		},
	})

	out, err := executeBackup(t, "--no-edit", "mybunker")
	if err != nil {
		t.Fatalf("backup --no-edit error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "backup created:") {
		t.Errorf("output = %q, want substring 'backup created:'", out)
	}
}

// TestBackup_IgnoreExtraFlag_AddsPatterns verifies REQ-CLI-5:
// --ignore-extra=build,dist adds those patterns to the effective ignore list.
// We capture the ignore list the Copier received via a capturing copier.
func TestBackup_IgnoreExtraFlag_AddsPatterns(t *testing.T) {
	setSafeBackupDefaults(t)
	cap := &capturingCopier{}
	setBackupCopier(t, cap)

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "mybunker", Image: "fedora:45"},
		InspectRawResult: `[{"Id":"mybunker","Image":"fedora:45","Config":{"User":"1000"},"State":{"Running":true}}]`,
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			switch strings.Join(cmd, " ") {
			case "getent passwd 1000":
				return "alejndro:x:1000:1000::/home/alejndro:/bin/bash", nil
			case "getent passwd":
				return "alejndro:x:1000:1000::/home/alejndro:/bin/bash", nil
			case "cat /etc/os-release":
				return "ID=fedora\nVERSION_ID=45\n", nil
			case "which dnf5", "which dnf":
				return "", nil
			case "dnf5 repoquery --installed", "dnf list installed":
				return "neovim-0:0.10.2-1.fc40.x86_64\n", nil
			}
			return "", nil
		},
	})

	if _, err := executeBackup(t, "--no-edit", "--ignore-extra=build,dist", "mybunker"); err != nil {
		t.Fatalf("backup --ignore-extra error: %v", err)
	}
	if !cap.called {
		t.Fatalf("Copier not invoked")
	}
	if !containsString(cap.lastOpts.Ignore, "build") || !containsString(cap.lastOpts.Ignore, "dist") {
		t.Errorf("effective ignore list = %v, want to contain build and dist", cap.lastOpts.Ignore)
	}
	// Defaults must still be present alongside the extras.
	for _, d := range []string{".cache", "node_modules", "*.log"} {
		if !containsString(cap.lastOpts.Ignore, d) {
			t.Errorf("default %q missing from effective ignore list %v", d, cap.lastOpts.Ignore)
		}
	}
}

// TestBackup_Help_ListsNewFlags verifies `bunkerctl backup --help` lists the new
// flags (--output, --no-edit, --ignore-extra) and NOT the removed --format.
func TestBackup_Help_ListsNewFlags(t *testing.T) {
	resetRoot()
	resetBackupFlags()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	backupCmd.SetOut(buf)
	backupCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"backup", "--help"})

	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("backup --help error: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"--output", "--no-edit", "--ignore-extra"} {
		if !strings.Contains(got, want) {
			t.Errorf("help output missing %q; got %q", want, got)
		}
	}
	if strings.Contains(got, "--format") {
		t.Errorf("help output still lists removed --format; got %q", got)
	}
}

// TestBackup_Help_LongDescription triangulates: the --help output MUST contain
// the Long description.
func TestBackup_Help_LongDescription(t *testing.T) {
	resetRoot()
	resetBackupFlags()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	backupCmd.SetOut(buf)
	backupCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"backup", "--help"})

	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("backup --help error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "bunker.yaml") {
		t.Errorf("help output missing Long description (word 'bunker.yaml'); got %q", got)
	}
}

// failingEditor is a curate.Editor that always returns exit code 1; used to
// prove --no-edit skips the editor (if the editor ran, the backup would abort).
type failingEditor struct{}

func (failingEditor) Edit(path string) (int, error) { return 1, nil }

// capturingCopier records the CopyOptions it received.
type capturingCopier struct {
	called   bool
	lastOpts copy.CopyOptions
}

func (c *capturingCopier) Copy(ctx context.Context, runner podman.Runner, containerID string, opts copy.CopyOptions) (copy.CopyResult, error) {
	c.called = true
	c.lastOpts = opts
	return copy.CopyResult{}, nil
}

// containsString is a tiny helper for slice membership.
func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}