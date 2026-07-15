package compress

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// TestZstdTar_Compress_ThreeFiles is the RED anchor: compress a temp dir with 3
// files; the dest file MUST exist, be non-empty, and round-trip through a zstd
// decoder + tar reader back to the original 3 files with matching content.
func TestZstdTar_Compress_ThreeFiles(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.txt"), "alpha")
	writeFile(t, filepath.Join(src, "b.txt"), "beta")
	writeFile(t, filepath.Join(src, "sub", "c.txt"), "gamma")

	dest := filepath.Join(t.TempDir(), "out.bunker")
	z := ZstdTar{}
	if err := z.Compress(src, dest); err != nil {
		t.Fatalf("Compress error: %v", err)
	}
	// Dest must exist and be non-empty.
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("dest not created: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatalf("dest is empty")
	}
	// Round-trip: decode zstd then read tar entries.
	got := decodeZstdTar(t, dest)
	want := map[string]string{
		"a.txt":       "alpha",
		"b.txt":       "beta",
		"sub/c.txt":   "gamma",
	}
	for name, wantContent := range want {
		gotContent, ok := got[name]
		if !ok {
			t.Errorf("missing %q in decompressed archive", name)
			continue
		}
		if gotContent != wantContent {
			t.Errorf("content of %q = %q, want %q", name, gotContent, wantContent)
		}
	}
	if len(got) != len(want) {
		t.Errorf("decompressed entry count = %d, want %d", len(got), len(want))
	}
}

// TestZstdTar_Compress_EmptyDir triangulates: compressing an empty dir MUST
// still produce a non-empty dest file (the tar has headers even with no files).
func TestZstdTar_Compress_EmptyDir(t *testing.T) {
	src := t.TempDir()
	dest := filepath.Join(t.TempDir(), "empty.bunker")
	z := ZstdTar{}
	if err := z.Compress(src, dest); err != nil {
		t.Fatalf("Compress empty dir error: %v", err)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("dest not created: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatalf("dest for empty dir is empty; want non-empty (tar headers)")
	}
}

// writeFile writes content to path, creating parent dirs as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// decodeZstdTar decompresses a zstd-compressed tar at path and returns a map of
// entry name → content. Sorted names for deterministic comparison.
func decodeZstdTar(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open dest: %v", err)
	}
	defer f.Close()
	decoder, err := zstd.NewReader(f)
	if err != nil {
		t.Fatalf("zstd decoder: %v", err)
	}
	defer decoder.Close()
	raw, err := io.ReadAll(decoder)
	if err != nil {
		t.Fatalf("zstd read: %v", err)
	}
	tr := tar.NewReader(bytes.NewReader(raw))
	got := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("tar read body: %v", err)
		}
		got[hdr.Name] = string(body)
	}
	return got
}