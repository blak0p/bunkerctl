// Package pkgdetect probes a container for installed packages via the dnf and
// dnf5 package managers, returning per-package name and version (REQ-DETECT-4,
// REQ-DETECT-6). It is distinct from the existing internal/packages package,
// which returns only package names ([]string); pkgdetect returns []Package
// with both name and version as the YAML manifest requires.
//
// DetectManager probes which of dnf/dnf5 is available and prefers dnf5
// (REQ-DETECT-5). Each Detector runs the manager's `list installed` command
// through podman.Runner so tests inject fakes.
package pkgdetect

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blak0p/bunkerctl/internal/packages"
	"github.com/blak0p/bunkerctl/internal/podman"
)

// Package holds a single detected package with name and version. Both fields
// are populated by the manager's output (REQ-DETECT-6, REQ-YAML-5).
type Package struct {
	Name    string
	Version string
}

// Detector probes a container for installed packages of a specific manager and
// returns them with name and version.
type Detector interface {
	Detect(ctx context.Context, runner podman.Runner, containerID string) ([]Package, error)
}

// ErrNoPackageManager is returned by DetectManager when neither dnf nor dnf5 is
// available in the container (REQ-DETECT-5 scenario).
var ErrNoPackageManager = errors.New("no supported package manager found (dnf/dnf5)")

// DnfDetector lists installed packages via `dnf list installed`.
type DnfDetector struct{}

// NewDnfDetector returns a Detector for dnf.
func NewDnfDetector() Detector { return &DnfDetector{} }

// Dnf5Detector lists installed packages via `dnf5 list installed`.
type Dnf5Detector struct{}

// NewDnf5Detector returns a Detector for dnf5.
func NewDnf5Detector() Detector { return &Dnf5Detector{} }

// listCmd returns the in-container command for listing installed packages of
// the manager.
func listCmd(manager string) []string {
	return []string{manager, "list", "installed"}
}

// Detect runs `dnf list installed` and parses the output into []Package.
func (d *DnfDetector) Detect(ctx context.Context, runner podman.Runner, containerID string) ([]Package, error) {
	out, err := runner.Exec(ctx, containerID, listCmd("dnf"))
	if err != nil {
		return nil, fmt.Errorf("running dnf list installed: %w", err)
	}
	return parseDnfList(out), nil
}

// Detect runs `dnf5 list installed` and parses the output into []Package.
func (d *Dnf5Detector) Detect(ctx context.Context, runner podman.Runner, containerID string) ([]Package, error) {
	out, err := runner.Exec(ctx, containerID, listCmd("dnf5"))
	if err != nil {
		return nil, fmt.Errorf("running dnf5 list installed: %w", err)
	}
	return parseDnfList(out), nil
}

// parseDnfList parses `dnf`/`dnf5 list installed` output. The format is:
//
//	Installed Packages
//	<name>.<arch>   <version>-<release>   <repo>
//
// The header line is skipped; the name column is split on the LAST dot to
// separate the package name from the arch suffix (.x86_64, .noarch). The
// version column is the full version-release string.
func parseDnfList(out string) []Package {
	var pkgs []Package
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Installed Packages") {
			continue
		}
		// Fields are separated by runs of spaces; columns are name, version, repo.
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := stripArch(fields[0])
		version := fields[1]
		if name == "" {
			continue
		}
		pkgs = append(pkgs, Package{Name: name, Version: version})
	}
	return pkgs
}

// stripArch removes the .arch suffix from a "name.arch" package column. It
// splits on the LAST dot so names containing dots (e.g. "python3.12") keep
// their internal dots.
func stripArch(nameArch string) string {
	idx := strings.LastIndex(nameArch, ".")
	if idx <= 0 {
		return nameArch
	}
	return nameArch[:idx]
}

// managerProbeOrder is the order DetectManager probes managers. dnf5 is probed
// FIRST so it is preferred when both are present (REQ-DETECT-5).
var managerProbeOrder = []struct {
	Manager packages.Manager
	Bin     string
	DetectorFactory func() Detector
}{
	{Manager: packages.ManagerDnf5, Bin: "dnf5", DetectorFactory: NewDnf5Detector},
	{Manager: packages.ManagerDnf, Bin: "dnf", DetectorFactory: NewDnfDetector},
}

// DetectManager probes which of dnf5/dnf is available (via `which <bin>`) and
// returns the manager name plus the appropriate Detector. dnf5 is preferred
// over dnf (REQ-DETECT-5). Returns ErrNoPackageManager if neither is found.
func DetectManager(ctx context.Context, runner podman.Runner, containerID string) (packages.Manager, Detector, error) {
	for _, mp := range managerProbeOrder {
		if _, err := runner.Exec(ctx, containerID, []string{"which", mp.Bin}); err != nil {
			continue // not present, try next
		}
		return mp.Manager, mp.DetectorFactory(), nil
	}
	return packages.ManagerUnknown, nil, ErrNoPackageManager
}