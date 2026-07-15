// Package podman wraps the Podman CLI behind an interface for testability.
//
// All Podman operations go through Runner. The real implementation, CLIRunner,
// shells out to the podman binary via os/exec; tests inject a fake execBackend
// to avoid needing a real podman on PATH.
package podman

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrEngineUnavailable is returned when the Podman engine cannot be reached
// (binary missing, non-zero exit from `podman --version`, etc.).
var ErrEngineUnavailable = errors.New("podman engine unavailable")

// ErrInvalidContainerName is returned when a container name or ID passed to a
// Runner method fails validation (empty, contains shell metacharacters, or
// exceeds the Podman length limit).
var ErrInvalidContainerName = errors.New("invalid container name")

// ErrContainerNotFound is returned by Inspect when the container engine
// reports the requested container does not exist.
var ErrContainerNotFound = errors.New("container not found")

// maxContainerNameLen is the Podman-imposed upper bound on container name/ID
// length. Names longer than this are rejected defensively.
const maxContainerNameLen = 256

// ValidateContainerName rejects names that could enable command injection or
// exceed the engine's length limit. Container names/IDs originate from CLI
// args and `podman ps` output; this is the defense-in-depth seam that prevents
// a malicious or malformed value from being interpolated into a podman call.
// It is exported so command-layer code can validate at its own boundary
// before touching the engine.
func ValidateContainerName(name string) error {
	return validateContainerName(name)
}

// validateContainerName rejects names that could enable command injection or
// exceed the engine's length limit. Container names/IDs originate from CLI
// args and `podman ps` output; this is the defense-in-depth seam that prevents
// a malicious or malformed value from being interpolated into a podman call.
func validateContainerName(name string) error {
	if name == "" {
		return ErrInvalidContainerName
	}
	if len(name) > maxContainerNameLen {
		return ErrInvalidContainerName
	}
	for _, c := range name {
		switch c {
		case ';', '&', '|', '`', '$', '(', ')', '<', '>', '\n', '\r':
			return ErrInvalidContainerName
		}
	}
	return nil
}

// Container is a minimal description of a Podman container, returned by
// Runner.List. Fields are filled in by later slices; this PR only declares
// the shape.
type Container struct {
	ID     string   `json:"Id"`
	Names  []string `json:"Names"`
	Image  string   `json:"Image"`
	Status string   `json:"Status"`
}

// InspectResult is a minimal description of `podman inspect` output. Later
// slices expand it; this PR only declares the shape so the interface compiles.
type InspectResult struct {
	ID    string
	Image string
}

// Runner abstracts the Podman operations bunkerctl needs across the backup
// chain. Only Version is implemented in PR 1; the remaining methods are
// placeholders that return "not implemented" and are filled in by later PRs.
type Runner interface {
	// Version runs `podman --version` and returns the trimmed output, or
	// ErrEngineUnavailable when the engine is missing or errors.
	Version(ctx context.Context) (string, error)
	// List runs `podman ps` and returns the containers. Implemented in PR 2.
	List(ctx context.Context, all bool) ([]Container, error)
	// Inspect runs `podman inspect <id>`. Implemented in a later PR.
	Inspect(ctx context.Context, id string) (InspectResult, error)
	// Commit runs `podman commit <id> <image>`. Implemented in a later PR.
	Commit(ctx context.Context, id, image string) error
	// Save runs `podman save`. Implemented in a later PR.
	Save(ctx context.Context, image, format, dest string) error
	// Exec runs `podman exec <id> <cmd>`. Implemented in a later PR.
	Exec(ctx context.Context, id string, cmd []string) (string, error)
}

// execBackend is the seam between CLIRunner and os/exec. It exists so tests
// can inject a fake instead of spawning a real podman process.
type execBackend interface {
	CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error)
}

// realExec is the default execBackend, delegating to os/exec.CommandContext.
type realExec struct{}

func (realExec) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// CLIRunner is the production Runner. It shells out to the podman binary
// through an execBackend (os/exec by default, fakes in tests).
type CLIRunner struct {
	bin  string
	exec execBackend
}

// NewCLIRunner returns a CLIRunner targeting the given podman binary. Pass an
// empty string to use the default "podman".
func NewCLIRunner(bin string) *CLIRunner {
	if bin == "" {
		bin = "podman"
	}
	return &CLIRunner{bin: bin, exec: realExec{}}
}

// Version runs `podman --version`. It returns the trimmed output on success,
// or ErrEngineUnavailable if the binary cannot be found or exits non-zero.
func (r *CLIRunner) Version(ctx context.Context) (string, error) {
	out, err := r.exec.CombinedOutput(ctx, r.bin, "--version")
	if err != nil {
		return "", ErrEngineUnavailable
	}
	return strings.TrimSpace(string(out)), nil
}

// List runs `podman ps [--all] --format json` and parses the JSON array of
// containers. An empty result returns a non-nil empty slice so callers can
// range over it without nil checks. Malformed JSON surfaces as a parse error.
func (r *CLIRunner) List(ctx context.Context, all bool) ([]Container, error) {
	args := []string{"ps", "--format", "json"}
	if all {
		args = append(args, "-a")
	}
	out, err := r.exec.CombinedOutput(ctx, r.bin, args...)
	if err != nil {
		return nil, err
	}
	return parseContainerList(out)
}

// parseContainerList decodes the podman ps JSON payload into []Container. An
// empty payload ("[]\n" or whitespace) yields a non-nil empty slice.
func parseContainerList(raw []byte) ([]Container, error) {
	containers := []Container{}
	if err := json.Unmarshal(raw, &containers); err != nil {
		return nil, fmt.Errorf("parsing podman ps output: %w", err)
	}
	return containers, nil
}

// Inspect runs `podman inspect <id>`. PR 2 wires the name validation seam; the
// full parse of inspect output arrives in a later PR, so on a successful exec
// it returns a minimal InspectResult populated from the raw JSON.
func (r *CLIRunner) Inspect(ctx context.Context, id string) (InspectResult, error) {
	if err := validateContainerName(id); err != nil {
		return InspectResult{}, err
	}
	out, err := r.exec.CombinedOutput(ctx, r.bin, "inspect", "--format", "json", id)
	if err != nil {
		return InspectResult{}, err
	}
	return InspectResult{ID: id, Image: strings.TrimSpace(string(out))}, nil
}

// Commit runs `podman commit <id> <image>`. PR 2 wires the name validation
// seam; the full commit-pipeline behavior arrives in a later PR.
func (r *CLIRunner) Commit(ctx context.Context, id, image string) error {
	if err := validateContainerName(id); err != nil {
		return err
	}
	if err := validateContainerName(image); err != nil {
		return err
	}
	_, err := r.exec.CombinedOutput(ctx, r.bin, "commit", id, image)
	return err
}

// Save runs `podman save <image> -o <dest> -f <format>`. PR 2 wires the name
// validation seam; the full save-pipeline behavior arrives in a later PR.
func (r *CLIRunner) Save(ctx context.Context, image, format, dest string) error {
	if err := validateContainerName(image); err != nil {
		return err
	}
	_, err := r.exec.CombinedOutput(ctx, r.bin, "save", image, "-o", dest, "-f", format)
	return err
}

// Exec runs `podman exec <id> <cmd...>`. PR 2 wires the name validation seam;
// the full exec-pipeline behavior arrives in a later PR.
func (r *CLIRunner) Exec(ctx context.Context, id string, cmd []string) (string, error) {
	if err := validateContainerName(id); err != nil {
		return "", err
	}
	args := append([]string{"exec", id}, cmd...)
	out, err := r.exec.CombinedOutput(ctx, r.bin, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}