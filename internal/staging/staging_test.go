package staging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLocalStager_Prepare_CreatesDir verifies that Prepare returns a staging
// dir that exists on disk and a cleanup func that removes it.
func TestLocalStager_Prepare_CreatesDir(t *testing.T) {
	root := t.TempDir()
	stager := LocalStager{Root: root, ContainerID: "mybunker"}
	dir, cleanup, err := stager.Prepare()
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}
	defer cleanup()

	if !strings.HasPrefix(filepath.Base(dir), "mybunker-") {
		t.Errorf("Prepare dir base = %q, want prefix %q", filepath.Base(dir), "mybunker-")
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("staging dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("staging path %q is not a directory", dir)
	}
	if !strings.HasPrefix(dir, root) {
		t.Errorf("staging dir %q not under Root %q", dir, root)
	}
}

// TestLocalStager_Cleanup_RemovesDir triangulates: cleanup removes the staging
// directory and its contents.
func TestLocalStager_Cleanup_RemovesDir(t *testing.T) {
	root := t.TempDir()
	stager := LocalStager{Root: root, ContainerID: "devbox"}
	dir, cleanup, err := stager.Prepare()
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("after cleanup, dir exists err = %v, want ErrNotExist", err)
	}
}

// TestLocalStager_Prepare_CreatesFilesDir verifies the new layout: Prepare MUST
// create a files/ subdirectory (not the old preserve/) so the container-side
// copy step has a target to extract into.
func TestLocalStager_Prepare_CreatesFilesDir(t *testing.T) {
	root := t.TempDir()
	stager := LocalStager{Root: root, ContainerID: "c1"}
	dir, cleanup, err := stager.Prepare()
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}
	defer cleanup()

	filesDir := filepath.Join(dir, "files")
	info, err := os.Stat(filesDir)
	if err != nil {
		t.Fatalf("files/ subdir not created: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("files/ path %q is not a directory", filesDir)
	}
}

// TestLocalStager_Prepare_NoPreserveDir triangulates: the old v0.1.0 preserve/
// subdirectory MUST NOT be created anymore (it was the host-side bug vector).
func TestLocalStager_Prepare_NoPreserveDir(t *testing.T) {
	root := t.TempDir()
	stager := LocalStager{Root: root, ContainerID: "c2"}
	dir, cleanup, err := stager.Prepare()
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}
	defer cleanup()

	preserveDir := filepath.Join(dir, "preserve")
	if _, err := os.Stat(preserveDir); !os.IsNotExist(err) {
		t.Errorf("preserve/ still created err = %v, want ErrNotExist (removed in v1)", err)
	}
}

// TestLocalStager_FilesDir verifies FilesDir returns the path to the files/
// subdirectory under the prepared staging dir.
func TestLocalStager_FilesDir(t *testing.T) {
	root := t.TempDir()
	stager := LocalStager{Root: root, ContainerID: "c3"}
	dir, cleanup, err := stager.Prepare()
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}
	defer cleanup()

	got := stager.FilesDir()
	want := filepath.Join(dir, "files")
	if got != want {
		t.Errorf("FilesDir() = %q, want %q", got, want)
	}
}

// TestLocalStager_FilesDir_NotPrepared verifies FilesDir returns empty when
// Prepare was not called.
func TestLocalStager_FilesDir_NotPrepared(t *testing.T) {
	stager := LocalStager{Root: t.TempDir(), ContainerID: "c4"}
	if got := stager.FilesDir(); got != "" {
		t.Errorf("FilesDir() before Prepare = %q, want empty", got)
	}
}

// TestLocalStager_BunkerYAMLPath verifies BunkerYAMLPath returns the path to
// bunker.yaml at the staging dir root.
func TestLocalStager_BunkerYAMLPath(t *testing.T) {
	root := t.TempDir()
	stager := LocalStager{Root: root, ContainerID: "c5"}
	dir, cleanup, err := stager.Prepare()
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}
	defer cleanup()

	got := stager.BunkerYAMLPath()
	want := filepath.Join(dir, "bunker.yaml")
	if got != want {
		t.Errorf("BunkerYAMLPath() = %q, want %q", got, want)
	}
}

// TestLocalStager_BunkerYAMLPath_NotPrepared triangulates: BunkerYAMLPath
// returns empty when Prepare was not called.
func TestLocalStager_BunkerYAMLPath_NotPrepared(t *testing.T) {
	stager := LocalStager{Root: t.TempDir(), ContainerID: "c6"}
	if got := stager.BunkerYAMLPath(); got != "" {
		t.Errorf("BunkerYAMLPath() before Prepare = %q, want empty", got)
	}
}