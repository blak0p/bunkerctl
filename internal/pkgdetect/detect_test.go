package pkgdetect

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blak0p/bunkerctl/internal/packages"
	"github.com/blak0p/bunkerctl/internal/podman"
)

// fakeRunner is a minimal podman.Runner double keyed by the joined exec command.
type fakeRunner struct {
	*podman.FakeRunner
	execOut map[string]string
	execErr map[string]error
	calls   []string
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

// cannedDnfListInstalled is realistic `dnf list installed` output: a header
// line, then rows of "name.version    repo". dnf separates name and version
// with a dot in the first column.
const cannedDnfListInstalled = `Installed Packages
neovim.x86_64                 0.10.2-1.fc40            @fedora
fish.x86_64                   3.7.0-1.fc40             @fedora
ripgrep.x86_64                14.1.0-1.fc40            @fedora`

// cannedDnf5ListInstalled is realistic `dnf5 list installed` output, same
// shape as dnf (dnf5 reuses the list installed format).
const cannedDnf5ListInstalled = `Installed Packages
neovim.x86_64                 0.10.2-1.fc40            @system
fish.x86_64                   3.7.0-1.fc40             @system`

// --- DnfDetector ---

// TestDnfDetector_ParsesListInstalled verifies DnfDetector runs the dnf list
// command and parses name+version from each row (REQ-DETECT-6).
func TestDnfDetector_ParsesListInstalled(t *testing.T) {
	r := newFakeRunner()
	r.execOut["dnf list installed"] = cannedDnfListInstalled
	d := NewDnfDetector()
	pkgs, err := d.Detect(context.Background(), r, "bunker")
	if err != nil {
		t.Fatalf("DnfDetector.Detect error: %v", err)
	}
	if len(pkgs) != 3 {
		t.Fatalf("len = %d, want 3", len(pkgs))
	}
	wantByName := map[string]string{
		"neovim":  "0.10.2-1.fc40",
		"fish":    "3.7.0-1.fc40",
		"ripgrep": "14.1.0-1.fc40",
	}
	gotByName := map[string]string{}
	for _, p := range pkgs {
		gotByName[p.Name] = p.Version
	}
	for name, wantVer := range wantByName {
		if gotVer, ok := gotByName[name]; !ok {
			t.Errorf("missing package %q", name)
		} else if gotVer != wantVer {
			t.Errorf("version for %q = %q, want %q", name, gotVer, wantVer)
		}
	}
}

// TestDnfDetector_StripsArchSuffix verifies the .x86_64/.noarch arch suffix is
// stripped from the package name, leaving just the package name.
func TestDnfDetector_StripsArchSuffix(t *testing.T) {
	r := newFakeRunner()
	r.execOut["dnf list installed"] = "Installed Packages\nfoo.noarch  1.2.3-1.fc40  @fedora"
	d := NewDnfDetector()
	pkgs, err := d.Detect(context.Background(), r, "bunker")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("len = %d, want 1", len(pkgs))
	}
	if pkgs[0].Name != "foo" {
		t.Errorf("Name = %q, want foo (arch stripped)", pkgs[0].Name)
	}
	if pkgs[0].Version != "1.2.3-1.fc40" {
		t.Errorf("Version = %q, want 1.2.3-1.fc40", pkgs[0].Version)
	}
}

// TestDnfDetector_EmptyOutput verifies empty/whitespace output yields an empty
// (non-nil) slice with no error.
func TestDnfDetector_EmptyOutput(t *testing.T) {
	r := newFakeRunner()
	r.execOut["dnf list installed"] = "Installed Packages\n"
	d := NewDnfDetector()
	pkgs, err := d.Detect(context.Background(), r, "bunker")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if len(pkgs) != 0 {
		t.Errorf("len = %d, want 0 for empty output", len(pkgs))
	}
}

// TestDnfDetector_ExecFails verifies an exec error surfaces (e.g. dnf not
// present).
func TestDnfDetector_ExecFails(t *testing.T) {
	r := newFakeRunner()
	r.execErr["dnf list installed"] = errors.New("dnf: command not found")
	d := NewDnfDetector()
	_, err := d.Detect(context.Background(), r, "bunker")
	if err == nil {
		t.Fatalf("Detect(dnf missing) err = nil, want error")
	}
}

// --- Dnf5Detector ---

// TestDnf5Detector_ParsesListInstalled verifies Dnf5Detector runs the dnf5 list
// command and parses name+version.
func TestDnf5Detector_ParsesListInstalled(t *testing.T) {
	r := newFakeRunner()
	r.execOut["dnf5 list installed"] = cannedDnf5ListInstalled
	d := NewDnf5Detector()
	pkgs, err := d.Detect(context.Background(), r, "bunker5")
	if err != nil {
		t.Fatalf("Dnf5Detector.Detect error: %v", err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("len = %d, want 2", len(pkgs))
	}
	if pkgs[0].Name != "neovim" || pkgs[0].Version != "0.10.2-1.fc40" {
		t.Errorf("pkgs[0] = %+v, want {neovim 0.10.2-1.fc40}", pkgs[0])
	}
}

// --- DetectManager (REQ-DETECT-5) ---

// TestDetectManager_PrefersDnf5 verifies that when both dnf and dnf5 are
// present, DetectManager returns the dnf5 detector (REQ-DETECT-5 scenario:
// dnf5 preferred).
func TestDetectManager_PrefersDnf5(t *testing.T) {
	r := newFakeRunner()
	// `which dnf5` and `which dnf` both exit 0 (no error).
	r.execOut["which dnf5"] = "/usr/bin/dnf5"
	r.execOut["which dnf"] = "/usr/bin/dnf"
	mgr, _, err := DetectManager(context.Background(), r, "bunker5")
	if err != nil {
		t.Fatalf("DetectManager error: %v", err)
	}
	if mgr != packages.ManagerDnf5 {
		t.Errorf("manager = %q, want dnf5 (preferred)", mgr)
	}
}

// TestDetectManager_FallsBackToDnf verifies that when only dnf is present,
// DetectManager returns the dnf detector.
func TestDetectManager_FallsBackToDnf(t *testing.T) {
	r := newFakeRunner()
	r.execOut["which dnf"] = "/usr/bin/dnf"
	r.execErr["which dnf5"] = errors.New("not found")
	mgr, _, err := DetectManager(context.Background(), r, "bunker")
	if err != nil {
		t.Fatalf("DetectManager error: %v", err)
	}
	if mgr != packages.ManagerDnf {
		t.Errorf("manager = %q, want dnf (fallback)", mgr)
	}
}

// TestDetectManager_NeitherPresent verifies that when neither dnf nor dnf5 is
// present, DetectManager returns ErrNoPackageManager (REQ-DETECT-5 scenario).
func TestDetectManager_NeitherPresent(t *testing.T) {
	r := newFakeRunner()
	r.execErr["which dnf5"] = errors.New("not found")
	r.execErr["which dnf"] = errors.New("not found")
	_, _, err := DetectManager(context.Background(), r, "stripped")
	if !errors.Is(err, ErrNoPackageManager) {
		t.Errorf("DetectManager(neither) err = %v, want ErrNoPackageManager", err)
	}
}

// TestDetectManager_ReturnsDetector verifies the returned Detector is the
// correct concrete type for the detected manager.
func TestDetectManager_ReturnsDetector(t *testing.T) {
	r := newFakeRunner()
	r.execOut["which dnf5"] = "/usr/bin/dnf5"
	mgr, det, err := DetectManager(context.Background(), r, "bunker5")
	if err != nil {
		t.Fatalf("DetectManager error: %v", err)
	}
	if mgr != packages.ManagerDnf5 {
		t.Fatalf("manager = %q, want dnf5", mgr)
	}
	switch det.(type) {
	case *Dnf5Detector:
		// ok
	default:
		t.Errorf("detector type = %T, want *Dnf5Detector", det)
	}
}

// TestDetectManager_Dnf5ProbeOrder verifies dnf5 is probed BEFORE dnf. When
// dnf5 is present, DetectManager short-circuits and does NOT probe dnf (this
// is the preference behavior). We assert the first probe call is "which dnf5".
func TestDetectManager_Dnf5ProbeOrder(t *testing.T) {
	r := newFakeRunner()
	r.execOut["which dnf5"] = "/usr/bin/dnf5"
	r.execOut["which dnf"] = "/usr/bin/dnf"
	_, _, _ = DetectManager(context.Background(), r, "bunker5")
	if len(r.calls) == 0 {
		t.Fatalf("expected at least one probe call; got %v", r.calls)
	}
	if r.calls[0] != "which dnf5" {
		t.Errorf("first probe = %q, want \"which dnf5\" (dnf5 probed first)", r.calls[0])
	}
}

// TestDetectManager_ProbesDnfWhenDnf5Absent verifies that when dnf5 is absent,
// DetectManager falls through to probing dnf.
func TestDetectManager_ProbesDnfWhenDnf5Absent(t *testing.T) {
	r := newFakeRunner()
	r.execErr["which dnf5"] = errors.New("not found")
	r.execOut["which dnf"] = "/usr/bin/dnf"
	_, _, _ = DetectManager(context.Background(), r, "bunker")
	if len(r.calls) < 2 {
		t.Fatalf("expected both probes; got %v", r.calls)
	}
	if r.calls[0] != "which dnf5" || r.calls[1] != "which dnf" {
		t.Errorf("probe order = %v, want [which dnf5, which dnf]", r.calls)
	}
}