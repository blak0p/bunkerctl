// Package pkgdetect probes a container for installed packages via the dnf and
// dnf5 package managers, returning per-package name and version (REQ-DETECT-4,
// REQ-DETECT-6). It is distinct from the existing internal/packages package,
// which returns only package names ([]string); pkgdetect returns []Package
// with both name and version as the YAML manifest requires.
//
// DetectManager probes which of dnf/dnf5 is available and prefers dnf5
// (REQ-DETECT-5). DnfDetector runs `dnf list installed`; Dnf5Detector runs
// `dnf5 repoquery --installed` (NOT `dnf5 list installed`, which returns exit
// 1 with "No matching packages to list" on real Fedora 44/45 even when
// packages are installed — repoquery reads the rpmdb directly and exits 0).
// Each Detector runs through podman.Runner so tests inject fakes.
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

// Dnf5Detector lists installed packages via `dnf5 repoquery --installed`.
// repoquery reads the rpmdb directly and returns one `name-epoch:version-
// release.arch` line per installed package; unlike `dnf5 list installed`, it
// exits 0 and returns the complete list on real Fedora 44/45.
type Dnf5Detector struct{}

// NewDnf5Detector returns a Detector for dnf5.
func NewDnf5Detector() Detector { return &Dnf5Detector{} }

// Detect runs `dnf list installed` and parses the output into []Package.
func (d *DnfDetector) Detect(ctx context.Context, runner podman.Runner, containerID string) ([]Package, error) {
	out, err := runner.Exec(ctx, containerID, []string{"dnf", "list", "installed"})
	if err != nil {
		return nil, fmt.Errorf("running dnf list installed: %w", err)
	}
	return parseDnfList(out), nil
}

// Detect runs `dnf5 repoquery --installed` and parses the output into []Package.
func (d *Dnf5Detector) Detect(ctx context.Context, runner podman.Runner, containerID string) ([]Package, error) {
	out, err := runner.Exec(ctx, containerID, []string{"dnf5", "repoquery", "--installed"})
	if err != nil {
		return nil, fmt.Errorf("running dnf5 repoquery --installed: %w", err)
	}
	return parseDnf5Repoquery(out), nil
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

// parseDnf5Repoquery parses `dnf5 repoquery --installed` output. The format is
// one package per line, no header:
//
//	<name>-<epoch>:<version>-<release>.<arch>
//
// e.g. `neovim-0:0.10.2-1.fc44.x86_64` or `cups-filesystem-1:2.4.19-3.fc44.noarch`.
//
// Splitting strategy: the arch is the substring after the LAST dot; the
// epoch:version-release is the segment between the package name and the arch.
// The package name can contain dots and hyphens (e.g. python3.12,
// apr-util-lmdb), so it cannot be split by those. Instead we locate the
// epoch:version-release segment by finding the FIRST `:` in the line — every
// repoquery line includes an explicit epoch (`0:` when zero). Everything before
// the epoch segment's leading `-` is the name; the rest is version-release.
func parseDnf5Repoquery(out string) []Package {
	var pkgs []Package
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pkg, ok := parseDnf5RepoqueryLine(line)
		if !ok {
			continue
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs
}

// parseDnf5RepoqueryLine parses a single `name-epoch:version-release.arch`
// line into a Package. Returns ok=false if the line does not contain an epoch
// colon (not a valid repoquery row) or cannot be split into name + nvra.
func parseDnf5RepoqueryLine(line string) (Package, bool) {
	// Drop the arch suffix: split on the LAST dot.
	nvra, _, ok := cutLast(line, ".")
	if !ok || nvra == "" {
		return Package{}, false
	}
	// nvra is now `name-epoch:version-release`. Find the epoch colon.
	epochIdx := strings.Index(nvra, ":")
	if epochIdx < 0 {
		return Package{}, false
	}
	// The name is everything before the `-` that precedes the epoch. Search
	// backwards from the colon for the `-` separating name from the epoch
	// segment.
	dashIdx := strings.LastIndex(nvra[:epochIdx], "-")
	if dashIdx < 0 {
		return Package{}, false
	}
	name := nvra[:dashIdx]
	// version-release is everything after the epoch colon.
	versionRelease := nvra[epochIdx+1:]
	if name == "" || versionRelease == "" {
		return Package{}, false
	}
	return Package{Name: name, Version: versionRelease}, true
}

// cutLast splits s at the LAST occurrence of sep into before and after. ok is
// false if sep is not present.
func cutLast(s, sep string) (before string, after string, ok bool) {
	idx := strings.LastIndex(s, sep)
	if idx < 0 {
		return s, "", false
	}
	return s[:idx], s[idx+len(sep):], true
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
	Manager         packages.Manager
	Bin             string
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