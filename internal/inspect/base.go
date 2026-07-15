package inspect

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blak0p/bunkerctl/internal/podman"
)

// BaseInfo holds the detected distro id and version (REQ-YAML-3).
type BaseInfo struct {
	Distro  string
	Version string
}

// ErrUnsupportedDistro is returned by DetectBase when the container is not
// Fedora. This SDD supports only Fedora (dnf/dnf5); other distros arrive in
// SDD-D (REQ-YAML-3 scenario: non-Fedora rejected).
var ErrUnsupportedDistro = errors.New("unsupported distro: only Fedora (dnf/dnf5) is supported in this version")

// ErrNoOSRelease is returned when /etc/os-release cannot be read or does not
// contain the required ID and VERSION_ID fields.
var ErrNoOSRelease = errors.New("could not parse /etc/os-release: missing ID or VERSION_ID")

// DetectBase reads /etc/os-release inside the container via `cat /etc/os-release`
// and parses the ID and VERSION_ID fields. It returns ErrUnsupportedDistro when
// ID is not "fedora" (REQ-YAML-3). Quoted values (ID="fedora") are unquoted.
func DetectBase(ctx context.Context, runner podman.Runner, name string) (BaseInfo, error) {
	out, err := runner.Exec(ctx, name, []string{"cat", "/etc/os-release"})
	if err != nil {
		return BaseInfo{}, fmt.Errorf("reading /etc/os-release: %w", err)
	}
	distro, version, perr := parseOSRelease(out)
	if perr != nil {
		return BaseInfo{}, perr
	}
	if distro != "fedora" {
		return BaseInfo{}, fmt.Errorf("%w: distro=%q", ErrUnsupportedDistro, distro)
	}
	return BaseInfo{Distro: distro, Version: version}, nil
}

// parseOSRelease extracts the ID and VERSION_ID values from an os-release
// payload. Both fields MUST be present. Quoted values are unquoted.
func parseOSRelease(content string) (distro, version string, err error) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.Trim(val, `"`)
		switch strings.TrimSpace(key) {
		case "ID":
			distro = val
		case "VERSION_ID":
			version = val
		}
	}
	if distro == "" || version == "" {
		return "", "", ErrNoOSRelease
	}
	return distro, version, nil
}