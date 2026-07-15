// Package podman wraps the Podman CLI behind an interface for testability.
//
// All Podman operations go through Runner. The real implementation, CLIRunner,
// shells out to the podman binary via os/exec; tests inject a fake execBackend
// to avoid needing a real podman on PATH.
package podman

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// ErrEngineUnavailable is returned when the Podman engine cannot be reached
// (binary missing, non-zero exit from `podman --version`, etc.).
var ErrEngineUnavailable = errors.New("podman engine unavailable")

// Container is a minimal description of a Podman container, returned by
// Runner.List. Fields are filled in by later slices; this PR only declares
// the shape.
type Container struct {
	ID     string
	Names  []string
	Image  string
	Status string
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

// List is a placeholder implemented in PR 2.
func (r *CLIRunner) List(ctx context.Context, all bool) ([]Container, error) {
	return nil, errors.New("not implemented")
}

// Inspect is a placeholder implemented in a later PR.
func (r *CLIRunner) Inspect(ctx context.Context, id string) (InspectResult, error) {
	return InspectResult{}, errors.New("not implemented")
}

// Commit is a placeholder implemented in a later PR.
func (r *CLIRunner) Commit(ctx context.Context, id, image string) error {
	return errors.New("not implemented")
}

// Save is a placeholder implemented in a later PR.
func (r *CLIRunner) Save(ctx context.Context, image, format, dest string) error {
	return errors.New("not implemented")
}

// Exec is a placeholder implemented in a later PR.
func (r *CLIRunner) Exec(ctx context.Context, id string, cmd []string) (string, error) {
	return "", errors.New("not implemented")
}