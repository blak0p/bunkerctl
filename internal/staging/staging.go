// Package staging creates and populates an external staging directory used to
// hold the bunker.yaml manifest and the container-side file copy before
// archive compression.
//
// The staging directory lives outside the source container so that the source
// remains read-only. LocalStager creates a timestamped temp dir under Root with
// a files/ subdirectory that the container-side copy step (internal/copy)
// extracts into. bunker.yaml lives at the staging dir root.
package staging

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Stager abstracts staging-area preparation so tests can substitute in-memory
// or alternate implementations.
type Stager interface {
	Prepare() (stagingDir string, cleanup func(), err error)
}

// LocalStager creates a real on-disk staging directory under Root, named
// <ContainerID>-<timestamp>. Prepare records the created dir in dir so
// FilesDir/BunkerYAMLPath can reuse it without re-scanning Root.
type LocalStager struct {
	// Root is the parent directory under which the staging dir is created.
	Root string
	// ContainerID is the name/ID of the container being backed up; used to
	// name the staging dir so multiple backups do not collide.
	ContainerID string

	// dir holds the staging dir created by Prepare. Set by Prepare, read by
	// FilesDir/BunkerYAMLPath. Empty before Prepare is called.
	dir string
}

// Prepare creates a staging directory named <ContainerID>-<timestamp> under
// Root and returns its path plus a cleanup func that removes it. The files/
// subdirectory is created eagerly so the container-side copy step has a target
// to extract into.
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

// Compile-time guarantee that *LocalStager satisfies Stager.
var _ Stager = (*LocalStager)(nil)