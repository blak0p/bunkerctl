package podman

import "context"

// CreateCall records a single FakeRunner.Create invocation.
type CreateCall struct {
	Image string
	Name  string
	Env   []string
}

// CpCall records a single FakeRunner.Cp invocation.
type CpCall struct {
	Src string
	Dst string
}

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

	// CreateErr, when non-nil, is returned by Create. CreateCalls records
	// each invocation with its image, name, and env args.
	CreateErr  error
	CreateCalls []CreateCall

	// StartErr, when non-nil, is returned by Start. StartCalls records each
	// container name passed.
	StartErr  error
	StartCalls []string

	// CpErr, when non-nil, is returned by Cp. CpCalls records each (src,dst)
	// pair passed.
	CpErr  error
	CpCalls []CpCall

	// PullErr, when non-nil, is returned by Pull. PullCalls records each
	// image passed.
	PullErr  error
	PullCalls []string

	// RemoveErr, when non-nil, is returned by Remove. RemoveCalls records
	// each container name passed.
	RemoveErr  error
	RemoveCalls []string

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

// Create records the call and returns the configured error (nil by default).
func (f *FakeRunner) Create(ctx context.Context, image, name string, env []string) error {
	f.Calls = append(f.Calls, "Create:"+name)
	envCopy := append([]string{}, env...)
	f.CreateCalls = append(f.CreateCalls, CreateCall{Image: image, Name: name, Env: envCopy})
	return f.CreateErr
}

// Start records the container name and returns the configured error.
func (f *FakeRunner) Start(ctx context.Context, name string) error {
	f.Calls = append(f.Calls, "Start:"+name)
	f.StartCalls = append(f.StartCalls, name)
	return f.StartErr
}

// Cp records the (src,dst) pair and returns the configured error.
func (f *FakeRunner) Cp(ctx context.Context, src, dst string) error {
	f.Calls = append(f.Calls, "Cp:"+src+" "+dst)
	f.CpCalls = append(f.CpCalls, CpCall{Src: src, Dst: dst})
	return f.CpErr
}

// Pull records the image and returns the configured error.
func (f *FakeRunner) Pull(ctx context.Context, image string) error {
	f.Calls = append(f.Calls, "Pull:"+image)
	f.PullCalls = append(f.PullCalls, image)
	return f.PullErr
}

// Remove records the container name and returns the configured error.
func (f *FakeRunner) Remove(ctx context.Context, name string) error {
	f.Calls = append(f.Calls, "Remove:"+name)
	f.RemoveCalls = append(f.RemoveCalls, name)
	return f.RemoveErr
}