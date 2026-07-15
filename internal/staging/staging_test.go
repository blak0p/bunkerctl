package staging

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/blak0p/bunkerctl/internal/preserve"
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
	// Drop a file inside to prove cleanup removes contents too.
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("after cleanup, dir exists err = %v, want ErrNotExist", err)
	}
}

// copyFS is a small fstest.MapFS with two real files for Copy tests.
func copyFS() fstest.MapFS {
	return fstest.MapFS{
		"projects/proj1/main.go":   {Data: []byte("package main")},
		"projects/proj1/README.md": {Data: []byte("readme")},
		"projects/proj2/main.go":   {Data: []byte("package main2")},
	}
}

// TestLocalStager_Copy_GlobsAndLiterals verifies Copy expands preserve entries
// (glob + literal) and copies matched files into <staging>/preserve/ with their
// original content preserved.
func TestLocalStager_Copy_GlobsAndLiterals(t *testing.T) {
	root := t.TempDir()
	stager := LocalStager{Root: root, ContainerID: "c1"}
	dir, cleanup, err := stager.Prepare()
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}
	defer cleanup()

	entries := []preserve.Entry{
		{Raw: "projects/**", IsGlob: true},
		{Raw: "projects/proj1/README.md", IsGlob: false},
	}
	expander := preserve.Expander{FS: copyFS()}
	if err := stager.Copy(nil, entries, expander); err != nil {
		t.Fatalf("Copy error: %v", err)
	}

	preserveDir := filepath.Join(dir, "preserve")
	// The glob matches 3 files; the literal is one of those 3 (already counted),
	// so we expect 3 files under preserve/. Order is not guaranteed.
	var found []string
	_ = filepath.WalkDir(preserveDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		found = append(found, p)
		return nil
	})
	if len(found) != 3 {
		t.Fatalf("Copied files len = %d %v, want 3", len(found), found)
	}

	// Verify content of one copied file is intact.
	mainPath := filepath.Join(preserveDir, "projects", "proj1", "main.go")
	b, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read copied main.go: %v", err)
	}
	if string(b) != "package main" {
		t.Errorf("copied main.go content = %q, want %q", string(b), "package main")
	}
}

// TestLocalStager_Copy_LiteralMissingSkipped triangulates: a literal entry that
// matches nothing is skipped silently (no error, no copy).
func TestLocalStager_Copy_LiteralMissingSkipped(t *testing.T) {
	root := t.TempDir()
	stager := LocalStager{Root: root, ContainerID: "c2"}
	dir, cleanup, err := stager.Prepare()
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}
	defer cleanup()

	entries := []preserve.Entry{
		{Raw: "projects/proj1/missing.go", IsGlob: false},
	}
	expander := preserve.Expander{FS: copyFS()}
	if err := stager.Copy(nil, entries, expander); err != nil {
		t.Fatalf("Copy error: %v", err)
	}

	// preserve dir exists but contains no files (the literal was skipped).
	preserveDir := filepath.Join(dir, "preserve")
	if _, err := os.Stat(preserveDir); err != nil {
		t.Fatalf("preserve dir not created: %v", err)
	}
	var count int
	_ = filepath.WalkDir(preserveDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		count++
		return nil
	})
	if count != 0 {
		t.Errorf("copied files = %d, want 0 (missing literal skipped)", count)
	}
}

// TestLocalStager_Copy_GlobNoMatchSkipped triangulates: a glob that matches
// nothing produces ErrGlobNoMatch which Copy treats as a warning (no error
// returned), and no files are copied for that entry.
func TestLocalStager_Copy_GlobNoMatchSkipped(t *testing.T) {
	root := t.TempDir()
	stager := LocalStager{Root: root, ContainerID: "c3"}
	dir, cleanup, err := stager.Prepare()
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}
	defer cleanup()

	entries := []preserve.Entry{
		{Raw: "nowhere/**", IsGlob: true},
	}
	expander := preserve.Expander{FS: copyFS()}
	if err := stager.Copy(nil, entries, expander); err != nil {
		t.Fatalf("Copy error on no-match glob: %v", err)
	}

	// Expect zero files copied (glob had no matches, treated as warning).
	preserveDir := filepath.Join(dir, "preserve")
	var count int
	_ = filepath.WalkDir(preserveDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		count++
		return nil
	})
	if count != 0 {
		t.Errorf("copied files = %d, want 0 (no-match glob skipped)", count)
	}
}