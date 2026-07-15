package podman

import (
	"context"
	"errors"
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

// --- PR 4: Commit/Save format validation + validateImageRef ---

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

// TestCLIRunner_Save_RejectsInvalidFormat is a RED test: Save with an
// unsupported format ("gzip") MUST return ErrInvalidFormat BEFORE spawning the
// podman process (no exec call).
func TestCLIRunner_Save_RejectsInvalidFormat(t *testing.T) {
	b := &argsBackend{}
	r := &CLIRunner{bin: "podman", exec: b}
	err := r.Save(context.Background(), "fedora:40", "gzip", "/tmp/x.tar")
	if !errors.Is(err, ErrInvalidFormat) {
		t.Errorf("Save(gzip) err = %v, want ErrInvalidFormat", err)
	}
	if b.args != nil {
		t.Errorf("Save(gzip) spawned podman with args %v; want no exec call", b.args)
	}
}

// TestCLIRunner_Save_DockerArchive triangulates: Save with format
// "docker-archive" MUST run `podman save --format=docker-archive -o <dest>
// <image>` (the right podman command).
func TestCLIRunner_Save_DockerArchive(t *testing.T) {
	b := &argsBackend{}
	r := &CLIRunner{bin: "podman", exec: b}
	if err := r.Save(context.Background(), "fedora:40", "docker-archive", "/tmp/x.tar"); err != nil {
		t.Fatalf("Save(docker-archive) error: %v", err)
	}
	if b.name != "podman" {
		t.Errorf("binary = %q, want podman", b.name)
	}
	// Expected: save --format=docker-archive -o /tmp/x.tar fedora:40
	want := []string{"save", "--format=docker-archive", "-o", "/tmp/x.tar", "fedora:40"}
	if len(b.args) != len(want) {
		t.Fatalf("args = %v, want %v", b.args, want)
	}
	for i, w := range want {
		if b.args[i] != w {
			t.Errorf("args[%d] = %q, want %q (full: %v)", i, b.args[i], w, b.args)
		}
	}
}

// TestCLIRunner_Save_OciArchive triangulates: with format "oci-archive" the
// --format flag reflects it.
func TestCLIRunner_Save_OciArchive(t *testing.T) {
	b := &argsBackend{}
	r := &CLIRunner{bin: "podman", exec: b}
	if err := r.Save(context.Background(), "ubuntu:24.04", "oci-archive", "/tmp/o.tar"); err != nil {
		t.Fatalf("Save(oci-archive) error: %v", err)
	}
	want := []string{"save", "--format=oci-archive", "-o", "/tmp/o.tar", "ubuntu:24.04"}
	for i, w := range want {
		if b.args[i] != w {
			t.Errorf("args[%d] = %q, want %q (full: %v)", i, b.args[i], w, b.args)
		}
	}
}

// TestCLIRunner_Commit_RunsPodmanCommit is a RED test: Commit MUST run
// `podman commit <id> <image>`.
func TestCLIRunner_Commit_RunsPodmanCommit(t *testing.T) {
	b := &argsBackend{}
	r := &CLIRunner{bin: "podman", exec: b}
	if err := r.Commit(context.Background(), "mybunker", "bunkerctl-tmp-123"); err != nil {
		t.Fatalf("Commit error: %v", err)
	}
	want := []string{"commit", "mybunker", "bunkerctl-tmp-123"}
	if len(b.args) != len(want) {
		t.Fatalf("Commit args = %v, want %v", b.args, want)
	}
	for i, w := range want {
		if b.args[i] != w {
			t.Errorf("Commit args[%d] = %q, want %q", i, b.args[i], w)
		}
	}
}

// TestValidateImageRef_RejectsInjection is a RED test: validateImageRef MUST
// reject shell-injection attempts like `image;rm` and names with spaces.
func TestValidateImageRef_RejectsInjection(t *testing.T) {
	bad := []string{
		"image;rm",
		"image with space",
		"image&foo",
		"image|bar",
		"image`whoami`",
	}
	for _, ref := range bad {
		if err := validateImageRef(ref); !errors.Is(err, ErrInvalidContainerName) {
			t.Errorf("validateImageRef(%q) err = %v, want ErrInvalidContainerName", ref, err)
		}
	}
}

// TestValidateImageRef_AcceptsValidRefs triangulates: valid image refs with
// tags, registry paths, and digests MUST be accepted.
func TestValidateImageRef_AcceptsValidRefs(t *testing.T) {
	good := []string{
		"fedora:40",
		"docker.io/library/ubuntu:24.04",
		"myregistry.com:5000/myimg:v1",
		"image@sha256:abc123",
		"bunkerctl-tmp-1700000000",
	}
	for _, ref := range good {
		if err := validateImageRef(ref); err != nil {
			t.Errorf("validateImageRef(%q) err = %v, want nil", ref, err)
		}
	}
}