package archive

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blak0p/bunkerctl/internal/compress"
	"github.com/blak0p/bunkerctl/internal/metadata"
	"github.com/blak0p/bunkerctl/internal/podman"
	"github.com/klauspost/compress/zstd"
)

// callRunner records Commit and Save invocations and lets tests drive error
// paths.
type callRunner struct {
	commitCalls   []commitCall
	saveCalls     []saveCall
	commitErr     error
	saveErr       error
	inspectResult podman.InspectResult
}

type commitCall struct{ id, image string }
type saveCall struct{ image, format, dest string }

func (r *callRunner) Version(ctx context.Context) (string, error) {
	return "podman version 5.0.0", nil
}
func (r *callRunner) List(ctx context.Context, all bool) ([]podman.Container, error) {
	return nil, nil
}
func (r *callRunner) Inspect(ctx context.Context, id string) (podman.InspectResult, error) {
	return r.inspectResult, nil
}
func (r *callRunner) Commit(ctx context.Context, id, image string) error {
	r.commitCalls = append(r.commitCalls, commitCall{id, image})
	return r.commitErr
}
func (r *callRunner) Save(ctx context.Context, image, format, dest string) error {
	r.saveCalls = append(r.saveCalls, saveCall{image, format, dest})
	// Write a sentinel payload so the produced archive contains a real image
	// file when decompressed.
	return os.WriteFile(dest, []byte("FAKE-IMAGE-PAYLOAD"), 0o644)
}
func (r *callRunner) Exec(ctx context.Context, id string, cmd []string) (string, error) {
	return "", nil
}

// newProducer wires a DefaultProducer with real compress + metadata writers.
func newProducer() DefaultProducer {
	return DefaultProducer{
		Compressor: compress.ZstdTar{},
		MetaWriter: metadata.JSONWriter{},
	}
}

// TestProducer_Produce_DockerArchive is the RED anchor: Produce with a
// FakeRunner MUST call Commit, call Save with the right format, write metadata,
// and compress to the dest path. The dest file MUST exist.
func TestProducer_Produce_DockerArchive(t *testing.T) {
	r := &callRunner{inspectResult: podman.InspectResult{ID: "mybunker", Image: "fedora:40"}}
	staging := t.TempDir()
	// Seed staging with a preserve file so the archive has real content.
	writeFile(t, filepath.Join(staging, "preserve", "home", "me", "file.txt"), "hello")
	dest := filepath.Join(t.TempDir(), "bunker-mybunker.bunker")

	p := newProducer()
	md, err := p.Produce(context.Background(), r, r.inspectResult, staging, dest, ProduceOptions{
		Format:     "docker-archive",
		BackupDate: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Produce error: %v", err)
	}
	if md.ContainerName != "mybunker" {
		t.Errorf("Metadata.ContainerName = %q, want mybunker", md.ContainerName)
	}
	if md.Format != "docker-archive" {
		t.Errorf("Metadata.Format = %q, want docker-archive", md.Format)
	}
	// Commit was called once.
	if len(r.commitCalls) != 1 {
		t.Errorf("Commit calls = %d, want 1", len(r.commitCalls))
	}
	// Save was called once with docker-archive format.
	if len(r.saveCalls) != 1 {
		t.Errorf("Save calls = %d, want 1", len(r.saveCalls))
	} else if r.saveCalls[0].format != "docker-archive" {
		t.Errorf("Save format = %q, want docker-archive", r.saveCalls[0].format)
	}
	// Dest file exists and is non-empty.
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("dest not created: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatalf("dest is empty")
	}
}

// TestProducer_Produce_CommitFails triangulates: if Commit fails, Produce MUST
// return ErrArchiveFailed and Save MUST NOT be called.
func TestProducer_Produce_CommitFails(t *testing.T) {
	r := &callRunner{
		inspectResult: podman.InspectResult{ID: "bunker", Image: "ubuntu:24.04"},
		commitErr:     errors.New("podman commit failed"),
	}
	staging := t.TempDir()
	dest := filepath.Join(t.TempDir(), "out.bunker")

	p := newProducer()
	_, err := p.Produce(context.Background(), r, r.inspectResult, staging, dest, ProduceOptions{
		Format:     "docker-archive",
		BackupDate: time.Now(),
	})
	if !errors.Is(err, ErrArchiveFailed) {
		t.Errorf("Produce(CommitFails) err = %v, want ErrArchiveFailed", err)
	}
	if len(r.saveCalls) != 0 {
		t.Errorf("Save calls = %d, want 0 when Commit fails", len(r.saveCalls))
	}
}

// TestProducer_Produce_RoundTrip triangulates: the produced dest file MUST
// decompress back and the metadata.json inside MUST match what was passed.
func TestProducer_Produce_RoundTrip(t *testing.T) {
	r := &callRunner{inspectResult: podman.InspectResult{ID: "rtbunker", Image: "alpine:3.20"}}
	staging := t.TempDir()
	writeFile(t, filepath.Join(staging, "preserve", "opt", "app.conf"), "config")
	dest := filepath.Join(t.TempDir(), "rt.bunker")

	p := newProducer()
	_, err := p.Produce(context.Background(), r, r.inspectResult, staging, dest, ProduceOptions{
		Format:     "docker-archive",
		BackupDate: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Produce error: %v", err)
	}

	// Decompress the archive into a temp dir and inspect contents.
	extractDir := t.TempDir()
	decompressZstdTar(t, dest, extractDir)

	// metadata.json must exist and carry the container name + format.
	metaPath := filepath.Join(extractDir, "metadata.json")
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("metadata.json not in archive: %v", err)
	}
	if !strings.Contains(string(metaBytes), "rtbunker") {
		t.Errorf("metadata.json = %q, want substring 'rtbunker'", string(metaBytes))
	}
	if !strings.Contains(string(metaBytes), "docker-archive") {
		t.Errorf("metadata.json = %q, want substring 'docker-archive'", string(metaBytes))
	}
	// preserve file must be present.
	preserveContent, err := os.ReadFile(filepath.Join(extractDir, "preserve", "opt", "app.conf"))
	if err != nil {
		t.Fatalf("preserve file not in archive: %v", err)
	}
	if string(preserveContent) != "config" {
		t.Errorf("preserve content = %q, want 'config'", string(preserveContent))
	}
}

// TestProducer_Produce_OciArchive triangulates: with format "oci-archive" the
// Save call uses --format=oci-archive.
func TestProducer_Produce_OciArchive(t *testing.T) {
	r := &callRunner{inspectResult: podman.InspectResult{ID: "ocibunker", Image: "fedora:40"}}
	staging := t.TempDir()
	dest := filepath.Join(t.TempDir(), "oci.bunker")

	p := newProducer()
	_, err := p.Produce(context.Background(), r, r.inspectResult, staging, dest, ProduceOptions{
		Format:     "oci-archive",
		BackupDate: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Produce error: %v", err)
	}
	if len(r.saveCalls) != 1 || r.saveCalls[0].format != "oci-archive" {
		t.Errorf("Save calls = %+v, want one with format oci-archive", r.saveCalls)
	}
}

// TestProducer_Produce_VersionFromOptions is a RED test (Slice 7): Produce MUST
// copy ProduceOptions.Version into Metadata.Version so the .bunker archive
// records the bunkerctl release that produced it (default "1" is replaced by
// the caller-supplied release version, e.g. "0.1.0").
func TestProducer_Produce_VersionFromOptions(t *testing.T) {
	r := &callRunner{inspectResult: podman.InspectResult{ID: "verbunker", Image: "fedora:40"}}
	staging := t.TempDir()
	dest := filepath.Join(t.TempDir(), "ver.bunker")

	p := newProducer()
	md, err := p.Produce(context.Background(), r, r.inspectResult, staging, dest, ProduceOptions{
		Format:     "docker-archive",
		BackupDate: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		Version:    "0.1.0",
	})
	if err != nil {
		t.Fatalf("Produce error: %v", err)
	}
	if md.Version != "0.1.0" {
		t.Errorf("Metadata.Version = %q, want %q", md.Version, "0.1.0")
	}
}

// --- helpers ---

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

// decompressZstdTar extracts a zstd-compressed tar into destDir (test helper).
func decompressZstdTar(t *testing.T, srcPath, destDir string) {
	t.Helper()
	f, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	dec, err := zstd.NewReader(f)
	if err != nil {
		t.Fatalf("zstd: %v", err)
	}
	defer dec.Close()
	tr := tar.NewReader(dec)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		dest := filepath.Join(destDir, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(dest, 0o755)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(dest), 0o755)
			out, err := os.Create(dest)
			if err != nil {
				t.Fatalf("create %s: %v", dest, err)
			}
			io.Copy(out, tr)
			out.Close()
		}
	}
}
