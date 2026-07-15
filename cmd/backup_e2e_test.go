package cmd

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/blak0p/bunkerctl/internal/podman"
)

// errFakeNonZero simulates a non-zero exit from `which` (manager not present).
var errFakeNonZero = errors.New("fake non-zero exit")

// TestBackup_PreserveStaging_E2E verifies the Slice-3 end-to-end flow: with a
// real config file declaring a preserve glob, a fake FS holding matching files,
// and a FakeRunner with the engine OK + container found, `bunkerctl backup
// mybunker` MUST stage the preserve files into a temp staging dir and print a
// "staged N files" summary.
func TestBackup_PreserveStaging_E2E(t *testing.T) {
	// Build a temp config dir with a config.toml declaring a preserve glob.
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`preserve = ["projects/**"]
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	setBackupConfigPath(t, cfgPath)
	setBackupStagingRoot(t, t.TempDir())
	setBackupDestPath(t, filepath.Join(t.TempDir(), "bunker-mybunker.bunker"))
	setBackupFormat(t, "docker-archive")

	// Fake FS with matching files + an excluded dir.
	fsys := fstest.MapFS{
		"projects/proj1/main.go":          {Data: []byte("p1")},
		"projects/proj1/README.md":        {Data: []byte("p1-readme")},
		"projects/proj2/main.go":          {Data: []byte("p2")},
		"projects/proj1/node_modules/x.js": {Data: []byte("excluded")},
	}
	setBackupPreserveFS(t, fsys)

	// Non-interactive editor so the manifest step does not block.
	setBackupEditor(t, "true")

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectResult: podman.InspectResult{ID: "mybunker", Image: "fedora:40"},
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			// No package manager present → empty package list.
			return "", errFakeNonZero
		},
	})

	out, err := executeBackup(t, "mybunker")
	if err != nil {
		t.Fatalf("backup mybunker error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "staged") {
		t.Errorf("output = %q, want substring 'staged'", out)
	}
	if !strings.Contains(out, "3 files") {
		t.Errorf("output = %q, want substring '3 files' (3 non-excluded matches)", out)
	}
}

// TestBackup_PreserveStaging_EmptyConfig triangulates: a config with no
// preserve key produces an empty staging dir and the backup still succeeds,
// printing "staged 0 files".
func TestBackup_PreserveStaging_EmptyConfig(t *testing.T) {
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`# no preserve key
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	setBackupConfigPath(t, cfgPath)
	setBackupStagingRoot(t, t.TempDir())
	setBackupDestPath(t, filepath.Join(t.TempDir(), "bunker-empty.bunker"))
	setBackupFormat(t, "docker-archive")
	setBackupEditor(t, "true")

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectResult: podman.InspectResult{ID: "emptybunker", Image: "ubuntu:24.04"},
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			return "", errFakeNonZero
		},
	})

	out, err := executeBackup(t, "emptybunker")
	if err != nil {
		t.Fatalf("backup error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "staged 0 files") {
		t.Errorf("output = %q, want substring 'staged 0 files'", out)
	}
}

// TestBackup_PreserveStaging_MissingConfig triangulates: when no config file
// exists (first-time user), backup proceeds with an empty preserve-list and
// prints "staged 0 files" without error.
func TestBackup_PreserveStaging_MissingConfig(t *testing.T) {
	setBackupConfigPath(t, filepath.Join(t.TempDir(), "nope.toml"))
	setBackupStagingRoot(t, t.TempDir())
	setBackupDestPath(t, filepath.Join(t.TempDir(), "bunker-new.bunker"))
	setBackupFormat(t, "docker-archive")
	setBackupEditor(t, "true")

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectResult: podman.InspectResult{ID: "newbunker", Image: "alpine:3.20"},
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			return "", errFakeNonZero
		},
	})

	out, err := executeBackup(t, "newbunker")
	if err != nil {
		t.Fatalf("backup error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "staged 0 files") {
		t.Errorf("output = %q, want substring 'staged 0 files'", out)
	}
}

// TestBackup_Manifest_NoEditorError verifies the Slice-4 no-editor path: when
// $EDITOR is unset and no fallback is configured, the backup command exits
// non-zero with a clear "no editor" message (NOT a panic).
func TestBackup_Manifest_NoEditorError(t *testing.T) {
	setBackupConfigPath(t, filepath.Join(t.TempDir(), "nope.toml"))
	setBackupStagingRoot(t, t.TempDir())
	setBackupEditor(t, "") // no editor, no fallback

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectResult: podman.InspectResult{ID: "noedit", Image: "fedora:40"},
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			// apt is present → packages get detected, so the manifest step runs.
			if len(cmd) > 1 && cmd[0] == "which" && cmd[1] == "apt" {
				return "", nil // apt present
			}
			return "", errFakeNonZero
		},
	})

	out, err := executeBackup(t, "noedit")
	if err == nil {
		t.Fatalf("backup with no editor returned nil error, want non-nil")
	}
	if !strings.Contains(strings.ToLower(out), "no editor") {
		t.Errorf("output = %q, want substring 'no editor'", out)
	}
}

// Compile-time guarantee the fs.FS seam type is used.
var _ fs.FS = fstest.MapFS(nil)

// --- PR 4: cache cleaning + archive production E2E ---

// TestBackup_ArchiveProduction_E2E is a RED test: the full pipeline with a
// FakeRunner (fake image + fake exec outputs) MUST run all the way to
// "backup created: <destPath>" and produce a real .bunker file that is a
// valid zstd-compressed tar.
func TestBackup_ArchiveProduction_E2E(t *testing.T) {
	setSafeBackupDefaults(t)
	// Steer the dest path into a temp dir so we can assert the file exists.
	destDir := t.TempDir()
	setBackupDestPath(t, filepath.Join(destDir, "bunker-mybunker.bunker"))
	setBackupFormat(t, "docker-archive")

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectResult: podman.InspectResult{ID: "mybunker", Image: "fedora:40"},
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			// apt is present → cache cleaning runs.
			if len(cmd) == 2 && cmd[0] == "which" && cmd[1] == "apt" {
				return "", nil
			}
			if len(cmd) == 2 && cmd[0] == "apt-mark" {
				return "vim\ngit\n", nil
			}
			// rm -rf cache → succeed (no-op in the fake).
			return "", nil
		},
	})

	out, err := executeBackup(t, "mybunker")
	if err != nil {
		t.Fatalf("backup error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "backup created:") {
		t.Errorf("output = %q, want substring 'backup created:'", out)
	}
	if !strings.Contains(out, "cleaned") {
		t.Errorf("output = %q, want substring 'cleaned'", out)
	}
	// The .bunker file must exist and be non-empty.
	dest := filepath.Join(destDir, "bunker-mybunker.bunker")
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf(".bunker file not created at %s: %v", dest, err)
	}
	if fi.Size() == 0 {
		t.Fatalf(".bunker file is empty")
	}
	// It must be a valid zstd stream (zstd magic: 0x28 0xB5 0x2F 0xFD).
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read .bunker: %v", err)
	}
	if len(data) < 4 || data[0] != 0x28 || data[1] != 0xB5 || data[2] != 0x2F || data[3] != 0xFD {
		t.Errorf(".bunker first bytes = % x, want zstd magic 28 b5 2f fd", data[:4])
	}
}

// TestBackup_ArchiveProduction_NoManagers triangulates: a container with no
// detectable managers → the cleaner is a no-op and the archive still succeeds.
func TestBackup_ArchiveProduction_NoManagers(t *testing.T) {
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	setBackupDestPath(t, filepath.Join(destDir, "bunker-nomgr.bunker"))
	setBackupFormat(t, "docker-archive")

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectResult: podman.InspectResult{ID: "nomgr", Image: "alpine:3.20"},
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			// No manager present → every which exits non-zero.
			return "", errFakeNonZero
		},
	})

	out, err := executeBackup(t, "nomgr")
	if err != nil {
		t.Fatalf("backup error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "backup created:") {
		t.Errorf("output = %q, want substring 'backup created:'", out)
	}
	// No managers → no cache cleaning (no "cleaned" line).
	if strings.Contains(out, "cleaned") {
		t.Errorf("output = %q, did NOT expect 'cleaned' with no managers", out)
	}
	dest := filepath.Join(destDir, "bunker-nomgr.bunker")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf(".bunker file not created: %v", err)
	}
}