// Package manifest defines the bunker.yaml schema (format_version 1) and the
// helpers to marshal, unmarshal, and validate it. The YAML manifest IS the
// backup: it records the container's user, base distro, packages (with per-
// package name+version), file-copy config (including the default ignore list),
// custom environment, and verify policy. There is no image export; the .bunker
// archive holds this YAML plus a files/ tree copied from inside the container.
package manifest

import (
	"bytes"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/blak0p/bunkerctl/internal/ignore"
)

// currentFormatVersion is the only schema generation this package produces and
// accepts. Readers MUST reject any other major version (REQ-YAML-1 scenario:
// "unknown major version is rejected by a future reader").
const currentFormatVersion = 1

// BunkerManifest is the top-level bunker.yaml structure (format_version 1).
// Every field except Name/Created is REQUIRED and validated by Validate.
type BunkerManifest struct {
	FormatVersion int                  `yaml:"format_version"`
	Name          string               `yaml:"name"`
	Created       string               `yaml:"created"` // YYYY-MM-DD
	User          UserInfo             `yaml:"user"`
	Base          BaseInfo             `yaml:"base"`
	Packages      map[string][]Package `yaml:"packages"`
	Files         FilesConfig          `yaml:"files"`
	Custom        CustomConfig         `yaml:"custom"`
	Verify        VerifyConfig         `yaml:"verify"`
}

// UserInfo holds the detected user info from inside the container. The uid/gid
// come from the container's /etc/passwd, never from the host (REQ-YAML-2,
// REQ-DETECT-3).
type UserInfo struct {
	Name string `yaml:"name"`
	UID  int    `yaml:"uid"`
	GID  int    `yaml:"gid"`
	Home string `yaml:"home"`
}

// BaseInfo holds the detected distro id and version (REQ-YAML-3).
type BaseInfo struct {
	Distro  string `yaml:"distro"`
	Version string `yaml:"version"`
}

// Package is a single installed package with name and version. Both fields are
// REQUIRED per entry (REQ-YAML-5).
type Package struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

// FilesConfig configures the container-side file copy step (REQ-YAML-6).
type FilesConfig struct {
	Copy    string   `yaml:"copy"`     // MUST be "auto" in v1
	CopyEtc []string `yaml:"copy_etc"` // extra absolute container paths
	Ignore  []string `yaml:"ignore"`   // patterns matched during traversal
}

// CustomConfig holds the captured container environment (REQ-YAML-7). The
// environment map MAY be empty but the key MUST be present.
type CustomConfig struct {
	Environment map[string]string `yaml:"environment"`
}

// VerifyConfig holds the restore-verification policy (REQ-YAML-7). Auto defaults
// to true in v1; SDD-B (restore) reads it.
type VerifyConfig struct {
	Auto bool `yaml:"auto"`
}

// ErrInvalidFormatVersion is returned when format_version != 1 (REQ-YAML-1).
var ErrInvalidFormatVersion = errors.New("unsupported format_version: must be 1")

// ErrMissingRequiredField is returned when a required section or field is absent
// or empty. The wrapped message names the missing field.
var ErrMissingRequiredField = errors.New("missing required field in bunker.yaml")

// Validate checks that all required fields are present and format_version == 1.
// It is called by Unmarshal after decoding; callers that build a manifest in
// code can also call it directly before Marshal.
func (m *BunkerManifest) Validate() error {
	if m.FormatVersion != currentFormatVersion {
		return fmt.Errorf("%w: got %d", ErrInvalidFormatVersion, m.FormatVersion)
	}
	if m.User.Name == "" {
		return fmt.Errorf("%w: user.name", ErrMissingRequiredField)
	}
	if m.User.Home == "" {
		return fmt.Errorf("%w: user.home", ErrMissingRequiredField)
	}
	if m.Base.Distro == "" {
		return fmt.Errorf("%w: base.distro", ErrMissingRequiredField)
	}
	if m.Base.Version == "" {
		return fmt.Errorf("%w: base.version", ErrMissingRequiredField)
	}
	if m.Packages == nil {
		return fmt.Errorf("%w: packages", ErrMissingRequiredField)
	}
	// REQ-YAML-5: each package entry MUST have name and version.
	for mgr, pkgs := range m.Packages {
		for i, p := range pkgs {
			if p.Name == "" {
				return fmt.Errorf("%w: packages.%s[%d].name", ErrMissingRequiredField, mgr, i)
			}
			if p.Version == "" {
				return fmt.Errorf("%w: packages.%s[%d].version", ErrMissingRequiredField, mgr, i)
			}
		}
	}
	if m.Files.Copy != "auto" {
		return fmt.Errorf("%w: files.copy (must be \"auto\" in v1)", ErrMissingRequiredField)
	}
	if len(m.Files.Ignore) == 0 {
		return fmt.Errorf("%w: files.ignore", ErrMissingRequiredField)
	}
	if m.Custom.Environment == nil {
		return fmt.Errorf("%w: custom.environment", ErrMissingRequiredField)
	}
	return nil
}

// Marshal encodes m as YAML. It validates first so a malformed manifest is never
// serialized. The output is deterministic for a given struct value (yaml.v3
// sorts map keys; packages is a map so its manager keys are sorted, which is
// acceptable for a machine-generated manifest).
func Marshal(m *BunkerManifest) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("marshaling bunker.yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("closing yaml encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// Unmarshal decodes YAML into a BunkerManifest and validates it. A YAML that
// decodes cleanly but fails Validate (e.g. format_version: 2) returns the
// validation error.
func Unmarshal(data []byte) (*BunkerManifest, error) {
	var m BunkerManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing bunker.yaml: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// DefaultIgnoreList returns a copy of the default ignore patterns (REQ-COPY-4).
// It delegates to ignore.DefaultPatterns so the default list has a single
// source of truth; callers may freely mutate the returned slice without
// affecting the shared default.
func DefaultIgnoreList() []string {
	return ignore.DefaultPatterns()
}