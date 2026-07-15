package metadata

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMetadata_RoundTrip is the RED anchor: JSONWriter.Write then JSONReader.Read
// MUST return the same Metadata that was written.
func TestMetadata_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	in := Metadata{
		ContainerName: "mybunker",
		Image:         "fedora:40",
		CreatedAt:     time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		Managers:      []string{"apt", "pacman"},
		PreserveCount: 7,
		Format:        "docker-archive",
		Version:       "1",
	}
	w := JSONWriter{}
	if err := w.Write(path, in); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	r := JSONReader{}
	got, err := r.Read(path)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if got.ContainerName != in.ContainerName {
		t.Errorf("ContainerName = %q, want %q", got.ContainerName, in.ContainerName)
	}
	if got.Image != in.Image {
		t.Errorf("Image = %q, want %q", got.Image, in.Image)
	}
	if !got.CreatedAt.Equal(in.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, in.CreatedAt)
	}
	if len(got.Managers) != 2 || got.Managers[0] != "apt" || got.Managers[1] != "pacman" {
		t.Errorf("Managers = %v, want [apt pacman]", got.Managers)
	}
	if got.PreserveCount != in.PreserveCount {
		t.Errorf("PreserveCount = %d, want %d", got.PreserveCount, in.PreserveCount)
	}
	if got.Format != in.Format {
		t.Errorf("Format = %q, want %q", got.Format, in.Format)
	}
	if got.Version != in.Version {
		t.Errorf("Version = %q, want %q", got.Version, in.Version)
	}
}

// TestMetadata_MalformedJSON triangulates: reading a file that is not valid
// JSON MUST return ErrInvalidMetadata.
func TestMetadata_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := JSONReader{}
	_, err := r.Read(path)
	if err == nil {
		t.Fatalf("Read(malformed) err = nil, want non-nil")
	}
	// Ensure the sentinel is matched (use errors.Is via a local check).
	if !errors.Is(err, ErrInvalidMetadata) {
		t.Errorf("Read(malformed) err = %v, want ErrInvalidMetadata", err)
	}
}