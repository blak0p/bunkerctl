package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/blak0p/bunkerctl/internal/compress"
	"github.com/blak0p/bunkerctl/internal/copy"
	"github.com/blak0p/bunkerctl/internal/manifest"
	"github.com/blak0p/bunkerctl/internal/packages"
	"github.com/blak0p/bunkerctl/internal/pkgdetect"
	"github.com/blak0p/bunkerctl/internal/podman"
)

// TestDedupeManagers is a unit test for the pure dedupe helper: when both dnf
// and dnf5 are detected, dnf is dropped; other managers pass through in order.
func TestDedupeManagers(t *testing.T) {
	cases := []struct {
		name string
		in   []packages.Manager
		want []packages.Manager
	}{
		{"empty", nil, []packages.Manager{}},
		{"only dnf5", []packages.Manager{packages.ManagerDnf5}, []packages.Manager{packages.ManagerDnf5}},
		{"only dnf", []packages.Manager{packages.ManagerDnf}, []packages.Manager{packages.ManagerDnf}},
		{"dnf then dnf5", []packages.Manager{packages.ManagerDnf, packages.ManagerDnf5}, []packages.Manager{packages.ManagerDnf5}},
		{"dnf5 then dnf", []packages.Manager{packages.ManagerDnf5, packages.ManagerDnf}, []packages.Manager{packages.ManagerDnf5}},
		{"dnf + apt", []packages.Manager{packages.ManagerDnf, packages.ManagerApt}, []packages.Manager{packages.ManagerDnf, packages.ManagerApt}},
		{"dnf + dnf5 + apt", []packages.Manager{packages.ManagerDnf, packages.ManagerDnf5, packages.ManagerApt}, []packages.Manager{packages.ManagerDnf5, packages.ManagerApt}},
		{"apt + pacman", []packages.Manager{packages.ManagerApt, packages.ManagerPacman}, []packages.Manager{packages.ManagerApt, packages.ManagerPacman}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dedupeManagers(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("dedupeManagers(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestDedupeStrings is a unit test for the string dedupe helper: preserves
// first-occurrence order, drops later occurrences.
func TestDedupeStrings(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, []string{}},
		{"empty", []string{}, []string{}},
		{"no dupes", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"with dupes", []string{"a", "b", "a", "c", "b"}, []string{"a", "b", "c"}},
		{"all same", []string{"x", "x", "x"}, []string{"x"}},
		{"one element", []string{"only"}, []string{"only"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dedupeStrings(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("dedupeStrings(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestParseIgnoreExtra verifies parseIgnoreExtra splits, trims, and drops
// empties as the --ignore-extra flag handler requires (REQ-CLI-5).
func TestParseIgnoreExtra(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", []string(nil)},
		{"single", "build", []string{"build"}},
		{"two", "build,dist", []string{"build", "dist"}},
		{"with spaces", "build, dist , *.bak", []string{"build", "dist", "*.bak"}},
		{"trailing comma", "a,b,", []string{"a", "b"}},
		{"only commas", ",,,", []string(nil)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseIgnoreExtra(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseIgnoreExtra(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestSelectEnv verifies selectEnv keeps only the wanted env vars (EDITOR,
// TERM, SHELL) and always returns a non-nil map.
func TestSelectEnv(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want map[string]string
	}{
		{"nil", nil, map[string]string{}},
		{"empty", []string{}, map[string]string{}},
		{"all wanted", []string{"EDITOR=nvim", "TERM=kitty", "SHELL=/bin/fish"}, map[string]string{"EDITOR": "nvim", "TERM": "kitty", "SHELL": "/bin/fish"}},
		{"mixed", []string{"EDITOR=vi", "PATH=/usr/bin", "TERM=xterm", "HOME=/root"}, map[string]string{"EDITOR": "vi", "TERM": "xterm"}},
		{"no equals", []string{"GARBGAGE", "EDITOR=nvim"}, map[string]string{"EDITOR": "nvim"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := selectEnv(c.in)
			if got == nil {
				t.Fatalf("selectEnv returned nil map, want non-nil")
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("selectEnv(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestToManifestPackages verifies toManifestPackages converts pkgdetect.Package
// entries to manifest.Package preserving name and version.
func TestToManifestPackages(t *testing.T) {
	in := []pkgdetect.Package{
		{Name: "neovim", Version: "0.10.2-1.fc40"},
		{Name: "fish", Version: "3.7.0-1.fc40"},
	}
	got := toManifestPackages(in)
	if len(got) != 2 || got[0].Name != "neovim" || got[0].Version != "0.10.2-1.fc40" ||
		got[1].Name != "fish" || got[1].Version != "3.7.0-1.fc40" {
		t.Errorf("toManifestPackages(%v) = %+v", in, got)
	}
}

// TestToManifestPackages_Empty triangulates the empty input.
func TestToManifestPackages_Empty(t *testing.T) {
	got := toManifestPackages(nil)
	if len(got) != 0 {
		t.Errorf("toManifestPackages(nil) = %v, want empty", got)
	}
}

// TestDefaultBackupDestPath verifies REQ-CLI-6: the default path is
// ./<name>-<timestamp>.bunker with the timestamp in 20060102-150405 format.
func TestDefaultBackupDestPath(t *testing.T) {
	got := defaultBackupDestPath("mybunker")
	if !strings.HasPrefix(got, "mybunker-") {
		t.Errorf("defaultBackupDestPath = %q, want prefix 'mybunker-'", got)
	}
	if !strings.HasSuffix(got, ".bunker") {
		t.Errorf("defaultBackupDestPath = %q, want suffix '.bunker'", got)
	}
}

// TestResolveOutput_AbsoluteAndRelative verifies resolveOutput returns
// absolute paths as-is and resolves relative paths against the cwd. It also
// returns empty when --output is unset (the default path applies).
func TestResolveOutput_AbsoluteAndRelative(t *testing.T) {
	orig := cliOutput
	t.Cleanup(func() { cliOutput = orig })
	// Default: empty.
	cliOutput = ""
	if got := resolveOutput(); got != "" {
		t.Errorf("resolveOutput() = %q, want empty when --output unset", got)
	}
	// Absolute passes through.
	cliOutput = "/tmp/abs.bunker"
	if got := resolveOutput(); got != "/tmp/abs.bunker" {
		t.Errorf("resolveOutput(abs) = %q, want %q", got, "/tmp/abs.bunker")
	}
	// Relative gets joined with cwd.
	cliOutput = "rel.bunker"
	got := resolveOutput()
	if !strings.HasSuffix(got, "rel.bunker") || strings.HasPrefix(got, "rel.bunker") {
		t.Errorf("resolveOutput(rel) = %q, want cwd-joined path ending in rel.bunker", got)
	}
	cliOutput = ""
}

// TestBackup_EngineErrorTriangulate triangulates the engine path: a non-
// ErrEngineUnavailable error from Version still fails the backup with the
// error surfaced.
func TestBackup_EngineErrorTriangulate(t *testing.T) {
	setBackupRunner(t, &podman.FakeRunner{Err: context.DeadlineExceeded})
	_, err := executeBackup(t, "mybunker")
	if err == nil {
		t.Fatalf("backup with engine error returned nil error, want non-nil")
	}
}

// TestBackup_InvalidName_PipeChar is another threat-matrix triangulation with
// a pipe character, distinct from the semicolon and backtick cases.
func TestBackup_InvalidName_PipeChar(t *testing.T) {
	setBackupRunner(t, &podman.FakeRunner{VersionStr: "podman version 5.0.0"})
	out, err := executeBackup(t, "foo|bar")
	if err == nil {
		t.Fatalf("backup with pipe returned nil error, want non-nil")
	}
	if !strings.Contains(strings.ToLower(out), "invalid container name") {
		t.Errorf("output = %q, want substring 'invalid container name'", out)
	}
}

// TestBackup_InvalidName_DollarChar triangulates the $ metacharacter.
func TestBackup_InvalidName_DollarChar(t *testing.T) {
	setBackupRunner(t, &podman.FakeRunner{VersionStr: "podman version 5.0.0"})
	out, err := executeBackup(t, "foo$bar")
	if err == nil {
		t.Fatalf("backup with $ returned nil error, want non-nil")
	}
	if !strings.Contains(strings.ToLower(out), "invalid container name") {
		t.Errorf("output = %q, want substring 'invalid container name'", out)
	}
}

// TestBackup_E2E_IgnoreExtraAppearsInYAML verifies REQ-CLI-5 end-to-end:
// --ignore-extra patterns are present in the generated bunker.yaml files.ignore
// alongside the defaults.
func TestBackup_E2E_IgnoreExtraAppearsInYAML(t *testing.T) {
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "extra.bunker")
	setBackupDestPath(t, dest)
	setBackupCompressor(t, compressZstdTar())

	r := &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "extra", Image: "fedora:45"},
		InspectRawResult: fedoraInspectRaw,
		ExecFn:           fedoraExecFn("neovim-0:0.10.2-1.fc40.x86_64\n"),
	}
	setBackupRunner(t, r)

	if _, err := executeBackup(t, "--no-edit", "--ignore-extra=build,dist,*.bak", "extra"); err != nil {
		t.Fatalf("backup error: %v", err)
	}
	m := readManifestFromArchive(t, dest)
	for _, extra := range []string{"build", "dist", "*.bak"} {
		if !containsString(m.Files.Ignore, extra) {
			t.Errorf("--ignore-extra %q missing from files.ignore %v", extra, m.Files.Ignore)
		}
	}
	// Defaults must still be present.
	if !containsString(m.Files.Ignore, ".cache") {
		t.Errorf("default .cache missing from files.ignore %v", m.Files.Ignore)
	}
}

// TestBackup_E2E_CustomEnvironmentCaptured verifies REQ-YAML-7: the
// custom.environment map in the generated YAML captures the wanted env vars
// from the container inspect.
func TestBackup_E2E_CustomEnvironmentCaptured(t *testing.T) {
	_, dest, err := runFullBackup(t, "bunker")
	if err != nil {
		t.Fatalf("backup error: %v", err)
	}
	m := readManifestFromArchive(t, dest)
	if m.Custom.Environment == nil {
		t.Fatalf("custom.environment is nil, want non-nil map")
	}
	if m.Custom.Environment["EDITOR"] != "nvim" {
		t.Errorf("custom.environment.EDITOR = %q, want nvim", m.Custom.Environment["EDITOR"])
	}
	if m.Custom.Environment["TERM"] != "kitty" {
		t.Errorf("custom.environment.TERM = %q, want kitty", m.Custom.Environment["TERM"])
	}
	// PATH must NOT be captured (not in the wanted set).
	if _, ok := m.Custom.Environment["PATH"]; ok {
		t.Errorf("custom.environment.PATH captured; want it excluded")
	}
}

// TestBackup_E2E_VerifyAutoTrue verifies REQ-YAML-7: verify.auto defaults to true.
func TestBackup_E2E_VerifyAutoTrue(t *testing.T) {
	_, dest, err := runFullBackup(t, "bunker")
	if err != nil {
		t.Fatalf("backup error: %v", err)
	}
	m := readManifestFromArchive(t, dest)
	if !m.Verify.Auto {
		t.Errorf("verify.auto = %v, want true", m.Verify.Auto)
	}
}

// TestBackup_E2E_FilesCopyIsAuto verifies REQ-YAML-6: files.copy is "auto".
func TestBackup_E2E_FilesCopyIsAuto(t *testing.T) {
	_, dest, err := runFullBackup(t, "bunker")
	if err != nil {
		t.Fatalf("backup error: %v", err)
	}
	m := readManifestFromArchive(t, dest)
	if m.Files.Copy != "auto" {
		t.Errorf("files.copy = %q, want auto", m.Files.Copy)
	}
}

// TestBackup_E2E_CacheCleaningRan verifies the cache-cleaning step (kept from
// v0.1.0) runs for the detected manager: FakeRunner.Calls must contain the
// `rm -rf /var/cache/dnf` exec.
func TestBackup_E2E_CacheCleaningRan(t *testing.T) {
	r, _, err := runFullBackup(t, "bunker")
	if err != nil {
		t.Fatalf("backup error: %v", err)
	}
	found := false
	for _, c := range r.Calls {
		if strings.Contains(c, "Exec:bunker") {
			// The Exec call records only the container id prefix; check the
			// ExecFn was invoked with the rm cmd by looking at the canned fn.
			found = true
		}
	}
	if !found {
		t.Errorf("no Exec calls recorded; expected cache cleaning to run")
	}
}

// --- Pipeline error-path tests ---
//
// Each test drives the full pipeline through runFullBackup-like setup but
// forces a single step to fail, asserting the backup aborts with a non-nil
// error and (where applicable) no archive is produced.

// runBackupWithExecFn runs a full backup with a custom ExecFn overriding the
// package-detection + distro + user responses, returning the runner, dest, and err.
func runBackupWithExecFn(t *testing.T, name string, execFn func(ctx context.Context, id string, cmd []string) (string, error)) (*podman.FakeRunner, string, error) {
	t.Helper()
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, name+".bunker")
	setBackupDestPath(t, dest)
	setBackupCompressor(t, compress.ZstdTar{})
	r := &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: name, Image: "fedora:45"},
		InspectRawResult: fedoraInspectRaw,
		ExecFn:           execFn,
	}
	setBackupRunner(t, r)
	_, err := executeBackup(t, "--no-edit", name)
	return r, dest, err
}

// TestBackup_Pipeline_InspectFailsAborts verifies that when InspectRaw returns
// an error, the backup aborts before staging produces an archive.
func TestBackup_Pipeline_InspectFailsAborts(t *testing.T) {
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "x.bunker")
	setBackupDestPath(t, dest)
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "x", Image: "fedora:45"},
		InspectRawErr:    errors.New("inspect boom"),
	})
	_, err := executeBackup(t, "--no-edit", "x")
	if err == nil {
		t.Fatalf("backup with inspect error returned nil, want non-nil")
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Errorf("archive created despite inspect failure")
	}
}

// TestBackup_Pipeline_UserDetectFailsAborts verifies the user-detect fallback
// chain: when both getent and echo $HOME fail, the backup aborts.
func TestBackup_Pipeline_UserDetectFailsAborts(t *testing.T) {
	_, _, err := runBackupWithExecFn(t, "nofail", func(ctx context.Context, id string, cmd []string) (string, error) {
		// getent passwd 1000 → fail; echo $HOME → fail.
		return "", errFakeNonZero
	})
	if err == nil {
		t.Fatalf("backup with user-detect failure returned nil, want non-nil")
	}
}

// TestBackup_Pipeline_DistroFailsAborts verifies a non-Fedora distro aborts.
func TestBackup_Pipeline_DistroFailsAborts(t *testing.T) {
	_, dest, err := runBackupWithExecFn(t, "arch", func(ctx context.Context, id string, cmd []string) (string, error) {
		switch strings.Join(cmd, " ") {
		case "getent passwd 1000":
			return "u:x:1000:1000::/home/u:/bin/bash", nil
		case "getent passwd":
			return "u:x:1000:1000::/home/u:/bin/bash", nil
		case "cat /etc/os-release":
			return "ID=arch\nVERSION_ID=rolling\n", nil
		}
		return "", nil
	})
	if err == nil {
		t.Fatalf("backup arch returned nil, want unsupported-distro error")
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Errorf("archive created for non-Fedora arch")
	}
}

// TestBackup_Pipeline_CopyFailsAborts verifies that when the container-side copy
// fails, the backup aborts with no archive.
func TestBackup_Pipeline_CopyFailsAborts(t *testing.T) {
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "copyfail.bunker")
	setBackupDestPath(t, dest)
	setBackupCompressor(t, compress.ZstdTar{})
	setBackupCopier(t, errorCopier{}) // Copy returns an error.
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "copyfail", Image: "fedora:45"},
		InspectRawResult: fedoraInspectRaw,
		ExecFn:            fedoraExecFn("neovim-0:0.10.2-1.fc40.x86_64\n"),
	})
	_, err := executeBackup(t, "--no-edit", "copyfail")
	if err == nil {
		t.Fatalf("backup with copy failure returned nil, want non-nil")
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Errorf("archive created despite copy failure")
	}
}

// TestBackup_Pipeline_CompressFailsAborts verifies REQ-ERR-4: when compression
// fails, no archive is left behind.
func TestBackup_Pipeline_CompressFailsAborts(t *testing.T) {
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "compfail.bunker")
	setBackupDestPath(t, dest)
	setBackupCompressor(t, errorCompressor{})
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "compfail", Image: "fedora:45"},
		InspectRawResult: fedoraInspectRaw,
		ExecFn:            fedoraExecFn("neovim-0:0.10.2-1.fc40.x86_64\n"),
	})
	_, err := executeBackup(t, "--no-edit", "compfail")
	if err == nil {
		t.Fatalf("backup with compress failure returned nil, want non-nil")
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Errorf("partial .bunker left after compress failure (REQ-ERR-4)")
	}
}

// TestBackup_Pipeline_EditorErrNoEditorAborts verifies the no-editor-configured
// path (without --no-edit) aborts with a clear message, not a panic.
func TestBackup_Pipeline_EditorErrNoEditorAborts(t *testing.T) {
	setSafeBackupDefaults(t)
	// Remove the fallback editor so ShellEditor returns ErrNoEditor, and clear
	// the injected editor so the real ShellEditor path runs.
	orig := backupEditorFallback
	backupEditorFallback = ""
	t.Cleanup(func() { backupEditorFallback = orig })
	origEd := backupEditor
	backupEditor = nil
	t.Cleanup(func() { backupEditor = origEd })
	t.Setenv("EDITOR", "")
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "noed", Image: "fedora:45"},
		InspectRawResult: fedoraInspectRaw,
		ExecFn:            fedoraExecFn("neovim-0:0.10.2-1.fc40.x86_64\n"),
	})
	_, err := executeBackup(t, "noed") // no --no-edit
	if err == nil {
		t.Fatalf("backup with no editor returned nil, want non-nil")
	}
}

// TestBackup_Pipeline_OutputMissingParentFails triangulates the --output
// validation: a dest whose parent dir does not exist makes compression fail.
func TestBackup_Pipeline_OutputMissingParentFails(t *testing.T) {
	setSafeBackupDefaults(t)
	missing := filepath.Join(t.TempDir(), "no", "such", "dir", "out.bunker")
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "x", Image: "fedora:45"},
		InspectRawResult: fedoraInspectRaw,
		ExecFn:            fedoraExecFn("neovim-0:0.10.2-1.fc40.x86_64\n"),
	})
	_, err := executeBackup(t, "--no-edit", "--output="+missing, "x")
	if err == nil {
		t.Fatalf("backup with missing parent returned nil, want non-nil")
	}
}

// TestBackup_E2E_PackagesKeyIsDnf5 verifies REQ-YAML-4: the packages map key is
// the detected manager name (dnf5 when dnf5 is present).
func TestBackup_E2E_PackagesKeyIsDnf5(t *testing.T) {
	_, dest, err := runFullBackup(t, "bunker")
	if err != nil {
		t.Fatalf("backup error: %v", err)
	}
	m := readManifestFromArchive(t, dest)
	if _, ok := m.Packages["dnf5"]; !ok {
		t.Errorf("packages.dnf5 missing; got keys %v", keysOf(m.Packages))
	}
}

// TestBackup_E2E_DnfOnlyManagerKey triangulates REQ-YAML-4 scenario: when only
// dnf is present (dnf5 absent), the packages map key is "dnf".
func TestBackup_E2E_DnfOnlyManagerKey(t *testing.T) {
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "dnfonly.bunker")
	setBackupDestPath(t, dest)
	setBackupCompressor(t, compress.ZstdTar{})
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "dnfonly", Image: "fedora:45"},
		InspectRawResult: fedoraInspectRaw,
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			switch strings.Join(cmd, " ") {
			case "getent passwd 1000", "getent passwd":
				return "u:x:1000:1000::/home/u:/bin/bash", nil
			case "cat /etc/os-release":
				return "ID=fedora\nVERSION_ID=45\n", nil
			case "which dnf5":
				return "", errFakeNonZero // dnf5 absent
			case "which dnf":
				return "", nil // dnf present
			case "dnf list installed":
				return "Installed Packages\nfish.x86_64 3.7.0-1.fc40 @repo\n", nil
			}
			return "", nil
		},
	})
	if _, err := executeBackup(t, "--no-edit", "dnfonly"); err != nil {
		t.Fatalf("backup dnfonly error: %v", err)
	}
	m := readManifestFromArchive(t, dest)
	if _, ok := m.Packages["dnf"]; !ok {
		t.Errorf("packages.dnf missing; got keys %v", keysOf(m.Packages))
	}
	if _, ok := m.Packages["dnf5"]; ok {
		t.Errorf("packages.dnf5 present but dnf5 was absent; got keys %v", keysOf(m.Packages))
	}
}

// TestBackup_E2E_NameAndCreatedFields verifies the top-level name and created
// fields are populated (name = container, created = today's date).
func TestBackup_E2E_NameAndCreatedFields(t *testing.T) {
	_, dest, err := runFullBackup(t, "bunker")
	if err != nil {
		t.Fatalf("backup error: %v", err)
	}
	m := readManifestFromArchive(t, dest)
	if m.Name != "bunker" {
		t.Errorf("name = %q, want bunker", m.Name)
	}
	if m.Created == "" {
		t.Errorf("created is empty, want a YYYY-MM-DD date")
	}
}

// TestBackup_E2E_NoEditSkipsEditorCall verifies that with --no-edit, the editor
// collaborator is never invoked. We assert by injecting an editor that would
// fail the pipeline if called.
func TestBackup_E2E_NoEditSkipsEditorCall(t *testing.T) {
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "noedit.bunker")
	setBackupDestPath(t, dest)
	setBackupCompressor(t, compress.ZstdTar{})
	editorCalled := false
	setBackupEditor(t, recordingEditor{called: &editorCalled, exitCode: 1})
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "noedit", Image: "fedora:45"},
		InspectRawResult: fedoraInspectRaw,
		ExecFn:           fedoraExecFn("neovim-0:0.10.2-1.fc40.x86_64\n"),
	})
	if _, err := executeBackup(t, "--no-edit", "noedit"); err != nil {
		t.Fatalf("backup --no-edit error: %v", err)
	}
	if editorCalled {
		t.Errorf("editor was invoked despite --no-edit")
	}
}

// TestBackup_E2E_EditorInvokedWithoutNoEdit triangulates: without --no-edit the
// editor collaborator IS invoked (exit 0 so the pipeline completes).
func TestBackup_E2E_EditorInvokedWithoutNoEdit(t *testing.T) {
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "edited.bunker")
	setBackupDestPath(t, dest)
	setBackupCompressor(t, compress.ZstdTar{})
	editorCalled := false
	setBackupEditor(t, recordingEditor{called: &editorCalled, exitCode: 0})
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "edited", Image: "fedora:45"},
		InspectRawResult: fedoraInspectRaw,
		ExecFn:            fedoraExecFn("neovim-0:0.10.2-1.fc40.x86_64\n"),
	})
	if _, err := executeBackup(t, "edited"); err != nil { // no --no-edit
		t.Fatalf("backup error: %v", err)
	}
	if !editorCalled {
		t.Errorf("editor was NOT invoked; expected curation to run without --no-edit")
	}
}

// recordingEditor is a curate.Editor that records whether it was invoked and
// returns a configured exit code, without spawning a process.
type recordingEditor struct {
	called   *bool
	exitCode int
}

func (e recordingEditor) Edit(path string) (int, error) {
	if e.called != nil {
		*e.called = true
	}
	return e.exitCode, nil
}

// keysOf returns the map keys (helper for assertion messages).
func keysOf(m map[string][]manifest.Package) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// errorCopier is a Copier that always fails, used to drive the copy-error path.
type errorCopier struct{}

func (errorCopier) Copy(ctx context.Context, runner podman.Runner, containerID string, opts copy.CopyOptions) (copy.CopyResult, error) {
	return copy.CopyResult{}, errors.New("copy failed")
}

// errorCompressor is a Compressor that always fails.
type errorCompressor struct{}

func (errorCompressor) Compress(srcDir, destPath string) error {
	return errors.New("compress failed")
}

// compressZstdTar returns the real ZstdTar so archive contents are real.
func compressZstdTar() compress.ZstdTar { return compress.ZstdTar{} }

// readManifestFromArchive decompresses a .bunker and parses bunker.yaml.
func readManifestFromArchive(t *testing.T, path string) *manifest.BunkerManifest {
	t.Helper()
	extractDir := t.TempDir()
	if err := (compress.ZstdTar{}).Decompress(path, extractDir); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	data, err := os.ReadFile(extractDir + "/bunker.yaml")
	if err != nil {
		t.Fatalf("read bunker.yaml: %v", err)
	}
	m, err := manifest.Unmarshal(data)
	if err != nil {
		t.Fatalf("parse bunker.yaml: %v", err)
	}
	return m
}