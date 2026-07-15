// Package packages detects which package managers are present inside a
// container and lists the explicitly-installed packages for each.
//
// Detection runs `which <bin>` for each supported manager via the injected
// Runner; listing runs a manager-specific command via Runner.Exec. Both are
// expressed against the podman.Runner interface so tests inject fakes and the
// real implementation shells out through `podman exec`.
package packages

import (
	"context"
	"fmt"
	"strings"

	"github.com/blak0p/bunkerctl/internal/podman"
)

// Manager is the enum of supported package managers.
type Manager string

const (
	ManagerDnf     Manager = "dnf"
	ManagerDnf5    Manager = "dnf5"
	ManagerApt     Manager = "apt"
	ManagerPacman  Manager = "pacman"
	ManagerBrew    Manager = "brew"
	ManagerUnknown Manager = "unknown"
)

// managerBins is the canonical probe order: the binary name each manager is
// detected by. Order is stable so Detect returns a deterministic slice.
var managerBins = []struct {
	Manager Manager
	Bin     string
}{
	{ManagerDnf, "dnf"},
	{ManagerDnf5, "dnf5"},
	{ManagerApt, "apt"},
	{ManagerPacman, "pacman"},
	{ManagerBrew, "brew"},
}

// Detector probes a container for the presence of supported package managers.
type Detector interface {
	Detect(ctx context.Context, runner podman.Runner, containerID string) ([]Manager, error)
}

// DefaultDetector runs `which <bin>` for each supported manager via
// runner.Exec. A manager is considered present when `which` exits 0 (nil error
// from Exec). Managers absent (non-zero exit) are skipped.
type DefaultDetector struct{}

// Compile-time guarantee.
var _ Detector = DefaultDetector{}

// Detect returns the list of package managers present in the container, in the
// canonical probe order. If none are present, it returns an empty slice and
// nil (the backup continues without a package list).
func (DefaultDetector) Detect(ctx context.Context, runner podman.Runner, containerID string) ([]Manager, error) {
	var present []Manager
	for _, mb := range managerBins {
		cmd := []string{"which", mb.Bin}
		if _, err := runner.Exec(ctx, containerID, cmd); err != nil {
			// Non-zero exit: manager not present. Continue probing.
			continue
		}
		present = append(present, mb.Manager)
	}
	return present, nil
}

// Lister queries a container for the explicitly-installed packages of a single
// manager.
type Lister interface {
	List(ctx context.Context, runner podman.Runner, containerID string, m Manager) ([]string, error)
}

// DefaultLister runs a manager-specific command via runner.Exec and parses the
// newline-delimited output into a package-name slice.
type DefaultLister struct{}

// Compile-time guarantee.
var _ Lister = DefaultLister{}

// listCommand returns the command to run inside the container for listing
// explicitly-installed packages of the given manager. For pacman, the pipeline
// is run through `sh -c` since podman exec passes args without a shell.
func listCommand(m Manager) []string {
	switch m {
	case ManagerDnf:
		return []string{"dnf", "repoquery", "--userinstalled", "--qf", "%{name}\n"}
	case ManagerDnf5:
		return []string{"dnf5", "repoquery", "--userinstalled", "--qf", "%{name}\n"}
	case ManagerApt:
		return []string{"apt-mark", "showmanual"}
	case ManagerPacman:
		// pacman -Qe lists explicitly-installed; awk extracts the name column.
		return []string{"sh", "-c", "pacman -Qe | awk '{print $1}'"}
	case ManagerBrew:
		return []string{"brew", "leaves"}
	default:
		return nil
	}
}

// List runs the manager-specific command and parses each non-empty trimmed
// line of the output as a package name. ManagerUnknown returns an empty slice
// with no exec invocation.
func (DefaultLister) List(ctx context.Context, runner podman.Runner, containerID string, m Manager) ([]string, error) {
	cmd := listCommand(m)
	if cmd == nil {
		return []string{}, nil
	}
	out, err := runner.Exec(ctx, containerID, cmd)
	if err != nil {
		return nil, fmt.Errorf("listing packages for %s: %w", m, err)
	}
	return parsePackageList(out), nil
}

// parsePackageList splits the command output on newlines, trimming whitespace
// and dropping empty lines. Returns a non-nil (possibly empty) slice.
func parsePackageList(out string) []string {
	var pkgs []string
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		pkgs = append(pkgs, name)
	}
	return pkgs
}