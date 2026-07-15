package cmd

import (
	"context"
	"os"
	"testing"

	"github.com/blak0p/bunkerctl/internal/cleaner"
	"github.com/blak0p/bunkerctl/internal/compress"
	"github.com/blak0p/bunkerctl/internal/copy"
	"github.com/blak0p/bunkerctl/internal/curate"
	"github.com/blak0p/bunkerctl/internal/podman"
)

// --- Test seams (only used by *_test.go files via t.Helper) ---
//
// These setters let backup E2E tests inject controlled staging roots, editors,
// output paths, and the new pipeline collaborators (copier, cleaner, compressor)
// without touching real ~/.config or spawning a real editor. Each restores the
// previous value on cleanup.

// setBackupStagingRoot overrides the staging parent directory.
func setBackupStagingRoot(t *testing.T, root string) {
	t.Helper()
	orig := backupStagingRoot
	backupStagingRoot = root
	t.Cleanup(func() { backupStagingRoot = orig })
}

// setBackupEditorFallback overrides the fallback editor used when $EDITOR is
// unset. Pass "" to assert the no-editor error path. Also sets $EDITOR so the
// ShellEditor path resolves to the controlled value.
func setBackupEditorFallback(t *testing.T, editor string) {
	t.Helper()
	orig := backupEditorFallback
	backupEditorFallback = editor
	t.Setenv("EDITOR", editor)
	t.Cleanup(func() { backupEditorFallback = orig })
}

// setBackupEditor overrides the whole Editor collaborator with a fake. When
// set, the pipeline uses it instead of ShellEditor; this lets tests drive the
// curation step (and its non-zero-exit abort) without spawning a process.
func setBackupEditor(t *testing.T, e curate.Editor) {
	t.Helper()
	orig := backupEditor
	backupEditor = e
	t.Cleanup(func() { backupEditor = orig })
}

// setBackupDestPath overrides the backup destination .bunker path resolver for
// the duration of the test. The fixed path is returned regardless of the
// container name passed by the pipeline.
func setBackupDestPath(t *testing.T, p string) {
	t.Helper()
	orig := backupDestPathFn
	backupDestPathFn = func(name string) string { return p }
	t.Cleanup(func() { backupDestPathFn = orig })
}

// setBackupCopier overrides the container-side Copier so tests can inject a
// fake that writes canned bytes into staging/files without a real podman exec
// tar pipe.
func setBackupCopier(t *testing.T, c copy.Copier) {
	t.Helper()
	orig := backupCopier
	backupCopier = c
	t.Cleanup(func() { backupCopier = orig })
}

// setBackupCleaner overrides the cache Cleaner so tests can assert on whether
// cleaning ran without depending on a real container.
func setBackupCleaner(t *testing.T, c cleaner.Cleaner) {
	t.Helper()
	orig := backupCleaner
	backupCleaner = c
	t.Cleanup(func() { backupCleaner = orig })
}

// setBackupCompressor overrides the Compressor so tests can capture the staging
// dir / dest path without writing a real zstd-tar.
func setBackupCompressor(t *testing.T, c compress.Compressor) {
	t.Helper()
	orig := backupCompressor
	backupCompressor = c
	t.Cleanup(func() { backupCompressor = orig })
}

// setSafeBackupDefaults wires non-interactive defaults for the new v1 pipeline
// so selection/engine tests are not broken by the staging/copy/compress steps:
// temp staging root, a no-op editor, a no-op copier, a no-op cleaner, a no-op
// compressor, and a temp dest .bunker path so no real file lands in
// ~/.bunkerctl/backups.
func setSafeBackupDefaults(t *testing.T) {
	t.Helper()
	setBackupStagingRoot(t, t.TempDir())
	setBackupEditorFallback(t, "true")
	setBackupDestPath(t, t.TempDir()+"/bunker-safe.bunker")
	setBackupCopier(t, noopCopier{})
	setBackupCleaner(t, cleaner.DefaultCleaner{})
	setBackupCompressor(t, &captureCompressor{})
}

// noopCopier is a Copier that creates the staging files/ dir and copies
// nothing, returning zero bytes. Used by setSafeBackupDefaults so the pipeline
// completes without a real container tar pipe.
type noopCopier struct{}

func (noopCopier) Copy(ctx context.Context, runner podman.Runner, containerID string, opts copy.CopyOptions) (copy.CopyResult, error) {
	return copy.CopyResult{}, nil
}

// captureCompressor is a Compressor that records the srcDir/destPath it was
// called with and writes a minimal placeholder file so the dest exists. Used
// by setSafeBackupDefaults so tests can assert "backup created:" without a real
// zstd stream.
type captureCompressor struct {
	lastSrc  string
	lastDest string
}

func (c *captureCompressor) Compress(srcDir, destPath string) error {
	c.lastSrc = srcDir
	c.lastDest = destPath
	return os.WriteFile(destPath, []byte("bunker"), 0o644)
}