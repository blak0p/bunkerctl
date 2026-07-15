// Package cleaner removes known package-manager cache directories inside a
// container after the preserve-list has been staged and the manifest confirmed.
package cleaner

import (
	"context"
	"errors"

	"github.com/blak0p/bunkerctl/internal/packages"
	"github.com/blak0p/bunkerctl/internal/podman"
)

// ErrCacheCleanFailed is returned when a cache removal command fails inside the
// container.
var ErrCacheCleanFailed = errors.New("cache clean failed")

// CachePath maps a package manager to the known host-side cache path it leaves
// inside a container. For inside-container cleaning these paths are
// container-relative.
type CachePath struct {
	Manager packages.Manager
	Path    string
}

// Cleaner removes known cache directories for the detected package managers
// inside a container via runner.Exec.
type Cleaner interface {
	Clean(ctx context.Context, runner podman.Runner, containerID string, managers []packages.Manager) error
}

// DefaultCleaner looks up known cache paths per manager and runs
// `rm -rf <path>` inside the container for each.
type DefaultCleaner struct{}

// Compile-time guarantee.
var _ Cleaner = DefaultCleaner{}

// knownCachePaths is the canonical map of manager → cache path. ManagerUnknown
// is absent on purpose (skipped silently).
var knownCachePaths = map[packages.Manager]string{
	packages.ManagerApt:    "/var/cache/apt/archives",
	packages.ManagerDnf:   "/var/cache/dnf",
	packages.ManagerDnf5:  "/var/cache/dnf",
	packages.ManagerPacman: "/var/cache/pacman/pkg",
	packages.ManagerBrew:  "/home/*/.cache/Homebrew",
}

// Clean removes the known cache directory for each detected manager inside the
// container. ManagerUnknown and any manager without a known path are skipped
// silently. A failure from runner.Exec is wrapped in ErrCacheCleanFailed.
func (DefaultCleaner) Clean(ctx context.Context, runner podman.Runner, containerID string, managers []packages.Manager) error {
	for _, m := range managers {
		path, ok := knownCachePaths[m]
		if !ok {
			continue
		}
		if _, err := runner.Exec(ctx, containerID, []string{"rm", "-rf", path}); err != nil {
			return ErrCacheCleanFailed
		}
	}
	return nil
}