//go:build integration

// Package cmd integration tests run the full backup pipeline against a real
// Podman engine. They are excluded from the default `go test ./...` run by the
// `integration` build tag. Run them with:
//
//	go test -tags=integration ./cmd/...
//
// or via the Makefile target:
//
//	make test-integration
//
// The test spins up a temporary alpine container, runs the backup command
// against it, and asserts the produced .bunker file is a valid zstd-compressed
// tar. It skips itself when podman is not on PATH.
package cmd

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blak0p/bunkerctl/internal/compress"
	"github.com/blak0p/bunkerctl/internal/podman"
)

// TestBackup_Integration_RealPodman is the Slice-7 end-to-end smoke test: it
// verifies the full backup pipeline against a real Podman engine and a
// temporary alpine container. Skipped when podman is unavailable.
func TestBackup_Integration_RealPodman(t *testing.T) {
	// 0. Gate: skip if podman is not on PATH.
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not available")
	}
	ctx := context.Background()

	// 1. Create a temporary alpine container.
	containerName := "bunkerctl-smoke-" + strings.ReplaceAll(t.Name(), "/", "-")
	cleanupContainer(t, containerName) // remove any stale leftover
	if out, err := exec.CommandContext(ctx, "podman", "run", "-d",
		"--name", containerName, "alpine:latest", "sleep", "infinity").CombinedOutput(); err != nil {
		t.Fatalf("podman run alpine: %v\n%s", err, out)
	}
	t.Cleanup(func() { cleanupContainer(t, containerName) })

	// 2. Wait for it to be running (poll up to 10s).
	if err := waitForRunning(ctx, containerName, 10*time.Second); err != nil {
		t.Fatalf("container %s did not reach running: %v", containerName, err)
	}

	// 3. Run the full backup flow via the real CLIRunner with a temp dest.
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "bunker-smoke.bunker")
	setBackupConfigPath(t, filepath.Join(t.TempDir(), "no-config.toml"))
	setBackupStagingRoot(t, t.TempDir())
	setBackupEditor(t, "true")
	// Use the real podman runner (no fake). resetBackupFlags + --output.
	resetBackupFlags()
	cliOutput = dest
	t.Cleanup(func() { cliOutput = "" })

	setBackupRunner(t, podman.NewCLIRunner(""))

	// 4. Execute and assert success.
	out, err := executeBackup(t, containerName)
	if err != nil {
		t.Fatalf("backup integration error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "backup created:") {
		t.Errorf("output = %q, want substring 'backup created:'", out)
	}

	// 5. Assert the .bunker file exists, is non-empty, and is a valid zstd tar.
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf(".bunker not created at %s: %v", dest, err)
	}
	if fi.Size() == 0 {
		t.Fatalf(".bunker file is empty")
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read .bunker: %v", err)
	}
	if len(data) < 4 || data[0] != 0x28 || data[1] != 0xB5 || data[2] != 0x2F || data[3] != 0xFD {
		t.Errorf(".bunker first bytes = % x, want zstd magic 28 b5 2f fd", data[:4])
	}
	// Decompress and confirm it is a valid tar with at least metadata.json.
	extractDir := t.TempDir()
	if err := (compress.ZstdTar{}).Decompress(dest, extractDir); err != nil {
		t.Fatalf("decompress .bunker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(extractDir, "metadata.json")); err != nil {
		t.Errorf("metadata.json not found in extracted archive: %v", err)
	}
}

// cleanupContainer stops and removes the named container, ignoring errors
// (best-effort cleanup).
func cleanupContainer(t *testing.T, name string) {
	t.Helper()
	ctx := context.Background()
	_ = exec.CommandContext(ctx, "podman", "stop", name).Run()
	_ = exec.CommandContext(ctx, "podman", "rm", "-f", name).Run()
}

// waitForRunning polls `podman inspect --format '{{.State.Running}}'` until the
// container reports running=true or the timeout elapses.
func waitForRunning(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := exec.CommandContext(ctx, "podman", "inspect",
			"--format", "{{.State.Running}}", name).CombinedOutput()
		if err == nil && strings.TrimSpace(string(out)) == "true" {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return io.EOF
}
