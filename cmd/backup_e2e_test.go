package cmd

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/blak0p/bunkerctl/internal/compress"
	"github.com/blak0p/bunkerctl/internal/packages"
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

// TestBackup_Manifest_DedupesWhenBothDnfAndDnf5 is a RED test for a real-world
// bug found by running `bunkerctl backup bunker` on a Fedora 40+ container:
// when the detector finds BOTH dnf and dnf5 (common on modern Fedora where
// `dnf` is a wrapper around dnf5), the lister runs twice and the manifest
// contains each package twice. The pipeline MUST prefer dnf5 over dnf so the
// list is unique.
func TestBackup_Manifest_DedupesWhenBothDnfAndDnf5(t *testing.T) {
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	destPath := filepath.Join(destDir, "bunker-fedora.bunker")
	setBackupDestPath(t, destPath)
	setBackupFormat(t, "docker-archive")
	setBackupEditor(t, "true")

	// The package list the dnf5 lister returns (real Fedora has 70+ pkgs;
	// we just need enough to prove uniqueness after the bug).
	dnf5List := "vim\ngit\nbash\n"
	// dnf returns the same content (it's the same DB on Fedora 40+).
	dnfList := dnf5List

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectResult: podman.InspectResult{ID: "fedora", Image: "fedora:40"},
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			// which dnf  → present
			if len(cmd) == 2 && cmd[0] == "which" && cmd[1] == "dnf" {
				return "", nil
			}
			// which dnf5 → present
			if len(cmd) == 2 && cmd[0] == "which" && cmd[1] == "dnf5" {
				return "", nil
			}
			// All other managers → absent.
			if len(cmd) == 2 && cmd[0] == "which" {
				return "", errFakeNonZero
			}
			// dnf repoquery → returns dnfList
			if len(cmd) >= 1 && cmd[0] == "dnf" {
				return dnfList, nil
			}
			// dnf5 repoquery → returns dnf5List
			if len(cmd) >= 1 && cmd[0] == "dnf5" {
				return dnf5List, nil
			}
			// cache clean + commit + save → succeed in the fake.
			return "", nil
		},
	})

	_, err := executeBackup(t, "fedora")
	if err != nil {
		t.Fatalf("backup error: %v", err)
	}

	// Decompress the .bunker and read manifest.toml from inside.
	extractDir := t.TempDir()
	if err := (compress.ZstdTar{}).Decompress(destPath, extractDir); err != nil {
		t.Fatalf("decompress .bunker: %v", err)
	}
	manifestPath := filepath.Join(extractDir, "manifest.toml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	content := string(data)

	// Assert each package appears exactly once in the manifest body.
	for _, pkg := range []string{"vim", "git", "bash"} {
		count := strings.Count(content, "\n"+pkg+" = \"\"")
		if count != 1 {
			t.Errorf("manifest has %d lines for %q, want 1\nmanifest:\n%s", count, pkg, content)
		}
	}
}

// TestBackup_Manifest_DedupesDuplicatesFromSingleLister triangulates: even when
// a single lister returns a list with internal duplicates (e.g. an upstream
// repoquery bug), the manifest MUST NOT contain duplicate keys (TOML forbids
// them and the re-parse would fail). This is the package-level dedupe
// defense-in-depth path.
func TestBackup_Manifest_DedupesDuplicatesFromSingleLister(t *testing.T) {
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	destPath := filepath.Join(destDir, "bunker-dups.bunker")
	setBackupDestPath(t, destPath)
	setBackupFormat(t, "docker-archive")
	setBackupEditor(t, "true")

	// dnf5 returns a list with one duplicate line (vim appears twice).
	dupList := "vim\ngit\nvim\nbash\n"

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectResult: podman.InspectResult{ID: "dupbunker", Image: "fedora:40"},
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			if len(cmd) == 2 && cmd[0] == "which" && cmd[1] == "dnf5" {
				return "", nil
			}
			if len(cmd) == 2 && cmd[0] == "which" {
				return "", errFakeNonZero
			}
			if len(cmd) >= 1 && cmd[0] == "dnf5" {
				return dupList, nil
			}
			return "", nil
		},
	})

	_, err := executeBackup(t, "dupbunker")
	if err != nil {
		t.Fatalf("backup error: %v", err)
	}

	extractDir := t.TempDir()
	if err := (compress.ZstdTar{}).Decompress(destPath, extractDir); err != nil {
		t.Fatalf("decompress .bunker: %v", err)
	}
	manifestPath := filepath.Join(extractDir, "manifest.toml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	content := string(data)

	// vim must appear exactly once even though the lister returned it twice.
	if c := strings.Count(content, "\nvim = \"\""); c != 1 {
		t.Errorf("manifest has %d lines for 'vim', want 1\nmanifest:\n%s", c, content)
	}
	if c := strings.Count(content, "\ngit = \"\""); c != 1 {
		t.Errorf("manifest has %d lines for 'git', want 1\nmanifest:\n%s", c, content)
	}
	if c := strings.Count(content, "\nbash = \"\""); c != 1 {
		t.Errorf("manifest has %d lines for 'bash', want 1\nmanifest:\n%s", c, content)
	}
}

// TestDedupeManagers is a unit test for the pure dedupe helper: when both dnf
// and dnf5 are detected, dnf is dropped; other managers are passed through in
// order.
func TestDedupeManagers(t *testing.T) {
	cases := []struct {
		name string
		in   []packages.Manager
		want []packages.Manager
	}{
		{"empty", nil, []packages.Manager{}},
		{"only dnf5", []packages.Manager{packages.ManagerDnf5}, []packages.Manager{packages.ManagerDnf5}},
		{"only dnf", []packages.Manager{packages.ManagerDnf}, []packages.Manager{packages.ManagerDnf}},
		{"dnf then dnf5", []packages.Manager{packages.ManagerDnf, packages.ManagerDnf5}, []packages.Manager{packages.ManagerDnf5}},
		{"dnf5 then dnf (canonical order)", []packages.Manager{packages.ManagerDnf5, packages.ManagerDnf}, []packages.Manager{packages.ManagerDnf5}},
		{"dnf + apt", []packages.Manager{packages.ManagerDnf, packages.ManagerApt}, []packages.Manager{packages.ManagerDnf, packages.ManagerApt}},
		{"dnf + dnf5 + apt", []packages.Manager{packages.ManagerDnf, packages.ManagerDnf5, packages.ManagerApt}, []packages.Manager{packages.ManagerDnf5, packages.ManagerApt}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dedupeManagers(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("dedupeManagers(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestDedupeStrings is a unit test for the string dedupe helper: preserves
// first-occurrence order, drops later occurrences.
func TestDedupeStrings(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, []string{}},
		{"empty", []string{}, []string{}},
		{"no dupes", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"with dupes", []string{"a", "b", "a", "c", "b"}, []string{"a", "b", "c"}},
		{"all same", []string{"x", "x", "x"}, []string{"x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dedupeStrings(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("dedupeStrings(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

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
// TestBackup_PreserveTildeExpanded_E2E verifies the fix for the bug where
// preserve-list entries starting with "~/" were passed to the FS literally
// (with the tilde), causing Stat/ReadFile to fail and the preserve dir in
// the .bunker to be empty. The pipeline MUST expand "~/" to the user's home
// directory before evaluating the preserve path.
func TestBackup_PreserveTildeExpanded_E2E(t *testing.T) {
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	destPath := filepath.Join(destDir, "bunker-tilde.bunker")
	setBackupDestPath(t, destPath)
	setBackupFormat(t, "docker-archive")
	setBackupEditor(t, "true")

	// Create a real temp dir and point $HOME at it so that `~/dotfile` resolves
	// deterministically. The preserve entry is `~/dotfile` (literal, not glob).
	homeDir := t.TempDir()
	dotfilePath := filepath.Join(homeDir, "dotfile")
	if err := os.WriteFile(dotfilePath, []byte("important config"), 0o644); err != nil {
		t.Fatalf("write dotfile: %v", err)
	}
	t.Setenv("HOME", homeDir)

	// Config file: preserve ["~/dotfile"]
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`preserve = ["~/dotfile"]
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	setBackupConfigPath(t, cfgPath)

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectResult: podman.InspectResult{ID: "tildebunker", Image: "fedora:40"},
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			// No package manager present.
			return "", errFakeNonZero
		},
	})

	out, err := executeBackup(t, "tildebunker")
	if err != nil {
		t.Fatalf("backup error: %v\noutput: %s", err, out)
	}

	// Assert that staging reported 1 file.
	if !strings.Contains(out, "staged 1 files") {
		t.Errorf("output = %q, want substring 'staged 1 files' (tilde path should resolve and stage the dotfile)", out)
	}

	// Decompress the .bunker and verify the dotfile is inside preserve/.
	extractDir := t.TempDir()
	if err := (compress.ZstdTar{}).Decompress(destPath, extractDir); err != nil {
		t.Fatalf("decompress .bunker: %v", err)
	}
	expectedPath := filepath.Join(extractDir, "preserve", homeDir, "dotfile")
	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read preserved dotfile at %s: %v", expectedPath, err)
	}
	if string(data) != "important config" {
		t.Errorf("preserved dotfile content = %q, want %q", data, "important config")
	}
}
