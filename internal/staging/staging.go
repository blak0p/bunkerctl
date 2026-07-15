// Package staging creates and populates an external staging directory used to
// hold the bunker.yaml manifest and the container-side file copy before
// archive compression.
//
// The staging directory lives outside the source container so that the source
// remains read-only. LocalStager creates a timestamped temp dir under Root with
// a files/ subdirectory that the container-side copy step (internal/copy)
// extracts into. bunker.yaml lives at the staging dir root.
//
// NOTE: the legacy preserve/ subdirectory and Copy method are retained
// temporarily so existing callers (cmd/backup.go v0.1.0 path) keep compiling
// while the new pipeline is wired. They are removed once the v0.1.0 pipeline is
// deleted.
package staging

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/blak0p/bunkerctl/internal/preserve"
)

// Stager abstracts staging-area preparation so tests can substitute in-memory
// or alternate implementations.
type Stager interface {
	Prepare() (stagingDir string, cleanup func(), err error)
}

// LocalStager creates a real on-disk staging directory under Root, named
// <ContainerID>-<timestamp>. Prepare records the created dir in dir so
// FilesDir/BunkerYAMLPath/Copy can reuse it without re-scanning Root.
type LocalStager struct {
	// Root is the parent directory under which the staging dir is created.
	Root string
	// ContainerID is the name/ID of the container being backed up; used to
	// name the staging dir so multiple backups do not collide.
	ContainerID string

	// dir holds the staging dir created by Prepare. Set by Prepare, read by
	// FilesDir/BunkerYAMLPath/Copy. Empty before Prepare is called.
	dir string
}

// Prepare creates a staging directory named <ContainerID>-<timestamp> under
// Root and returns its path plus a cleanup func that removes it. The files/
// subdirectory is created eagerly so the container-side copy step has a target
// to extract into. The legacy preserve/ subdirectory is also created so the
// v0.1.0 host-side copy path keeps working until it is removed.
func (l *LocalStager) Prepare() (string, func(), error) {
	if l.Root == "" {
		return "", nil, errors.New("staging root is empty")
	}
	if l.ContainerID == "" {
		return "", nil, errors.New("staging container id is empty")
	}
	pattern := l.ContainerID + "-*"
	dir, err := os.MkdirTemp(l.Root, pattern)
	if err != nil {
		return "", nil, fmt.Errorf("creating staging dir: %w", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "files"), 0o755); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("creating files subdir: %w", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "preserve"), 0o755); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("creating preserve subdir: %w", err)
	}
	l.dir = dir
	cleanup := func() { _ = os.RemoveAll(dir) }
	return dir, cleanup, nil
}

// FilesDir returns the path to the files/ subdirectory under the prepared
// staging dir. Returns empty when Prepare has not been called.
func (l *LocalStager) FilesDir() string {
	if l.dir == "" {
		return ""
	}
	return filepath.Join(l.dir, "files")
}

// BunkerYAMLPath returns the path to bunker.yaml at the staging dir root.
// Returns empty when Prepare has not been called.
func (l *LocalStager) BunkerYAMLPath() string {
	if l.dir == "" {
		return ""
	}
	return filepath.Join(l.dir, "bunker.yaml")
}

// Copy expands each preserve entry via expander and copies matched files into
// <stagingDir>/preserve/<flattened-path>. Legacy v0.1.0 host-side copy path;
// retained until the v0.1.0 pipeline is deleted.
//
// ctx is accepted for future cancellation wiring; currently unused.
func (l *LocalStager) Copy(ctx context.Context, entries []preserve.Entry, expander preserve.Expander) error {
	if l.dir == "" {
		return errors.New("staging dir not prepared; call Prepare before Copy")
	}
	preserveDir := filepath.Join(l.dir, "preserve")
	for _, entry := range entries {
		matches, expandErr := expander.Expand(entry)
		if expandErr != nil {
			if errors.Is(expandErr, preserve.ErrGlobNoMatch) {
				continue
			}
			return fmt.Errorf("expanding %q: %w", entry.Raw, expandErr)
		}
		for _, m := range matches {
			if err := copyOne(expander.FS, m, preserveDir); err != nil {
				return fmt.Errorf("copying %q: %w", m, err)
			}
		}
	}
	return nil
}

// copyOne copies a single file from FS (src) into dstRoot, preserving the
// relative path under dstRoot. Parent directories are created as needed.
func copyOne(filesystem fs.FS, src, dstRoot string) error {
	rel := strings.TrimPrefix(src, "/")
	data, err := fs.ReadFile(filesystem, rel)
	if err != nil {
		return err
	}
	dst := filepath.Join(dstRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// Compile-time guarantee that *LocalStager satisfies Stager.
var _ Stager = (*LocalStager)(nil)