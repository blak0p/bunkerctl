package podman

import (
	"context"
	"errors"
	"testing"
)

// FakeRunner is a test double implementing Runner. It is NOT exported; it
// exists only to drive orchestrator/cmd tests against a controlled Podman.
type FakeRunner struct {
	VersionStr string
	Err        error

	// Calls records invocations for assertion.
	Calls []string
}

// Compile-time guarantee that FakeRunner satisfies Runner.
var _ Runner = (*FakeRunner)(nil)

func (f *FakeRunner) Version(ctx context.Context) (string, error) {
	f.Calls = append(f.Calls, "Version")
	return f.VersionStr, f.Err
}

func (f *FakeRunner) List(ctx context.Context, all bool) ([]Container, error) {
	return nil, errors.New("not implemented")
}

func (f *FakeRunner) Inspect(ctx context.Context, id string) (InspectResult, error) {
	return InspectResult{}, errors.New("not implemented")
}

func (f *FakeRunner) Commit(ctx context.Context, id, image string) error {
	return errors.New("not implemented")
}

func (f *FakeRunner) Save(ctx context.Context, image, format, dest string) error {
	return errors.New("not implemented")
}

func (f *FakeRunner) Exec(ctx context.Context, id string, cmd []string) (string, error) {
	return "", errors.New("not implemented")
}

// TestFakeRunnerImplementsRunner is a RED test: it references the Runner
// interface and FakeRunner type which do not exist yet.
func TestFakeRunnerImplementsRunner(t *testing.T) {
	var r Runner = &FakeRunner{VersionStr: "podman version 5.0.0"}
	got, err := r.Version(context.Background())
	if err != nil {
		t.Fatalf("FakeRunner.Version error: %v", err)
	}
	if got != "podman version 5.0.0" {
		t.Errorf("FakeRunner.Version = %q, want %q", got, "podman version 5.0.0")
	}
}

// TestFakeRunner_Version_Triangulate uses a different fake version string to
// force real logic (the fake returns whatever it is configured with, not a
// hardcoded value).
func TestFakeRunner_Version_Triangulate(t *testing.T) {
	var r Runner = &FakeRunner{VersionStr: "podman version 4.5.1", Err: nil}
	got, err := r.Version(context.Background())
	if err != nil {
		t.Fatalf("FakeRunner.Version error: %v", err)
	}
	if got != "podman version 4.5.1" {
		t.Errorf("FakeRunner.Version = %q, want %q", got, "podman version 4.5.1")
	}
}

// fakeBackend is a test double for the execBackend interface used by CLIRunner.
type fakeBackend struct {
	out string
	err error
}

func (fb *fakeBackend) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	if fb.err != nil {
		return nil, fb.err
	}
	return []byte(fb.out), nil
}

// TestCLIRunner_Version_HappyPath drives the real CLIRunner.Version against a
// fake exec backend that returns a known version string.
func TestCLIRunner_Version_HappyPath(t *testing.T) {
	r := &CLIRunner{bin: "podman", exec: &fakeBackend{out: "podman version 5.3.1"}}
	got, err := r.Version(context.Background())
	if err != nil {
		t.Fatalf("CLIRunner.Version error: %v", err)
	}
	if got != "podman version 5.3.1" {
		t.Errorf("CLIRunner.Version = %q, want %q", got, "podman version 5.3.1")
	}
}

// TestCLIRunner_Version_EngineUnavailable triangulates: when the exec backend
// returns an error, Version MUST return ErrEngineUnavailable (not the raw
// exec error, not nil).
func TestCLIRunner_Version_EngineUnavailable(t *testing.T) {
	r := &CLIRunner{bin: "podman", exec: &fakeBackend{err: errors.New("exec: not found")}}
	got, err := r.Version(context.Background())
	if !errors.Is(err, ErrEngineUnavailable) {
		t.Errorf("CLIRunner.Version err = %v, want ErrEngineUnavailable", err)
	}
	if got != "" {
		t.Errorf("CLIRunner.Version string = %q, want empty on error", got)
	}
}

// TestCLIRunner_Version_NonZeroExit triangulates: non-zero exit (represented
// as an error from the backend) also yields ErrEngineUnavailable.
func TestCLIRunner_Version_NonZeroExit(t *testing.T) {
	r := &CLIRunner{bin: "podman", exec: &fakeBackend{err: errors.New("exit status 127")}}
	_, err := r.Version(context.Background())
	if !errors.Is(err, ErrEngineUnavailable) {
		t.Errorf("CLIRunner.Version non-zero exit err = %v, want ErrEngineUnavailable", err)
	}
}