package podman

import "context"

// FakeRunner is an exported test double implementing Runner. It lives in a
// non-test file so that cross-package tests (e.g. cmd) can drive the backup
// command against a controlled Podman without a real engine. It is not
// intended for production use.
type FakeRunner struct {
	VersionStr string
	Err        error

	// ListResult is returned by List when ListErr is nil.
	ListResult []Container
	ListErr    error

	// InspectResult is returned by Inspect when InspectErr is nil.
	InspectResult InspectResult
	InspectErr    error

	// ExecFn, when non-nil, is called by Exec and its return values are
	// passed through. This lets cmd-level tests drive package detection and
	// listing through the Runner seam with canned output keyed by the joined
	// command. When nil, Exec returns ("", f.Err).
	ExecFn func(ctx context.Context, id string, cmd []string) (string, error)

	// InspectRawFn, when non-nil, is called by InspectRaw. When nil,
	// InspectRaw returns (f.InspectRawResult, f.InspectRawErr).
	InspectRawFn     func(ctx context.Context, id string) (string, error)
	InspectRawResult string
	InspectRawErr    error

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
	f.Calls = append(f.Calls, "List")
	return f.ListResult, f.ListErr
}

func (f *FakeRunner) Inspect(ctx context.Context, id string) (InspectResult, error) {
	f.Calls = append(f.Calls, "Inspect:"+id)
	return f.InspectResult, f.InspectErr
}

func (f *FakeRunner) Exec(ctx context.Context, id string, cmd []string) (string, error) {
	f.Calls = append(f.Calls, "Exec:"+id)
	if f.ExecFn != nil {
		return f.ExecFn(ctx, id, cmd)
	}
	return "", f.Err
}

func (f *FakeRunner) InspectRaw(ctx context.Context, id string) (string, error) {
	f.Calls = append(f.Calls, "InspectRaw:"+id)
	if f.InspectRawFn != nil {
		return f.InspectRawFn(ctx, id)
	}
	return f.InspectRawResult, f.InspectRawErr
}