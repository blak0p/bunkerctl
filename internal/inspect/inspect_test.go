package inspect

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blak0p/bunkerctl/internal/podman"
)

// fakeRunner is a minimal podman.Runner double keyed by the joined exec command
// and a separate InspectRawResult field. It lets inspect tests drive getent,
// os-release, and podman inspect through the Runner seam with canned output.
type fakeRunner struct {
	*podman.FakeRunner
	execOut map[string]string // joined cmd -> stdout
	execErr map[string]error  // joined cmd -> error (non-zero exit)
	calls   []string          // joined cmds in order
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		FakeRunner: &podman.FakeRunner{},
		execOut:    map[string]string{},
		execErr:    map[string]error{},
	}
}

func (f *fakeRunner) Exec(ctx context.Context, id string, cmd []string) (string, error) {
	joined := strings.Join(cmd, " ")
	f.calls = append(f.calls, joined)
	out := f.execOut[joined]
	if err, ok := f.execErr[joined]; ok {
		return out, err
	}
	return out, nil
}

// --- Fetch: podman inspect JSON parsing (REQ-DETECT-1) ---

// TestFetch_ParsesInspectJSON verifies Fetch parses a realistic podman inspect
// payload into InspectData with the expected ID, Image, User, Env, and State.
func TestFetch_ParsesInspectJSON(t *testing.T) {
	r := newFakeRunner()
	r.InspectRawResult = `[
		{
			"Id": "abc123def",
			"Image": "docker.io/fedora:45",
			"Config": {
				"User": "1000",
				"Env": ["EDITOR=nvim", "TERM=kitty", "PATH=/usr/bin"]
			},
			"State": {"Running": true}
		}
	]`
	got, err := Fetch(context.Background(), r, "bunker")
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if got.ID != "abc123def" {
		t.Errorf("ID = %q, want abc123def", got.ID)
	}
	if got.Image != "docker.io/fedora:45" {
		t.Errorf("Image = %q, want docker.io/fedora:45", got.Image)
	}
	if got.User != "1000" {
		t.Errorf("User = %q, want 1000", got.User)
	}
	if len(got.Env) != 3 || got.Env[0] != "EDITOR=nvim" {
		t.Errorf("Env = %v, want [EDITOR=nvim TERM=kitty PATH=/usr/bin]", got.Env)
	}
	if !got.State.Running {
		t.Errorf("State.Running = false, want true")
	}
}

// TestFetch_EmptyUserDefaultRoot verifies an empty Config.User is normalized to
// "0" (root), matching podman's default for unnamed users.
func TestFetch_EmptyUserDefaultRoot(t *testing.T) {
	r := newFakeRunner()
	r.InspectRawResult = `[{"Id":"x","Image":"fedora:45","Config":{"User":""},"State":{"Running":true}}]`
	got, err := Fetch(context.Background(), r, "bunker")
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if got.User != "0" {
		t.Errorf("User = %q, want 0 (root default)", got.User)
	}
}

// TestFetch_InspectErrorPropagates verifies a podman inspect failure surfaces
// as an error and no InspectData is returned.
func TestFetch_InspectErrorPropagates(t *testing.T) {
	r := newFakeRunner()
	r.InspectRawErr = errors.New("container not found")
	_, err := Fetch(context.Background(), r, "ghost")
	if err == nil {
		t.Fatalf("Fetch err = nil, want error")
	}
}

// TestFetch_MalformedJSON is the adversarial case: bad JSON MUST surface as a
// parse error, not a panic.
func TestFetch_MalformedJSON(t *testing.T) {
	r := newFakeRunner()
	r.InspectRawResult = `{not-json}`
	_, err := Fetch(context.Background(), r, "bunker")
	if err == nil {
		t.Fatalf("Fetch(malformed) err = nil, want parse error")
	}
}

// TestFetch_EmptyArray verifies an empty inspect array returns a clear error
// rather than an index-out-of-range panic.
func TestFetch_EmptyArray(t *testing.T) {
	r := newFakeRunner()
	r.InspectRawResult = `[]`
	_, err := Fetch(context.Background(), r, "bunker")
	if err == nil {
		t.Fatalf("Fetch([]) err = nil, want error")
	}
}

// --- DetectUser: getent passwd with fallback (REQ-DETECT-2, REQ-DETECT-3) ---

// TestDetectUser_GetentSucceeds verifies the happy path: getent passwd <uid>
// returns a passwd line and UserInfo is fully populated from it.
func TestDetectUser_GetentSucceeds(t *testing.T) {
	r := newFakeRunner()
	r.execOut["getent passwd 1000"] = "alejndro:x:1000:1000:Alejndro:/home/alejndro:/bin/bash"
	info, err := DetectUser(context.Background(), r, "bunker", 1000)
	if err != nil {
		t.Fatalf("DetectUser error: %v", err)
	}
	if info.Name != "alejndro" {
		t.Errorf("Name = %q, want alejndro", info.Name)
	}
	if info.UID != 1000 {
		t.Errorf("UID = %d, want 1000", info.UID)
	}
	if info.GID != 1000 {
		t.Errorf("GID = %d, want 1000", info.GID)
	}
	if info.Home != "/home/alejndro" {
		t.Errorf("Home = %q, want /home/alejndro", info.Home)
	}
}

// TestDetectUser_GetentFailsEchoHomeFallback verifies the fallback chain
// (REQ-DETECT-2 scenario): when getent returns nothing, sh -c 'echo $HOME'
// provides at least the home directory.
func TestDetectUser_GetentFailsEchoHomeFallback(t *testing.T) {
	r := newFakeRunner()
	r.execErr["getent passwd 1000"] = errors.New("no entry")
	r.execOut["sh -c echo $HOME"] = "/home/alejndro"
	info, err := DetectUser(context.Background(), r, "bunker", 1000)
	if err != nil {
		t.Fatalf("DetectUser error: %v", err)
	}
	if info.Home != "/home/alejndro" {
		t.Errorf("Home = %q, want /home/alejndro", info.Home)
	}
	if info.Name != "" {
		t.Errorf("Name = %q, want empty (fallback only yields home)", info.Name)
	}
	if info.UID != 1000 {
		t.Errorf("UID = %d, want 1000 (preserved from input)", info.UID)
	}
}

// TestDetectUser_BothFail verifies that when getent AND echo $HOME both fail,
// DetectUser returns an error (no silent success with empty info).
func TestDetectUser_BothFail(t *testing.T) {
	r := newFakeRunner()
	r.execErr["getent passwd 1000"] = errors.New("no entry")
	r.execErr["sh -c echo $HOME"] = errors.New("no shell")
	_, err := DetectUser(context.Background(), r, "bunker", 1000)
	if err == nil {
		t.Fatalf("DetectUser err = nil, want error when both getent and echo fail")
	}
}

// TestDetectUser_MalformedPasswdLine verifies a getent output that is not a
// valid passwd line surfaces as a parse error rather than wrong data.
func TestDetectUser_MalformedPasswdLine(t *testing.T) {
	r := newFakeRunner()
	r.execOut["getent passwd 1000"] = "not-a-passwd-line"
	_, err := DetectUser(context.Background(), r, "bunker", 1000)
	if err == nil {
		t.Fatalf("DetectUser(malformed) err = nil, want parse error")
	}
}

// TestDetectUser_TooFewFields verifies a passwd line with fewer than 7 fields
// is rejected.
func TestDetectUser_TooFewFields(t *testing.T) {
	r := newFakeRunner()
	r.execOut["getent passwd 1000"] = "alejndro:x:1000:1000"
	_, err := DetectUser(context.Background(), r, "bunker", 1000)
	if err == nil {
		t.Fatalf("DetectUser(too few fields) err = nil, want parse error")
	}
}

// TestDetectUser_NonNumericUID verifies a non-numeric uid in the passwd line
// surfaces as a parse error.
func TestDetectUser_NonNumericUID(t *testing.T) {
	r := newFakeRunner()
	r.execOut["getent passwd 1000"] = "alejndro:x:notanint:1000::/home/alejndro:/bin/bash"
	_, err := DetectUser(context.Background(), r, "bunker", 1000)
	if err == nil {
		t.Fatalf("DetectUser(non-numeric uid) err = nil, want parse error")
	}
}

// --- DetectMultiUser (REQ-ERR-3) ---

// TestDetectMultiUser_SingleUser verifies a container with one non-system user
// (UID >= 1000) returns nil (no error).
func TestDetectMultiUser_SingleUser(t *testing.T) {
	r := newFakeRunner()
	r.execOut["getent passwd"] = `root:x:0:0:root:/root:/bin/bash
alejndro:x:1000:1000:Alejndro:/home/alejndro:/bin/bash
`
	err := DetectMultiUser(context.Background(), r, "bunker")
	if err != nil {
		t.Errorf("DetectMultiUser(single) err = %v, want nil", err)
	}
}

// TestDetectMultiUser_TwoUsers verifies a container with two non-system users
// returns ErrMultiUser (REQ-ERR-3 scenario).
func TestDetectMultiUser_TwoUsers(t *testing.T) {
	r := newFakeRunner()
	r.execOut["getent passwd"] = `root:x:0:0:root:/root:/bin/bash
alice:x:1000:1000:Alice:/home/alice:/bin/bash
bob:x:1001:1001:Bob:/home/bob:/bin/bash
`
	err := DetectMultiUser(context.Background(), r, "shared")
	if !errors.Is(err, ErrMultiUser) {
		t.Errorf("DetectMultiUser(two users) err = %v, want ErrMultiUser", err)
	}
}

// TestDetectMultiUser_NoUsers verifies a container with only system users
// (UID < 1000) returns nil — not an error, just nothing to flag.
func TestDetectMultiUser_NoUsers(t *testing.T) {
	r := newFakeRunner()
	r.execOut["getent passwd"] = `root:x:0:0:root:/root:/bin/bash
bin:x:1:1:bin:/bin:/sbin/nologin
`
	err := DetectMultiUser(context.Background(), r, "bunker")
	if err != nil {
		t.Errorf("DetectMultiUser(no real users) err = %v, want nil", err)
	}
}

// TestDetectMultiUser_UserWithoutHome verifies a non-system user with no home
// directory (empty home field) is NOT counted, matching the spec's "with a
// home directory" qualifier.
func TestDetectMultiUser_UserWithoutHome(t *testing.T) {
	r := newFakeRunner()
	r.execOut["getent passwd"] = `svc:x:1000:1000:svc::/sbin/nologin
root:x:0:0:root:/root:/bin/bash
`
	err := DetectMultiUser(context.Background(), r, "bunker")
	if err != nil {
		t.Errorf("DetectMultiUser(user no home) err = %v, want nil", err)
	}
}

// --- DetectBase: os-release parsing (REQ-YAML-3) ---

// TestDetectBase_Fedora verifies the happy path: /etc/os-release with ID=fedora
// and VERSION_ID=45 yields BaseInfo{fedora, 45}.
func TestDetectBase_Fedora(t *testing.T) {
	r := newFakeRunner()
	r.execOut["cat /etc/os-release"] = `NAME="Fedora Linux"
VERSION_ID=45
ID=fedora
VERSION="45 (Container Image)"`
	base, err := DetectBase(context.Background(), r, "bunker")
	if err != nil {
		t.Fatalf("DetectBase error: %v", err)
	}
	if base.Distro != "fedora" {
		t.Errorf("Distro = %q, want fedora", base.Distro)
	}
	if base.Version != "45" {
		t.Errorf("Version = %q, want 45", base.Version)
	}
}

// TestDetectBase_NonFedoraRejected verifies a non-Fedora container returns
// ErrUnsupportedDistro (REQ-YAML-3 scenario: non-Fedora rejected).
func TestDetectBase_NonFedoraRejected(t *testing.T) {
	r := newFakeRunner()
	r.execOut["cat /etc/os-release"] = `NAME="Ubuntu"
ID=ubuntu
VERSION_ID=24.04`
	_, err := DetectBase(context.Background(), r, "ubuntu-box")
	if !errors.Is(err, ErrUnsupportedDistro) {
		t.Errorf("DetectBase(ubuntu) err = %v, want ErrUnsupportedDistro", err)
	}
}

// TestDetectBase_MissingID verifies os-release without an ID line surfaces as
// a parse error.
func TestDetectBase_MissingID(t *testing.T) {
	r := newFakeRunner()
	r.execOut["cat /etc/os-release"] = `NAME="Something"
VERSION_ID=1`
	_, err := DetectBase(context.Background(), r, "bunker")
	if err == nil {
		t.Fatalf("DetectBase(no ID) err = nil, want parse error")
	}
}

// TestDetectBase_MissingVersionID verifies os-release without VERSION_ID
// surfaces as a parse error.
func TestDetectBase_MissingVersionID(t *testing.T) {
	r := newFakeRunner()
	r.execOut["cat /etc/os-release"] = `NAME="Fedora Linux"
ID=fedora`
	_, err := DetectBase(context.Background(), r, "bunker")
	if err == nil {
		t.Fatalf("DetectBase(no VERSION_ID) err = nil, want parse error")
	}
}

// TestDetectBase_EmptyOutput verifies an empty os-release response surfaces as
// a parse error rather than returning empty BaseInfo.
func TestDetectBase_EmptyOutput(t *testing.T) {
	r := newFakeRunner()
	r.execOut["cat /etc/os-release"] = ""
	_, err := DetectBase(context.Background(), r, "bunker")
	if err == nil {
		t.Fatalf("DetectBase(empty) err = nil, want parse error")
	}
}

// TestDetectBase_QuotedValues verifies ID and VERSION_ID values wrapped in
// double quotes are unquoted correctly.
func TestDetectBase_QuotedValues(t *testing.T) {
	r := newFakeRunner()
	r.execOut["cat /etc/os-release"] = "ID=\"fedora\"\nVERSION_ID=\"45\""
	base, err := DetectBase(context.Background(), r, "bunker")
	if err != nil {
		t.Fatalf("DetectBase(quoted) error: %v", err)
	}
	if base.Distro != "fedora" {
		t.Errorf("Distro = %q, want fedora", base.Distro)
	}
	if base.Version != "45" {
		t.Errorf("Version = %q, want 45", base.Version)
	}
}

// TestDetectBase_CatFails verifies a cat failure (e.g. no os-release file)
// surfaces as an error.
func TestDetectBase_CatFails(t *testing.T) {
	r := newFakeRunner()
	r.execErr["cat /etc/os-release"] = errors.New("no such file")
	_, err := DetectBase(context.Background(), r, "bunker")
	if err == nil {
		t.Fatalf("DetectBase(cat fails) err = nil, want error")
	}
}