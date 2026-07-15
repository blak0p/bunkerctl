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

// cannedDnf5RepoqueryInstalled is realistic `dnf5 repoquery --installed`
// output, captured from the real Fedora 44 container. The format is
// `name-epoch:version-release.arch`, one package per line (no header). Epoch
// is `0:` for most packages and non-zero (e.g. `1:`) for a few. Several
// packages ship multiple arches (i686 + x86_64) and appear once per arch.
const cannedDnf5RepoqueryInstalled = `7zip-0:26.02-1.fc44.x86_64
apr-util-0:1.6.3-27.fc44.x86_64
apr-util-lmdb-0:1.6.3-27.fc44.x86_64
bash-0:5.3.9-3.fc44.x86_64
cups-filesystem-1:2.4.19-3.fc44.noarch
dbus-1:1.16.2-1.fc44.x86_64
fish-0:4.6.0-1.fc44.x86_64
glibc-0:2.43-7.fc44.i686
glibc-0:2.43-7.fc44.x86_64
neovim-0:0.10.2-1.fc44.x86_64
python3.12-0:3.12.6-1.fc44.x86_64`

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

// TestDnf5Detector_ParsesRepoqueryInstalled verifies Dnf5Detector runs
// `dnf5 repoquery --installed` (NOT `dnf5 list installed`, which returns exit 1
// with "No matching packages" on real Fedora 44/45) and parses name+version
// from the `name-epoch:version-release.arch` output. Covers: epoch=0 and
// non-zero epoch, hyphenated names, dotted names, and multi-arch duplicates.
func TestDnf5Detector_ParsesRepoqueryInstalled(t *testing.T) {
	r := newFakeRunner()
	r.execOut["dnf5 repoquery --installed"] = cannedDnf5RepoqueryInstalled
	d := NewDnf5Detector()
	pkgs, err := d.Detect(context.Background(), r, "bunker5")
	if err != nil {
		t.Fatalf("Dnf5Detector.Detect error: %v", err)
	}
	if len(pkgs) != 11 {
		t.Fatalf("len = %d, want 11 (one per line incl. multi-arch)", len(pkgs))
	}
	wantByName := map[string]string{
		"7zip":             "26.02-1.fc44",
		"apr-util":         "1.6.3-27.fc44",
		"apr-util-lmdb":    "1.6.3-27.fc44",
		"bash":             "5.3.9-3.fc44",
		"cups-filesystem":  "2.4.19-3.fc44",
		"dbus":             "1.16.2-1.fc44",
		"fish":             "4.6.0-1.fc44",
		"glibc":            "2.43-7.fc44",
		"neovim":           "0.10.2-1.fc44",
		"python3.12":       "3.12.6-1.fc44",
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
	// Non-zero epoch (cups-filesystem has epoch=1): epoch must NOT leak into
	// the version string. Version is `version-release` only.
	for _, p := range pkgs {
		if strings.Contains(p.Version, ":") {
			t.Errorf("version %q for %q contains epoch colon; want version-release only", p.Version, p.Name)
		}
	}
}

// TestDnf5Detector_StripsArchAndEpoch verifies a single repoquery line is
// split correctly: arch suffix removed, epoch prefix removed, version is
// version-release.
func TestDnf5Detector_StripsArchAndEpoch(t *testing.T) {
	r := newFakeRunner()
	r.execOut["dnf5 repoquery --installed"] = "cups-filesystem-1:2.4.19-3.fc44.noarch"
	d := NewDnf5Detector()
	pkgs, err := d.Detect(context.Background(), r, "bunker5")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("len = %d, want 1", len(pkgs))
	}
	if pkgs[0].Name != "cups-filesystem" {
		t.Errorf("Name = %q, want cups-filesystem (arch stripped, name keeps hyphens)", pkgs[0].Name)
	}
	if pkgs[0].Version != "2.4.19-3.fc44" {
		t.Errorf("Version = %q, want 2.4.19-3.fc44 (epoch stripped)", pkgs[0].Version)
	}
}

// TestDnf5Detector_DottedNameKeepsDots verifies a package name with internal
// dots (python3.12) is not split on those dots.
func TestDnf5Detector_DottedNameKeepsDots(t *testing.T) {
	r := newFakeRunner()
	r.execOut["dnf5 repoquery --installed"] = "python3.12-0:3.12.6-1.fc44.x86_64"
	d := NewDnf5Detector()
	pkgs, err := d.Detect(context.Background(), r, "bunker5")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("len = %d, want 1", len(pkgs))
	}
	if pkgs[0].Name != "python3.12" {
		t.Errorf("Name = %q, want python3.12 (internal dot preserved)", pkgs[0].Name)
	}
	if pkgs[0].Version != "3.12.6-1.fc44" {
		t.Errorf("Version = %q, want 3.12.6-1.fc44", pkgs[0].Version)
	}
}

// TestDnf5Detector_EmptyOutput verifies empty/whitespace repoquery output
// yields an empty (non-nil) slice with no error.
func TestDnf5Detector_EmptyOutput(t *testing.T) {
	r := newFakeRunner()
	r.execOut["dnf5 repoquery --installed"] = "\n\n"
	d := NewDnf5Detector()
	pkgs, err := d.Detect(context.Background(), r, "bunker5")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if len(pkgs) != 0 {
		t.Errorf("len = %d, want 0 for empty output", len(pkgs))
	}
}

// TestDnf5Detector_ExecFails verifies an exec error surfaces (e.g. dnf5 not
// present or repoquery unsupported).
func TestDnf5Detector_ExecFails(t *testing.T) {
	r := newFakeRunner()
	r.execErr["dnf5 repoquery --installed"] = errors.New("dnf5: command not found")
	d := NewDnf5Detector()
	_, err := d.Detect(context.Background(), r, "bunker5")
	if err == nil {
		t.Fatalf("Detect(dnf5 missing) err = nil, want error")
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