// Package compress provides zstd-compressed tar archive creation for .bunker
// backup files. It wraps github.com/klauspost/compress/zstd behind a Compressor
// interface so tests and restore (SDD 2) can substitute implementations.
package compress

import (
	"archive/tar"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// ErrCompressFailed is returned when archive creation or extraction fails.
var ErrCompressFailed = errors.New("compress failed")

// Compressor creates a compressed archive of a directory tree.
type Compressor interface {
	Compress(srcDir, destPath string) error
}

// Decompressor extracts a compressed archive into a directory.
type Decompressor interface {
	Decompress(srcPath, destDir string) error
}

// ZstdTar creates a tar of srcDir and compresses it with zstd into destPath. It
// also implements Decompress for the restore pipeline (SDD 2).
type ZstdTar struct{}

// Compile-time guarantees.
var (
	_ Compressor   = ZstdTar{}
	_ Decompressor = ZstdTar{}
)

// Compress walks srcDir, writes a tar stream of every regular file (with
// relative paths), pipes it through a zstd encoder, and writes the result to
// destPath.
func (ZstdTar) Compress(srcDir, destPath string) error {
	out, err := os.Create(destPath)
	if err != nil {
		return ErrCompressFailed
	}
	defer out.Close()
	enc, err := zstd.NewWriter(out)
	if err != nil {
		return ErrCompressFailed
	}
	defer enc.Close()
	tw := tar.NewWriter(enc)
	defer tw.Close()
	return tarDir(srcDir, tw)
}

// tarDir walks srcDir and writes a tar entry for every regular file, using the
// path relative to srcDir as the entry name.
func tarDir(srcDir string, tw *tar.Writer) error {
	return filepath.Walk(srcDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		// Use forward slashes for portable tar entries.
		rel = strings.ReplaceAll(rel, string(os.PathSeparator), "/")
		if rel == "." {
			return nil
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

// Decompress reads a zstd-compressed tar from srcPath and extracts every regular
// file into destDir, recreating the directory structure. Restore is out of scope
// for this PR (SDD 2), but the method is shipped now so the interface is stable.
func (ZstdTar) Decompress(srcPath, destDir string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return ErrCompressFailed
	}
	defer f.Close()
	dec, err := zstd.NewReader(f)
	if err != nil {
		return ErrCompressFailed
	}
	defer dec.Close()
	tr := tar.NewReader(dec)
	return extractTar(tr, destDir)
}

// extractTar reads entries from tr and writes them under destDir.
func extractTar(tr *tar.Reader, destDir string) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		dest := filepath.Join(destDir, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			out, err := os.Create(dest)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
}