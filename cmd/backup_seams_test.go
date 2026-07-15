package cmd

import (
	"io/fs"
	"path/filepath"
	"testing"
)

// --- Test seams (only used by *_test.go files via t.Helper) ---
//
// These setters let backup E2E tests inject controlled config paths, staging
// roots, preserve filesystems, and editor fallbacks without touching real
// ~/.config or spawning a real editor. Each restores the previous value on
// cleanup.

// setBackupConfigPath overrides the config file path resolver for the duration
// of the test.
func setBackupConfigPath(t *testing.T, p string) {
	t.Helper()
	orig := backupConfigPathFn
	backupConfigPathFn = func() string { return p }
	t.Cleanup(func() { backupConfigPathFn = orig })
}

// setBackupStagingRoot overrides the staging parent directory.
func setBackupStagingRoot(t *testing.T, root string) {
	t.Helper()
	orig := backupStagingRoot
	backupStagingRoot = root
	t.Cleanup(func() { backupStagingRoot = orig })
}

// setBackupPreserveFS overrides the fs.FS used by the preserve expander.
func setBackupPreserveFS(t *testing.T, fsys fs.FS) {
	t.Helper()
	orig := backupPreserveFS
	backupPreserveFS = fsys
	t.Cleanup(func() { backupPreserveFS = orig })
}

// setSafeBackupDefaults wires non-interactive defaults for the staging +
// manifest + archive phases so earlier selection/engine tests (PR 2) are not
// broken by the new pipeline: empty config, temp staging root, `true` as
// editor, and a temp dest .bunker path so no real file lands in
// ~/.bunkerctl/backups. (PR 4 added the dest-path default.)
func setSafeBackupDefaults(t *testing.T) {
	t.Helper()
	setBackupConfigPath(t, filepath.Join(t.TempDir(), "no-config.toml"))
	setBackupStagingRoot(t, t.TempDir())
	setBackupEditor(t, "true")
	setBackupDestPath(t, filepath.Join(t.TempDir(), "bunker-safe.bunker"))
	setBackupFormat(t, "docker-archive")
}
// ShellEditor (which reads $EDITOR first) uses the controlled value. Pass "" to
// assert the no-editor error path (both $EDITOR unset and empty fallback).
func setBackupEditor(t *testing.T, editor string) {
	t.Helper()
	orig := backupEditorFallback
	backupEditorFallback = editor
	t.Setenv("EDITOR", editor)
	t.Cleanup(func() { backupEditorFallback = orig })
}

// setBackupDestPath overrides the backup destination .bunker path resolver for
// the duration of the test. The fixed path is returned regardless of the
// container name passed by the pipeline. (PR 4)
func setBackupDestPath(t *testing.T, p string) {
	t.Helper()
	orig := backupDestPathFn
	backupDestPathFn = func(name string) string { return p }
	t.Cleanup(func() { backupDestPathFn = orig })
}

// setBackupFormat overrides the podman save --format value for the duration of
// the test. (PR 4)
func setBackupFormat(t *testing.T, format string) {
	t.Helper()
	orig := backupFormat
	backupFormat = format
	t.Cleanup(func() { backupFormat = orig })
}