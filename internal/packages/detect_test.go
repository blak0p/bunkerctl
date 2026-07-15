package packages

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blak0p/bunkerctl/internal/podman"
)

// errNonZero represents a non-zero exit from `which` (manager not present).
var errNonZero = errors.New("non-zero exit")

// fakeExecRunner is a FakeRunner whose Exec returns canned output keyed by the
// joined command string. It lets detection/listing tests assert exactly which
// command was run and what output it produced, without a real container.
type fakeExecRunner struct {
	*podman.FakeRunner
	// execOut maps "which dnf"-style joined commands to their stdout output.
	execOut map[string]string
	// execErr maps joined commands to an error (non-zero exit). If absent, nil.
	execErr map[string]error
	// execCalls records the joined commands invoked, in order.
	execCalls []string
}

func (f *fakeExecRunner) Exec(ctx context.Context, id string, cmd []string) (string, error) {
	joined := strings.Join(cmd, " ")
	f.execCalls = append(f.execCalls, joined)
	out, hasOut := f.execOut[joined]
	if !hasOut {
		out = ""
	}
	err, hasErr := f.execErr[joined]
	if hasErr {
		return out, err
	}
	return out, nil
}

// TestDetect_AptOnly verifies detection: when `which apt` exits 0 (no error)
// and `which dnf` exits non-zero, Detect returns [ManagerApt] only.
func TestDetect_AptOnly(t *testing.T) {
	runner := &fakeExecRunner{
		FakeRunner: &podman.FakeRunner{},
		execOut:    map[string]string{"which apt": ""},
		execErr:    map[string]error{"which dnf": errNonZero, "which dnf5": errNonZero, "which pacman": errNonZero, "which brew": errNonZero},
	}
	det := DefaultDetector{}
	got, err := det.Detect(context.Background(), runner, "devbox")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if len(got) != 1 || got[0] != ManagerApt {
		t.Errorf("Detect = %v, want [ManagerApt]", got)
	}
}

// TestDetect_DnfAndPacman triangulates: when both dnf and pacman are present,
// Detect returns both (order is the canonical probe order).
func TestDetect_DnfAndPacman(t *testing.T) {
	runner := &fakeExecRunner{
		FakeRunner: &podman.FakeRunner{},
		execOut:    map[string]string{"which dnf": "", "which pacman": ""},
		execErr:    map[string]error{"which dnf5": errNonZero, "which apt": errNonZero, "which brew": errNonZero},
	}
	det := DefaultDetector{}
	got, err := det.Detect(context.Background(), runner, "devbox")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Detect len = %d %v, want 2", len(got), got)
	}
	if got[0] != ManagerDnf || got[1] != ManagerPacman {
		t.Errorf("Detect = %v, want [ManagerDnf ManagerPacman]", got)
	}
}

// TestDetect_NonePresent triangulates: when no `which` probe succeeds, Detect
// returns an empty slice (backup continues without a package list).
func TestDetect_NonePresent(t *testing.T) {
	runner := &fakeExecRunner{
		FakeRunner: &podman.FakeRunner{},
		execErr: map[string]error{
			"which dnf":   errNonZero,
			"which dnf5":  errNonZero,
			"which apt":   errNonZero,
			"which pacman": errNonZero,
			"which brew":  errNonZero,
		},
	}
	det := DefaultDetector{}
	got, err := det.Detect(context.Background(), runner, "devbox")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Detect = %v, want empty", got)
	}
}

// TestList_AptRunsExactCommand verifies the Lister for apt runs the exact
// command `apt-mark showmanual` via the runner's Exec.
func TestList_AptRunsExactCommand(t *testing.T) {
	runner := &fakeExecRunner{
		FakeRunner: &podman.FakeRunner{},
		execOut:    map[string]string{"apt-mark showmanual": "vim\ncurl\ngit\n"},
	}
	lister := DefaultLister{}
	got, err := lister.List(context.Background(), runner, "devbox", ManagerApt)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	// Assert the exact command was run.
	found := false
	for _, c := range runner.execCalls {
		if c == "apt-mark showmanual" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("List did not run %q; calls = %v", "apt-mark showmanual", runner.execCalls)
	}
	// Assert the output was parsed into 3 package names.
	if len(got) != 3 {
		t.Fatalf("List len = %d %v, want 3", len(got), got)
	}
	want := []string{"vim", "curl", "git"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("List[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestList_DnfRunsRepoquery triangulates: dnf uses a repoquery command with a
// query format, and the output (one package per line) is parsed.
func TestList_DnfRunsRepoquery(t *testing.T) {
	runner := &fakeExecRunner{
		FakeRunner: &podman.FakeRunner{},
		execOut:    map[string]string{"dnf repoquery --userinstalled --qf %{name}\n": "htop\nstrace\n"},
	}
	lister := DefaultLister{}
	got, err := lister.List(context.Background(), runner, "devbox", ManagerDnf)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List(dnf) len = %d %v, want 2", len(got), got)
	}
	if got[0] != "htop" || got[1] != "strace" {
		t.Errorf("List(dnf) = %v, want [htop strace]", got)
	}
}

// TestList_Pacman triangulates: pacman uses `pacman -Qe | awk '{print $1}'` and
// output lines become package names.
func TestList_Pacman(t *testing.T) {
	// Note: the pipeline is run through the shell-equivalent exec; for the
	// fake we model the joined command. The real implementation runs a shell
	// pipeline via podman exec sh -c "...".
	runner := &fakeExecRunner{
		FakeRunner: &podman.FakeRunner{},
		execOut:    map[string]string{"sh -c pacman -Qe | awk '{print $1}'": "neovim\nripgrep\n"},
	}
	lister := DefaultLister{}
	got, err := lister.List(context.Background(), runner, "devbox", ManagerPacman)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List(pacman) len = %d, want 2", len(got))
	}
	if got[0] != "neovim" || got[1] != "ripgrep" {
		t.Errorf("List(pacman) = %v, want [neovim ripgrep]", got)
	}
}

// TestList_Brew triangulates: brew uses `brew leaves`.
func TestList_Brew(t *testing.T) {
	runner := &fakeExecRunner{
		FakeRunner: &podman.FakeRunner{},
		execOut:    map[string]string{"brew leaves": "jq\nfzf\n"},
	}
	lister := DefaultLister{}
	got, err := lister.List(context.Background(), runner, "devbox", ManagerBrew)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List(brew) len = %d, want 2", len(got))
	}
	if got[0] != "jq" || got[1] != "fzf" {
		t.Errorf("List(brew) = %v, want [jq fzf]", got)
	}
}

// TestList_UnknownReturnsEmpty triangulates: ManagerUnknown yields an empty
// package list and nil error (backup continues without a package list).
func TestList_UnknownReturnsEmpty(t *testing.T) {
	runner := &fakeExecRunner{FakeRunner: &podman.FakeRunner{}}
	lister := DefaultLister{}
	got, err := lister.List(context.Background(), runner, "devbox", ManagerUnknown)
	if err != nil {
		t.Fatalf("List(unknown) error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List(unknown) = %v, want empty", got)
	}
}