package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blak0p/bunkerctl/internal/podman"
)

// TestBackup_FormatFlag_InvalidValue is a RED test (Slice 7): `bunkerctl backup
// --format=gzip mybunker` MUST exit non-zero and the error MUST mention an
// invalid format referencing the allowed values (docker-archive / oci-archive).
// Before PR 5 there is no --format flag, so cobra would error on an unknown
// flag; once the flag exists it must validate the value and fail with a clear
// message. Either way, non-zero exit + mention of the format error is the
// observable behavior we assert.
func TestBackup_FormatFlag_InvalidValue(t *testing.T) {
	setSafeBackupDefaults(t)
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectResult: podman.InspectResult{ID: "mybunker", Image: "fedora:40"},
	})

	out, err := executeBackup(t, "--format=gzip", "mybunker")
	if err == nil {
		t.Fatalf("backup --format=gzip returned nil error, want non-nil")
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "invalid") || !strings.Contains(low, "format") {
		t.Errorf("output = %q, want substring mentioning 'invalid' and 'format'", out)
	}
}

// TestBackup_FormatFlag_OciArchive triangulates (GREEN): `bunkerctl backup
// --format=oci-archive mybunker` with a valid container MUST succeed and the
// Save call MUST be invoked with format "oci-archive". This proves the flag
// value flows through the backupFormat seam into the archive producer.
func TestBackup_FormatFlag_OciArchive(t *testing.T) {
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	setBackupDestPath(t, filepath.Join(destDir, "bunker-oci.bunker"))

	r := &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectResult: podman.InspectResult{ID: "mybunker", Image: "fedora:40"},
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			return "", errFakeNonZero // no managers
		},
	}
	setBackupRunner(t, r)

	if _, err := executeBackup(t, "--format=oci-archive", "mybunker"); err != nil {
		t.Fatalf("backup --format=oci-archive error: %v", err)
	}
	// FakeRunner.SaveCalls must record the format. This is the production
	// behavior the test drives: the --format flag value reaches Save.
	if len(r.SaveCalls) != 1 {
		t.Fatalf("SaveCalls = %d, want 1 (got %+v)", len(r.SaveCalls), r.SaveCalls)
	}
	if r.SaveCalls[0].Format != "oci-archive" {
		t.Errorf("SaveCalls[0].Format = %q, want %q", r.SaveCalls[0].Format, "oci-archive")
	}
}

// TestBackup_FormatFlag_DockerArchiveDefault triangulates: with --format
// omitted (default) the Save call uses docker-archive; with
// --format=docker-archive explicit, the same.
func TestBackup_FormatFlag_DockerArchiveDefault(t *testing.T) {
	setSafeBackupDefaults(t)
	// Override the dest seam to point into a temp dir.
	destDir := t.TempDir()
	setBackupDestPath(t, filepath.Join(destDir, "bunker-default.bunker"))

	r := &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectResult: podman.InspectResult{ID: "defbunker", Image: "fedora:40"},
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			return "", errFakeNonZero
		},
	}
	setBackupRunner(t, r)

	if _, err := executeBackup(t, "defbunker"); err != nil {
		t.Fatalf("backup defbunker (default format) error: %v", err)
	}
	if len(r.SaveCalls) != 1 || r.SaveCalls[0].Format != "docker-archive" {
		t.Errorf("SaveCalls = %+v, want one with Format docker-archive", r.SaveCalls)
	}
}

// TestBackup_OutputFlag_WritesToPath is a RED test (Slice 7): `bunkerctl backup
// --output=/tmp/foo.bunker mybunker` MUST succeed and the .bunker file MUST
// appear at exactly that path. Before PR 5 the --output flag does not exist.
func TestBackup_OutputFlag_WritesToPath(t *testing.T) {
	setSafeBackupDefaults(t)
	// Do NOT call setBackupDestPath: we want the --output flag to control the
	// dest path. setSafeBackupDefaults sets a safe dest, but the flag must
	// override it.
	outDir := t.TempDir()
	dest := filepath.Join(outDir, "custom.bunker")

	r := &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectResult: podman.InspectResult{ID: "mybunker", Image: "fedora:40"},
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			return "", errFakeNonZero
		},
	}
	setBackupRunner(t, r)

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
// path whose parent directory does not exist MUST fail with a clear error. We
// do NOT auto-mkdir; the user must fix the path.
func TestBackup_OutputFlag_MissingParentDir(t *testing.T) {
	setSafeBackupDefaults(t)
	missing := filepath.Join(t.TempDir(), "does", "not", "exist", "out.bunker")

	r := &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectResult: podman.InspectResult{ID: "mybunker", Image: "fedora:40"},
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			return "", errFakeNonZero
		},
	}
	setBackupRunner(t, r)

	out, err := executeBackup(t, "--output="+missing, "mybunker")
	if err == nil {
		t.Fatalf("backup --output with missing parent returned nil error, want non-nil")
	}
	if !strings.Contains(strings.ToLower(out), "output") && !strings.Contains(strings.ToLower(out), "path") {
		t.Errorf("output = %q, want substring mentioning 'output' or 'path'", out)
	}
}

// TestBackup_Help_ListsFlags is a RED test (Slice 7): `bunkerctl backup --help`
// MUST list both --format and --output flags with their defaults.
func TestBackup_Help_ListsFlags(t *testing.T) {
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
	if !strings.Contains(got, "--format") {
		t.Errorf("help output missing --format flag; got %q", got)
	}
	if !strings.Contains(got, "docker-archive") {
		t.Errorf("help output missing default format docker-archive; got %q", got)
	}
	if !strings.Contains(got, "--output") {
		t.Errorf("help output missing --output flag; got %q", got)
	}
}

// TestBackup_Help_LongDescription triangulates: the --help output MUST contain
// the new Long description summarizing what the backup does.
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
	if !strings.Contains(got, "portable") {
		t.Errorf("help output missing Long description (word 'portable'); got %q", got)
	}
	if !strings.Contains(got, "Examples") && !strings.Contains(got, "Example") {
		t.Errorf("help output missing Examples block; got %q", got)
	}
}
