package podman

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// FakeRunner is defined in fake.go (exported, non-test file) so that
// cross-package tests (cmd) can drive the backup command against a controlled
// Podman. The behavioral tests below verify the double works as advertised.

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

// TestFakeRunner_List_ReturnsConfigured verifies the extended FakeRunner.List
// returns the configured slice rather than "not implemented", enabling cmd
// tests to drive selection against controlled container sets.
func TestFakeRunner_List_ReturnsConfigured(t *testing.T) {
	want := []Container{{ID: "x1", Names: []string{"c1"}, Image: "img", Status: "running"}}
	r := &FakeRunner{ListResult: want}
	got, err := r.List(context.Background(), true)
	if err != nil {
		t.Fatalf("FakeRunner.List error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "x1" {
		t.Errorf("FakeRunner.List = %+v, want %+v", got, want)
	}
}

// TestFakeRunner_Inspect_ReturnsConfigured triangulates the Inspect extension.
func TestFakeRunner_Inspect_ReturnsConfigured(t *testing.T) {
	r := &FakeRunner{InspectResult: InspectResult{ID: "mybunker", Image: "fedora:40"}}
	got, err := r.Inspect(context.Background(), "mybunker")
	if err != nil {
		t.Fatalf("FakeRunner.Inspect error: %v", err)
	}
	if got.ID != "mybunker" || got.Image != "fedora:40" {
		t.Errorf("FakeRunner.Inspect = %+v, want {ID:mybunker Image:fedora:40}", got)
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

// TestCLIRunner_List_HappyPath drives the real CLIRunner.List against a fake
// exec backend returning a known podman ps JSON payload and asserts the parsed
// slice.
func TestCLIRunner_List_HappyPath(t *testing.T) {
	payload := `[{"Id":"abc123","Names":["mybunker"],"Image":"fedora:40","Status":"running"}]`
	r := &CLIRunner{bin: "podman", exec: &fakeBackend{out: payload}}
	got, err := r.List(context.Background(), true)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List len = %d, want 1", len(got))
	}
	if got[0].ID != "abc123" {
		t.Errorf("List[0].ID = %q, want %q", got[0].ID, "abc123")
	}
	if len(got[0].Names) != 1 || got[0].Names[0] != "mybunker" {
		t.Errorf("List[0].Names = %v, want [mybunker]", got[0].Names)
	}
	if got[0].Image != "fedora:40" {
		t.Errorf("List[0].Image = %q, want %q", got[0].Image, "fedora:40")
	}
}

// TestCLIRunner_List_Empty triangulates: an empty JSON array ("[]") MUST
// return a non-nil empty slice, not nil, so callers can range without nil
// checks.
func TestCLIRunner_List_Empty(t *testing.T) {
	r := &CLIRunner{bin: "podman", exec: &fakeBackend{out: "[]"}}
	got, err := r.List(context.Background(), true)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if got == nil {
		t.Errorf("List([]) = nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("List([]) len = %d, want 0", len(got))
	}
}

// TestCLIRunner_List_MalformedJSON triangulates: malformed JSON MUST surface
// as a parse error, not a silent empty slice or a panic.
func TestCLIRunner_List_MalformedJSON(t *testing.T) {
	r := &CLIRunner{bin: "podman", exec: &fakeBackend{out: "{not-json"}}
	_, err := r.List(context.Background(), true)
	if err == nil {
		t.Errorf("List(malformed) err = nil, want parse error")
	}
}

// TestCLIRunner_List_MultipleContainers triangulates with a multi-element
// payload to prove the parser generalizes beyond a single container.
func TestCLIRunner_List_MultipleContainers(t *testing.T) {
	payload := `[
		{"Id":"aaa","Names":["c1"],"Image":"img1","Status":"running"},
		{"Id":"bbb","Names":["c2"],"Image":"img2","Status":"exited"}
	]`
	r := &CLIRunner{bin: "podman", exec: &fakeBackend{out: payload}}
	got, err := r.List(context.Background(), true)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List len = %d, want 2", len(got))
	}
	if got[1].ID != "bbb" || got[1].Names[0] != "c2" {
		t.Errorf("List[1] = %+v, want ID=bbb Names=[c2]", got[1])
	}
}

// --- backup-format-v1 PR 2: InspectRaw (additive, non-breaking) ---

// argsBackend records the args passed to CombinedOutput so tests can assert the
// exact podman command line.
type argsBackend struct {
	out  string
	err  error
	args []string
	name string
}

func (a *argsBackend) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	a.name = name
	a.args = append([]string{}, args...)
	if a.err != nil {
		return nil, a.err
	}
	return []byte(a.out), nil
}

// TestCLIRunner_InspectRaw_RunsPodmanInspect asserts InspectRaw runs
// `podman inspect <id>` and returns the trimmed raw JSON.
func TestCLIRunner_InspectRaw_RunsPodmanInspect(t *testing.T) {
	b := &argsBackend{out: `[{"Id":"abc","Image":"fedora:45"}]`}
	r := &CLIRunner{bin: "podman", exec: b}
	got, err := r.InspectRaw(context.Background(), "bunker")
	if err != nil {
		t.Fatalf("InspectRaw error: %v", err)
	}
	wantArgs := []string{"inspect", "bunker"}
	if len(b.args) != len(wantArgs) {
		t.Fatalf("InspectRaw args = %v, want %v", b.args, wantArgs)
	}
	for i, w := range wantArgs {
		if b.args[i] != w {
			t.Errorf("InspectRaw args[%d] = %q, want %q", i, b.args[i], w)
		}
	}
	if got != `[{"Id":"abc","Image":"fedora:45"}]` {
		t.Errorf("InspectRaw output = %q, want raw JSON", got)
	}
}

// TestCLIRunner_InspectRaw_RejectsInvalidName asserts name validation still
// fires before the exec call.
func TestCLIRunner_InspectRaw_RejectsInvalidName(t *testing.T) {
	b := &argsBackend{}
	r := &CLIRunner{bin: "podman", exec: b}
	_, err := r.InspectRaw(context.Background(), "bad;name")
	if !errors.Is(err, ErrInvalidContainerName) {
		t.Errorf("InspectRaw(bad;name) err = %v, want ErrInvalidContainerName", err)
	}
	if b.args != nil {
		t.Errorf("InspectRaw(bad;name) spawned podman; want no exec call")
	}
}

// TestFakeRunner_InspectRaw_ReturnsConfigured triangulates the fake.
func TestFakeRunner_InspectRaw_ReturnsConfigured(t *testing.T) {
	r := &FakeRunner{InspectRawResult: `[{"Id":"xyz"}]`}
	got, err := r.InspectRaw(context.Background(), "bunker")
	if err != nil {
		t.Fatalf("FakeRunner.InspectRaw error: %v", err)
	}
	if got != `[{"Id":"xyz"}]` {
		t.Errorf("FakeRunner.InspectRaw = %q, want configured JSON", got)
	}
}

// TestFakeRunner_InspectRaw_ExecFnPassthrough triangulates the Fn override path.
func TestFakeRunner_InspectRaw_ExecFnPassthrough(t *testing.T) {
	called := false
	r := &FakeRunner{
		InspectRawFn: func(ctx context.Context, id string) (string, error) {
			called = true
			if id != "bunker" {
				t.Errorf("InspectRawFn id = %q, want bunker", id)
			}
			return "custom", nil
		},
	}
	got, _ := r.InspectRaw(context.Background(), "bunker")
	if !called {
		t.Errorf("InspectRawFn not invoked")
	}
	if got != "custom" {
		t.Errorf("InspectRaw = %q, want custom", got)
	}
}

// --- restore-core PR 1: Create, Start, Cp, Pull, Remove ---
//
// Strict TDD: these tests are written FIRST and reference production methods
// that do not exist yet on the Runner interface / CLIRunner / FakeRunner. The
// expected RED state is a COMPILE ERROR (undefined method), not a runtime
// assertion failure. GREEN adds the methods + doubles.

// TestCLIRunner_Create_RunsPodmanCreateWithEnv is the happy-path RED anchor for
// Create: it MUST run `podman create --env <v>... --name <name> <image>` and
// surface the env vars in the exact order given.
func TestCLIRunner_Create_RunsPodmanCreateWithEnv(t *testing.T) {
	b := &argsBackend{out: ""}
	r := &CLIRunner{bin: "podman", exec: b}
	if err := r.Create(context.Background(), "fedora:45", "mybox", []string{"EDITOR=nvim"}); err != nil {
		t.Fatalf("Create error: %v", err)
	}
	wantArgs := []string{"create", "--env", "EDITOR=nvim", "--name", "mybox", "fedora:45"}
	if len(b.args) != len(wantArgs) {
		t.Fatalf("Create args = %v, want %v", b.args, wantArgs)
	}
	for i, w := range wantArgs {
		if b.args[i] != w {
			t.Errorf("Create args[%d] = %q, want %q", i, b.args[i], w)
		}
	}
}

// TestCLIRunner_Create_NoEnvOmitsEnvFlag triangulates: when env is empty, no
// --env flag is emitted (spec REQ-RST-4: empty env creates with no vars).
func TestCLIRunner_Create_NoEnvOmitsEnvFlag(t *testing.T) {
	b := &argsBackend{out: ""}
	r := &CLIRunner{bin: "podman", exec: b}
	if err := r.Create(context.Background(), "fedora:45", "mybox", nil); err != nil {
		t.Fatalf("Create error: %v", err)
	}
	for _, a := range b.args {
		if a == "--env" {
			t.Errorf("Create(nil env) emitted --env; want no env flag, args=%v", b.args)
		}
	}
	wantArgs := []string{"create", "--name", "mybox", "fedora:45"}
	if len(b.args) != len(wantArgs) {
		t.Fatalf("Create(no env) args = %v, want %v", b.args, wantArgs)
	}
	for i, w := range wantArgs {
		if b.args[i] != w {
			t.Errorf("Create(no env) args[%d] = %q, want %q", i, b.args[i], w)
		}
	}
}

// TestCLIRunner_Create_MultipleEnvFlags triangulates: multiple env vars each
// get their own --env flag, in order.
func TestCLIRunner_Create_MultipleEnvFlags(t *testing.T) {
	b := &argsBackend{out: ""}
	r := &CLIRunner{bin: "podman", exec: b}
	if err := r.Create(context.Background(), "fedora:45", "mybox", []string{"EDITOR=nvim", "TERM=xterm"}); err != nil {
		t.Fatalf("Create error: %v", err)
	}
	wantArgs := []string{"create", "--env", "EDITOR=nvim", "--env", "TERM=xterm", "--name", "mybox", "fedora:45"}
	if len(b.args) != len(wantArgs) {
		t.Fatalf("Create(multi env) args = %v, want %v", b.args, wantArgs)
	}
	for i, w := range wantArgs {
		if b.args[i] != w {
			t.Errorf("Create(multi env) args[%d] = %q, want %q", i, b.args[i], w)
		}
	}
}

// TestCLIRunner_Create_RejectsInvalidName asserts name validation fires before
// the exec call (defense-in-depth).
func TestCLIRunner_Create_RejectsInvalidName(t *testing.T) {
	b := &argsBackend{}
	r := &CLIRunner{bin: "podman", exec: b}
	if err := r.Create(context.Background(), "fedora:45", "bad;name", nil); !errors.Is(err, ErrInvalidContainerName) {
		t.Errorf("Create(bad;name) err = %v, want ErrInvalidContainerName", err)
	}
	if b.args != nil {
		t.Errorf("Create(bad;name) spawned podman; want no exec call")
	}
}

// TestCLIRunner_Create_PropagatesExecError asserts that when the exec backend
// returns an error (e.g. name collision), Create surfaces it wrapped with
// method context (spec: Container name collision fails).
func TestCLIRunner_Create_PropagatesExecError(t *testing.T) {
	b := &argsBackend{err: errors.New("exit status 125: name already in use")}
	r := &CLIRunner{bin: "podman", exec: b}
	err := r.Create(context.Background(), "fedora:45", "mybox", nil)
	if err == nil {
		t.Fatalf("Create(colliding name) err = nil, want error")
	}
	if !strings.Contains(err.Error(), "create") {
		t.Errorf("Create error = %v, want wrapped error mentioning create", err)
	}
}

// TestCLIRunner_Start_RunsPodmanStart is the happy-path RED anchor for Start:
// it MUST run `podman start <name>`.
func TestCLIRunner_Start_RunsPodmanStart(t *testing.T) {
	b := &argsBackend{out: ""}
	r := &CLIRunner{bin: "podman", exec: b}
	if err := r.Start(context.Background(), "mybox"); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	wantArgs := []string{"start", "mybox"}
	if len(b.args) != len(wantArgs) {
		t.Fatalf("Start args = %v, want %v", b.args, wantArgs)
	}
	for i, w := range wantArgs {
		if b.args[i] != w {
			t.Errorf("Start args[%d] = %q, want %q", i, b.args[i], w)
		}
	}
}

// TestCLIRunner_Start_RejectsInvalidName triangulates name validation.
func TestCLIRunner_Start_RejectsInvalidName(t *testing.T) {
	b := &argsBackend{}
	r := &CLIRunner{bin: "podman", exec: b}
	if err := r.Start(context.Background(), "bad;name"); !errors.Is(err, ErrInvalidContainerName) {
		t.Errorf("Start(bad;name) err = %v, want ErrInvalidContainerName", err)
	}
	if b.args != nil {
		t.Errorf("Start(bad;name) spawned podman; want no exec call")
	}
}

// TestCLIRunner_Start_PropagatesExecError triangulates the error path.
func TestCLIRunner_Start_PropagatesExecError(t *testing.T) {
	b := &argsBackend{err: errors.New("exit status 1")}
	r := &CLIRunner{bin: "podman", exec: b}
	err := r.Start(context.Background(), "mybox")
	if err == nil {
		t.Fatalf("Start(backend error) err = nil, want error")
	}
	if !strings.Contains(err.Error(), "start") {
		t.Errorf("Start error = %v, want wrapped error mentioning start", err)
	}
}

// TestCLIRunner_Cp_RunsPodmanCp is the happy-path RED anchor for Cp: it MUST
// run `podman cp <src> <dst>`. Cp has no container name to validate.
func TestCLIRunner_Cp_RunsPodmanCp(t *testing.T) {
	b := &argsBackend{out: ""}
	r := &CLIRunner{bin: "podman", exec: b}
	if err := r.Cp(context.Background(), "/host/path/file", "mybox:/container/path"); err != nil {
		t.Fatalf("Cp error: %v", err)
	}
	wantArgs := []string{"cp", "/host/path/file", "mybox:/container/path"}
	if len(b.args) != len(wantArgs) {
		t.Fatalf("Cp args = %v, want %v", b.args, wantArgs)
	}
	for i, w := range wantArgs {
		if b.args[i] != w {
			t.Errorf("Cp args[%d] = %q, want %q", i, b.args[i], w)
		}
	}
}

// TestCLIRunner_Cp_PropagatesExecError triangulates the error path.
func TestCLIRunner_Cp_PropagatesExecError(t *testing.T) {
	b := &argsBackend{err: errors.New("exit status 125")}
	r := &CLIRunner{bin: "podman", exec: b}
	err := r.Cp(context.Background(), "/missing", "mybox:/dst")
	if err == nil {
		t.Fatalf("Cp(backend error) err = nil, want error")
	}
	if !strings.Contains(err.Error(), "cp") {
		t.Errorf("Cp error = %v, want wrapped error mentioning cp", err)
	}
}

// TestCLIRunner_Pull_RunsPodmanPull is the happy-path RED anchor for Pull: it
// MUST run `podman pull <image>`. Pull has no container name to validate.
func TestCLIRunner_Pull_RunsPodmanPull(t *testing.T) {
	b := &argsBackend{out: ""}
	r := &CLIRunner{bin: "podman", exec: b}
	if err := r.Pull(context.Background(), "fedora:45"); err != nil {
		t.Fatalf("Pull error: %v", err)
	}
	wantArgs := []string{"pull", "fedora:45"}
	if len(b.args) != len(wantArgs) {
		t.Fatalf("Pull args = %v, want %v", b.args, wantArgs)
	}
	for i, w := range wantArgs {
		if b.args[i] != w {
			t.Errorf("Pull args[%d] = %q, want %q", i, b.args[i], w)
		}
	}
}

// TestCLIRunner_Pull_PropagatesExecError triangulates the error path (spec:
// Pull fails with clear error).
func TestCLIRunner_Pull_PropagatesExecError(t *testing.T) {
	b := &argsBackend{err: errors.New("exit status 125: image not found")}
	r := &CLIRunner{bin: "podman", exec: b}
	err := r.Pull(context.Background(), "fedora:45")
	if err == nil {
		t.Fatalf("Pull(backend error) err = nil, want error")
	}
	if !strings.Contains(err.Error(), "pull") {
		t.Errorf("Pull error = %v, want wrapped error mentioning pull", err)
	}
}

// TestCLIRunner_Pull_DifferentImage triangulates with a different image to
// force real arg construction (not a hardcoded value).
func TestCLIRunner_Pull_DifferentImage(t *testing.T) {
	b := &argsBackend{out: ""}
	r := &CLIRunner{bin: "podman", exec: b}
	if err := r.Pull(context.Background(), "ubuntu:24.04"); err != nil {
		t.Fatalf("Pull error: %v", err)
	}
	if len(b.args) != 2 || b.args[1] != "ubuntu:24.04" {
		t.Errorf("Pull(ubuntu:24.04) args = %v, want [pull ubuntu:24.04]", b.args)
	}
}

// TestCLIRunner_Remove_RunsPodmanRemove is the happy-path RED anchor for
// Remove: it MUST run `podman rm <name>`.
func TestCLIRunner_Remove_RunsPodmanRemove(t *testing.T) {
	b := &argsBackend{out: ""}
	r := &CLIRunner{bin: "podman", exec: b}
	if err := r.Remove(context.Background(), "mybox"); err != nil {
		t.Fatalf("Remove error: %v", err)
	}
	wantArgs := []string{"rm", "mybox"}
	if len(b.args) != len(wantArgs) {
		t.Fatalf("Remove args = %v, want %v", b.args, wantArgs)
	}
	for i, w := range wantArgs {
		if b.args[i] != w {
			t.Errorf("Remove args[%d] = %q, want %q", i, b.args[i], w)
		}
	}
}

// TestCLIRunner_Remove_RejectsInvalidName triangulates name validation.
func TestCLIRunner_Remove_RejectsInvalidName(t *testing.T) {
	b := &argsBackend{}
	r := &CLIRunner{bin: "podman", exec: b}
	if err := r.Remove(context.Background(), "bad;name"); !errors.Is(err, ErrInvalidContainerName) {
		t.Errorf("Remove(bad;name) err = %v, want ErrInvalidContainerName", err)
	}
	if b.args != nil {
		t.Errorf("Remove(bad;name) spawned podman; want no exec call")
	}
}

// TestCLIRunner_Remove_PropagatesExecError triangulates the error path.
func TestCLIRunner_Remove_PropagatesExecError(t *testing.T) {
	b := &argsBackend{err: errors.New("exit status 1: no such container")}
	r := &CLIRunner{bin: "podman", exec: b}
	err := r.Remove(context.Background(), "mybox")
	if err == nil {
		t.Fatalf("Remove(backend error) err = nil, want error")
	}
	if !strings.Contains(err.Error(), "remove") {
		t.Errorf("Remove error = %v, want wrapped error mentioning remove", err)
	}
}

// --- FakeRunner doubles for the 5 new methods ---
//
// These mirror the existing FakeRunner test style: configured result/error
// fields, Calls recording, and assertion of recorded call arguments.

// TestFakeRunner_Create_RecordsCallAndResult verifies the fake records the
// Create call and returns the configured error (spec scenario: Create records
// env vars).
func TestFakeRunner_Create_RecordsCallAndResult(t *testing.T) {
	configuredErr := errors.New("name already in use")
	r := &FakeRunner{CreateErr: configuredErr}
	err := r.Create(context.Background(), "fedora:45", "mybox", []string{"EDITOR=nvim"})
	if !errors.Is(err, configuredErr) {
		t.Errorf("FakeRunner.Create err = %v, want %v", err, configuredErr)
	}
	if len(r.CreateCalls) != 1 {
		t.Fatalf("FakeRunner.CreateCalls len = %d, want 1", len(r.CreateCalls))
	}
	call := r.CreateCalls[0]
	if call.Image != "fedora:45" {
		t.Errorf("CreateCalls[0].Image = %q, want fedora:45", call.Image)
	}
	if call.Name != "mybox" {
		t.Errorf("CreateCalls[0].Name = %q, want mybox", call.Name)
	}
	if len(call.Env) != 1 || call.Env[0] != "EDITOR=nvim" {
		t.Errorf("CreateCalls[0].Env = %v, want [EDITOR=nvim]", call.Env)
	}
}

// TestFakeRunner_Create_HappyPath triangulates: nil error returns success and
// still records the call.
func TestFakeRunner_Create_HappyPath(t *testing.T) {
	r := &FakeRunner{}
	if err := r.Create(context.Background(), "ubuntu:24.04", "box2", []string{"TERM=xterm"}); err != nil {
		t.Fatalf("FakeRunner.Create error: %v", err)
	}
	if len(r.CreateCalls) != 1 {
		t.Fatalf("CreateCalls len = %d, want 1", len(r.CreateCalls))
	}
	if r.CreateCalls[0].Image != "ubuntu:24.04" || r.CreateCalls[0].Name != "box2" {
		t.Errorf("CreateCalls[0] = %+v, want image=ubuntu:24.04 name=box2", r.CreateCalls[0])
	}
}

// TestFakeRunner_Start_RecordsCall verifies the fake records Start calls.
func TestFakeRunner_Start_RecordsCall(t *testing.T) {
	r := &FakeRunner{StartErr: errors.New("already running")}
	err := r.Start(context.Background(), "mybox")
	if err == nil {
		t.Errorf("FakeRunner.Start err = nil, want configured error")
	}
	if len(r.StartCalls) != 1 || r.StartCalls[0] != "mybox" {
		t.Errorf("StartCalls = %v, want [mybox]", r.StartCalls)
	}
}

// TestFakeRunner_Start_HappyPath triangulates the success path.
func TestFakeRunner_Start_HappyPath(t *testing.T) {
	r := &FakeRunner{}
	if err := r.Start(context.Background(), "box2"); err != nil {
		t.Fatalf("FakeRunner.Start error: %v", err)
	}
	if len(r.StartCalls) != 1 || r.StartCalls[0] != "box2" {
		t.Errorf("StartCalls = %v, want [box2]", r.StartCalls)
	}
}

// TestFakeRunner_Cp_RecordsCall verifies the fake records Cp calls.
func TestFakeRunner_Cp_RecordsCall(t *testing.T) {
	r := &FakeRunner{CpErr: errors.New("no such path")}
	err := r.Cp(context.Background(), "/host/a", "mybox:/dst")
	if err == nil {
		t.Errorf("FakeRunner.Cp err = nil, want configured error")
	}
	if len(r.CpCalls) != 1 {
		t.Fatalf("CpCalls len = %d, want 1", len(r.CpCalls))
	}
	if r.CpCalls[0].Src != "/host/a" || r.CpCalls[0].Dst != "mybox:/dst" {
		t.Errorf("CpCalls[0] = %+v, want src=/host/a dst=mybox:/dst", r.CpCalls[0])
	}
}

// TestFakeRunner_Cp_HappyPath triangulates the success path.
func TestFakeRunner_Cp_HappyPath(t *testing.T) {
	r := &FakeRunner{}
	if err := r.Cp(context.Background(), "/x", "box:/y"); err != nil {
		t.Fatalf("FakeRunner.Cp error: %v", err)
	}
	if len(r.CpCalls) != 1 || r.CpCalls[0].Src != "/x" {
		t.Errorf("CpCalls = %+v, want one with src=/x", r.CpCalls)
	}
}

// TestFakeRunner_Pull_RecordsCall verifies the fake records Pull calls.
func TestFakeRunner_Pull_RecordsCall(t *testing.T) {
	r := &FakeRunner{PullErr: errors.New("image not found")}
	err := r.Pull(context.Background(), "fedora:45")
	if err == nil {
		t.Errorf("FakeRunner.Pull err = nil, want configured error")
	}
	if len(r.PullCalls) != 1 || r.PullCalls[0] != "fedora:45" {
		t.Errorf("PullCalls = %v, want [fedora:45]", r.PullCalls)
	}
}

// TestFakeRunner_Pull_HappyPath triangulates the success path.
func TestFakeRunner_Pull_HappyPath(t *testing.T) {
	r := &FakeRunner{}
	if err := r.Pull(context.Background(), "ubuntu:24.04"); err != nil {
		t.Fatalf("FakeRunner.Pull error: %v", err)
	}
	if len(r.PullCalls) != 1 || r.PullCalls[0] != "ubuntu:24.04" {
		t.Errorf("PullCalls = %v, want [ubuntu:24.04]", r.PullCalls)
	}
}

// TestFakeRunner_Remove_RecordsCall verifies the fake records Remove calls.
func TestFakeRunner_Remove_RecordsCall(t *testing.T) {
	r := &FakeRunner{RemoveErr: errors.New("no such container")}
	err := r.Remove(context.Background(), "mybox")
	if err == nil {
		t.Errorf("FakeRunner.Remove err = nil, want configured error")
	}
	if len(r.RemoveCalls) != 1 || r.RemoveCalls[0] != "mybox" {
		t.Errorf("RemoveCalls = %v, want [mybox]", r.RemoveCalls)
	}
}

// TestFakeRunner_Remove_HappyPath triangulates the success path.
func TestFakeRunner_Remove_HappyPath(t *testing.T) {
	r := &FakeRunner{}
	if err := r.Remove(context.Background(), "box2"); err != nil {
		t.Fatalf("FakeRunner.Remove error: %v", err)
	}
	if len(r.RemoveCalls) != 1 || r.RemoveCalls[0] != "box2" {
		t.Errorf("RemoveCalls = %v, want [box2]", r.RemoveCalls)
	}
}

// TestFakeRunner_AllNewMethodsRecordInCalls verifies that invoking all 5 new
// methods adds 5 entries to the shared Calls slice, proving the recording seam
// is consistent with the existing methods.
func TestFakeRunner_AllNewMethodsRecordInCalls(t *testing.T) {
	r := &FakeRunner{}
	ctx := context.Background()
	_ = r.Create(ctx, "fedora:45", "mybox", []string{"EDITOR=nvim"})
	_ = r.Start(ctx, "mybox")
	_ = r.Cp(ctx, "/a", "mybox:/b")
	_ = r.Pull(ctx, "fedora:45")
	_ = r.Remove(ctx, "mybox")
	// Expect the 5 new methods to appear in Calls, in order.
	if len(r.Calls) < 5 {
		t.Fatalf("Calls len = %d, want at least 5", len(r.Calls))
	}
	wantPrefixes := []string{"Create:mybox", "Start:mybox", "Cp:", "Pull:fedora:45", "Remove:mybox"}
	for _, prefix := range wantPrefixes {
		found := false
		for _, c := range r.Calls {
			if strings.HasPrefix(c, prefix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Calls missing entry with prefix %q; Calls=%v", prefix, r.Calls)
		}
	}
}

