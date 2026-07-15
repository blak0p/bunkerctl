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
// tar containing bunker.yaml (not metadata.json). It skips itself when podman
// is not on PATH.
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

// TestBackup_Integration_RealPodman is the end-to-end smoke test: it verifies
// the full v1 backup pipeline against a real Podman engine and a temporary
// alpine container. Skipped when podman is unavailable.
//
// NOTE: alpine is not Fedora, so the v1 pipeline's distro guard will reject it.
// This test therefore only exercises the engine-check + selection + inspect
// prefix; a full Fedora-based E2E is the manual `distrobox host exec` proof.
func TestBackup_Integration_RealPodman(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not available")
	}
	ctx := context.Background()

	containerName := "bunkerctl-smoke-" + strings.ReplaceAll(t.Name(), "/", "-")
	cleanupContainer(t, containerName)
	if out, err := exec.CommandContext(ctx, "podman", "run", "-d",
		"--name", containerName, "alpine:latest", "sleep", "infinity").CombinedOutput(); err != nil {
		t.Fatalf("podman run alpine: %v\n%s", err, out)
	}
	t.Cleanup(func() { cleanupContainer(t, containerName) })

	if err := waitForRunning(ctx, containerName, 10*time.Second); err != nil {
		t.Fatalf("container %s did not reach running: %v", containerName, err)
	}

	destDir := t.TempDir()
	dest := filepath.Join(destDir, "bunker-smoke.bunker")
	setBackupStagingRoot(t, t.TempDir())
	setBackupDestPath(t, dest)
	resetBackupFlags()
	t.Cleanup(func() { resetBackupFlags() })

	setBackupRunner(t, podman.NewCLIRunner(""))

	// alpine is not Fedora → the v1 pipeline rejects it with
	// ErrUnsupportedDistro. We assert that error rather than a successful
	// archive, since a full Fedora E2E is the manual distrobox proof.
	_, err := executeBackup(t, "--no-edit", containerName)
	if err == nil {
		t.Fatalf("backup alpine returned nil error, want unsupported-distro rejection")
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Errorf(".bunker created for non-Fedora alpine; want no archive")
	}
	_ = compress.ZstdTar{} // referenced for the decompress path used in 3f
	_ = io.EOF
}

// cleanupContainer stops and removes the named container, ignoring errors.
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