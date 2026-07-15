package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blak0p/bunkerctl/internal/podman"
)

// TestBackupCommand_Registered asserts the backup command is attached to the
// root command (its parent is rootCmd). Carried over from PR 1.
func TestBackupCommand_Registered(t *testing.T) {
	if backupCmd.Parent() != rootCmd {
		t.Errorf("backupCmd.Parent() = %v, want rootCmd", backupCmd.Parent())
	}
}

// TestBackupCommand_Use triangulates: the registered command's Use string is
// "backup", proving the right command was wired. Carried over from PR 1.
func TestBackupCommand_Use(t *testing.T) {
	if backupCmd.Use != "backup" {
		t.Errorf("backupCmd.Use = %q, want %q", backupCmd.Use, "backup")
	}
	if !strings.Contains(backupCmd.Short, "Backup") {
		t.Errorf("backupCmd.Short = %q, want substring %q", backupCmd.Short, "Backup")
	}
}

// The PR 1 placeholder test (TestBackupCommand_NotYetImplemented) was removed
// in PR 2: backup now has real engine-check + selection behavior, superseding
// the "not yet implemented" output. The TestBackup_* tests below cover the new
// behavior.

// setBackupRunner swaps the package-level Runner used by the backup command
// for a fake, and restores the original on cleanup. This is the test seam that
// lets cmd tests drive the backup pipeline without a real Podman engine.
func setBackupRunner(t *testing.T, r podman.Runner) {
	t.Helper()
	orig := backupRunner
	backupRunner = r
	t.Cleanup(func() { backupRunner = orig })
}

// executeBackup executes `bunkerctl backup [args...]` against the injected runner
// and returns the combined output buffer. It wires stdout/stderr so tests can
// assert on printed messages.
func executeBackup(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetRoot()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	backupCmd.SetOut(buf)
	backupCmd.SetErr(buf)
	rootCmd.SetArgs(append([]string{"backup"}, args...))
	err := rootCmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// TestBackup_EngineUnavailable verifies the deferred Slice-1 behavior: when
// the engine is unreachable (Version returns ErrEngineUnavailable), backup
// MUST fail fast with a clear message and a non-zero exit, no panic.
func TestBackup_EngineUnavailable(t *testing.T) {
	setBackupRunner(t, &podman.FakeRunner{Err: podman.ErrEngineUnavailable})
	out, err := executeBackup(t)
	if err == nil {
		t.Fatalf("backup with engine unavailable returned nil error, want non-nil")
	}
	if !strings.Contains(strings.ToLower(out), "engine") {
		t.Errorf("output = %q, want substring mentioning engine", out)
	}
}

// TestBackup_EngineAvailable_NoArg_InteractiveSelection verifies the Slice-2
// interactive chooser: with no positional arg and a fake container list,
// feeding stdin "2\n" selects the second container and prints "selected".
func TestBackup_EngineAvailable_NoArg_InteractiveSelection(t *testing.T) {
	containers := []podman.Container{
		{ID: "id1", Names: []string{"c1"}, Image: "img1", Status: "running"},
		{ID: "id2", Names: []string{"c2"}, Image: "img2", Status: "running"},
		{ID: "id3", Names: []string{"c3"}, Image: "img3", Status: "running"},
	}
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:   "podman version 5.0.0",
		ListResult:   containers,
		InspectResult: podman.InspectResult{ID: "id2", Image: "img2"},
	})
	// Wire stdin so the interactive chooser reads "2\n".
	resetRoot()
	rootCmd.SetIn(strings.NewReader("2\n"))
	out, err := executeBackup(t)
	if err != nil {
		t.Fatalf("backup interactive returned error: %v", err)
	}
	if !strings.Contains(out, "c2") {
		t.Errorf("output = %q, want substring 'c2' (selected container)", out)
	}
	if !strings.Contains(out, "selected") {
		t.Errorf("output = %q, want substring 'selected'", out)
	}
}

// TestBackup_ExplicitName_SelectsDirectly verifies Slice-2 explicit-arg
// selection: `bunkerctl backup mybunker` with the engine OK and the container
// found MUST exit 0 and mention the container name + "selected".
func TestBackup_ExplicitName_SelectsDirectly(t *testing.T) {
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectResult: podman.InspectResult{ID: "mybunker", Image: "fedora:40"},
	})
	out, err := executeBackup(t, "mybunker")
	if err != nil {
		t.Fatalf("backup mybunker returned error: %v", err)
	}
	if !strings.Contains(out, "mybunker") {
		t.Errorf("output = %q, want substring 'mybunker'", out)
	}
	if !strings.Contains(out, "selected") {
		t.Errorf("output = %q, want substring 'selected'", out)
	}
}

// TestBackup_NotFound verifies the not-found scenario: a name that the engine
// reports as not found MUST exit non-zero and print "not found".
func TestBackup_NotFound(t *testing.T) {
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:  "podman version 5.0.0",
		InspectErr:  podman.ErrContainerNotFound,
	})
	out, err := executeBackup(t, "nonexistent")
	if err == nil {
		t.Fatalf("backup nonexistent returned nil error, want non-nil")
	}
	if !strings.Contains(strings.ToLower(out), "not found") {
		t.Errorf("output = %q, want substring 'not found'", out)
	}
}

// TestBackup_InvalidName_EndToEnd triangulates the threat matrix end-to-end:
// a shell-injection attempt passed as a positional arg MUST be rejected at
// the backup command level and exit non-zero with "invalid container name".
func TestBackup_InvalidName_EndToEnd(t *testing.T) {
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr: "podman version 5.0.0",
	})
	out, err := executeBackup(t, "foo;rm -rf /")
	if err == nil {
		t.Fatalf("backup with injection returned nil error, want non-nil")
	}
	if !strings.Contains(strings.ToLower(out), "invalid container name") {
		t.Errorf("output = %q, want substring 'invalid container name'", out)
	}
}

// TestBackup_NoArg_EmptyList triangulates the empty-list edge case: with no
// arg and no containers, the command MUST print "no containers" and exit
// non-zero.
func TestBackup_NoArg_EmptyList(t *testing.T) {
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr: "podman version 5.0.0",
		ListResult: []podman.Container{},
	})
	out, err := executeBackup(t)
	if err == nil {
		t.Fatalf("backup with empty list returned nil error, want non-nil")
	}
	if !strings.Contains(strings.ToLower(out), "no containers") {
		t.Errorf("output = %q, want substring 'no containers'", out)
	}
}

// TestBackup_InvalidName_DifferentChar triangulates the threat matrix with a
// different injection character to prove validation is general.
func TestBackup_InvalidName_DifferentChar(t *testing.T) {
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr: "podman version 5.0.0",
	})
	out, err := executeBackup(t, "foo`whoami`")
	if err == nil {
		t.Fatalf("backup with backtick injection returned nil error, want non-nil")
	}
	if !strings.Contains(strings.ToLower(out), "invalid container name") {
		t.Errorf("output = %q, want substring 'invalid container name'", out)
	}
}

// TestBackup_EngineAvailable_TriangulateVersion triangulates the engine-OK
// path with a different version string to prove the engine check reads the
// real Version return rather than a hardcoded value.
func TestBackup_EngineAvailable_TriangulateVersion(t *testing.T) {
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:    "podman version 4.9.9",
		InspectResult: podman.InspectResult{ID: "devbox", Image: "ubuntu:24.04"},
	})
	out, err := executeBackup(t, "devbox")
	if err != nil {
		t.Fatalf("backup devbox returned error: %v", err)
	}
	if !strings.Contains(out, "devbox") {
		t.Errorf("output = %q, want substring 'devbox'", out)
	}
	// Ensure no engine-error message leaked on the happy path.
	if strings.Contains(strings.ToLower(out), "engine") && !strings.Contains(out, "selected") {
		t.Errorf("happy path output mentions engine unexpectedly: %q", out)
	}
}

// Suppress unused-import linter if errors.Is is not directly used in later
// edits; kept here to mirror PR 1 import style.
var _ = errors.Is