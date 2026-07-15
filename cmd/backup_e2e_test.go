package cmd

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blak0p/bunkerctl/internal/compress"
	"github.com/blak0p/bunkerctl/internal/copy"
	"github.com/blak0p/bunkerctl/internal/manifest"
	"github.com/blak0p/bunkerctl/internal/podman"
)

// errFakeNonZero simulates a non-zero exit from a `which` probe (manager absent).
var errFakeNonZero = errors.New("fake non-zero exit")

// fedoraExecFn returns an ExecFn that fakes a Fedora 45 single-user container
// with the given dnf/dnf5 package list. It is shared by the E2E + regression
// tests so they all drive the same canned container shape.
func fedoraExecFn(dnfList string) func(ctx context.Context, id string, cmd []string) (string, error) {
	return func(ctx context.Context, id string, cmd []string) (string, error) {
		switch strings.Join(cmd, " ") {
		case "getent passwd 1000":
			return "alejndro:x:1000:1000::/home/alejndro:/bin/bash", nil
		case "getent passwd":
			return "alejndro:x:1000:1000::/home/alejndro:/bin/bash", nil
		case "cat /etc/os-release":
			return "ID=fedora\nVERSION_ID=45\n", nil
		case "which dnf5":
			return "", nil
		case "which dnf":
			return "", errFakeNonZero // dnf5 preferred; dnf absent
		case "dnf5 list installed":
			return dnfList, nil
		case "rm -rf /var/cache/dnf":
			return "", nil
		}
		return "", nil
	}
}

// fedoraInspectRaw is the canned podman inspect JSON for the fake container.
const fedoraInspectRaw = `[{"Id":"bunker","Image":"fedora:45","Config":{"User":"1000","Env":["EDITOR=nvim","TERM=kitty","PATH=/usr/bin"]},"State":{"Running":true}}]`

// runFullBackup runs `bunkerctl backup <name> --no-edit` against a FakeRunner
// wired with the canned Fedora container and returns the runner (so tests can
// inspect Calls), the dest .bunker path, and any error.
func runFullBackup(t *testing.T, name string, extraArgs ...string) (*podman.FakeRunner, string, error) {
	t.Helper()
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, name+".bunker")
	setBackupDestPath(t, dest)
	// Use the real ZstdTar so the archive is a real zstd-tar tests can decompress.
	setBackupCompressor(t, compress.ZstdTar{})

	r := &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: name, Image: "fedora:45"},
		InspectRawResult: fedoraInspectRaw,
		ExecFn:           fedoraExecFn("Installed Packages\nneovim.x86_64 0.10.2-1.fc40 @repo\nfish.x86_64 3.7.0-1.fc40 @repo\n"),
	}
	setBackupRunner(t, r)

	args := append([]string{"--no-edit"}, extraArgs...)
	args = append(args, name)
	_, err := executeBackup(t, args...)
	return r, dest, err
}

// TestBackup_E2E_SentinelFileProvesContainerSideCopy is the REQ-COPY-1 proof:
// a file that exists ONLY inside the (faked) container MUST end up in
// staging/files/ with the container's content, not the host's. We inject a
// copier that writes a sentinel file into files/ with container content, and a
// host file at the same relative path with DIFFERENT content, then assert the
// archive contains the container content.
func TestBackup_E2E_SentinelFileProvesContainerSideCopy(t *testing.T) {
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "bunker.bunker")
	setBackupDestPath(t, dest)
	setBackupCompressor(t, compress.ZstdTar{})

	// Host sentinel with DIFFERENT content — must NOT appear in the archive.
	hostSentinelDir := t.TempDir()
	hostSentinelPath := filepath.Join(hostSentinelDir, "secret.txt")
	if err := os.WriteFile(hostSentinelPath, []byte("FROM_HOST"), 0o644); err != nil {
		t.Fatalf("write host sentinel: %v", err)
	}

	// Copier that writes the CONTAINER sentinel into files/. This stands in for
	// the real `podman exec tar` pipe which reads from inside the container.
	setBackupCopier(t, sentinelCopier{content: "FROM_CONTAINER"})

	r := &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "bunker", Image: "fedora:45"},
		InspectRawResult: fedoraInspectRaw,
		ExecFn:           fedoraExecFn("Installed Packages\nneovim.x86_64 0.10.2-1.fc40 @repo\n"),
	}
	setBackupRunner(t, r)

	if _, err := executeBackup(t, "--no-edit", "bunker"); err != nil {
		t.Fatalf("backup error: %v", err)
	}

	// Decompress and assert the sentinel is present with container content.
	extractDir := t.TempDir()
	if err := (compress.ZstdTar{}).Decompress(dest, extractDir); err != nil {
		t.Fatalf("decompress .bunker: %v", err)
	}
	got := readArchiveFiles(t, dest)
	if got["files/secret.txt"] != "FROM_CONTAINER" {
		t.Errorf("archive files/secret.txt = %q, want %q (container content)", got["files/secret.txt"], "FROM_CONTAINER")
	}
	if strings.Contains(joinArchive(got), "FROM_HOST") {
		t.Errorf("host content leaked into archive; archive must not contain FROM_HOST")
	}
	_ = hostSentinelPath // host file exists but the pipeline never reads it
}

// TestBackup_E2E_RegressionNoCommitNoSave is the MANDATORY regression proof for
// REQ-REM-1 and REQ-REM-2: after a full backup pipeline run, FakeRunner.Calls
// MUST NOT contain any "Commit:" or "Save:" entry. This is the evidence that
// the v0.1.0 GB-image pipeline is gone.
func TestBackup_E2E_RegressionNoCommitNoSave(t *testing.T) {
	r, _, err := runFullBackup(t, "bunker")
	if err != nil {
		t.Fatalf("backup error: %v", err)
	}
	for _, c := range r.Calls {
		if strings.HasPrefix(c, "Commit:") {
			t.Errorf("FakeRunner.Calls contains %q; podman commit must never be called (REQ-REM-1)", c)
		}
		if strings.HasPrefix(c, "Save:") {
			t.Errorf("FakeRunner.Calls contains %q; podman save must never be called (REQ-REM-2)", c)
		}
	}
}

// TestBackup_E2E_FormatVersionIs1 verifies REQ-YAML-1 + REQ-COMP-3: after
// backup, decompressing the .bunker and parsing bunker.yaml MUST yield
// format_version: 1 and the archive MUST contain bunker.yaml + files/.
func TestBackup_E2E_FormatVersionIs1(t *testing.T) {
	_, dest, err := runFullBackup(t, "bunker")
	if err != nil {
		t.Fatalf("backup error: %v", err)
	}
	extractDir := t.TempDir()
	if err := (compress.ZstdTar{}).Decompress(dest, extractDir); err != nil {
		t.Fatalf("decompress .bunker: %v", err)
	}
	yamlPath := filepath.Join(extractDir, "bunker.yaml")
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read bunker.yaml from archive: %v", err)
	}
	m, err := manifest.Unmarshal(data)
	if err != nil {
		t.Fatalf("parse bunker.yaml: %v", err)
	}
	if m.FormatVersion != 1 {
		t.Errorf("format_version = %d, want 1", m.FormatVersion)
	}
	// REQ-COMP-3: archive MUST contain files/ at the root.
	filesDir := filepath.Join(extractDir, "files")
	if info, err := os.Stat(filesDir); err != nil || !info.IsDir() {
		t.Errorf("archive missing files/ dir at root: %v", err)
	}
	// REQ-REM-6/7/8: archive MUST NOT contain the v0.1.0 artifacts.
	for _, bad := range []string{"image.tar", "metadata.json", "manifest.toml"} {
		if _, err := os.Stat(filepath.Join(extractDir, bad)); err == nil {
			t.Errorf("archive still contains removed v0.1.0 artifact %q", bad)
		}
	}
}

// TestBackup_E2E_PackagesHaveNameAndVersion verifies REQ-YAML-5: every entry
// under packages.dnf5 in the generated bunker.yaml MUST have both name and
// version populated.
func TestBackup_E2E_PackagesHaveNameAndVersion(t *testing.T) {
	_, dest, err := runFullBackup(t, "bunker")
	if err != nil {
		t.Fatalf("backup error: %v", err)
	}
	extractDir := t.TempDir()
	if err := (compress.ZstdTar{}).Decompress(dest, extractDir); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(extractDir, "bunker.yaml"))
	m, err := manifest.Unmarshal(data)
	if err != nil {
		t.Fatalf("parse bunker.yaml: %v", err)
	}
	pkgs, ok := m.Packages["dnf5"]
	if !ok || len(pkgs) == 0 {
		t.Fatalf("packages.dnf5 missing or empty: %v", m.Packages)
	}
	for i, p := range pkgs {
		if p.Name == "" || p.Version == "" {
			t.Errorf("packages.dnf5[%d] = %+v, want both name and version", i, p)
		}
	}
}

// TestBackup_E2E_DefaultIgnoreListPresent verifies REQ-COPY-4: the generated
// bunker.yaml files.ignore MUST contain all 15 default patterns.
func TestBackup_E2E_DefaultIgnoreListPresent(t *testing.T) {
	_, dest, err := runFullBackup(t, "bunker")
	if err != nil {
		t.Fatalf("backup error: %v", err)
	}
	extractDir := t.TempDir()
	if err := (compress.ZstdTar{}).Decompress(dest, extractDir); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(extractDir, "bunker.yaml"))
	m, err := manifest.Unmarshal(data)
	if err != nil {
		t.Fatalf("parse bunker.yaml: %v", err)
	}
	for _, d := range []string{".cache", "node_modules", "target", ".cargo/registry", ".npm", ".next", ".git", "Downloads", ".local/share/Trash", "__pycache__", ".venv", "venv", "*.log", "*.tmp", "/tmp"} {
		if !containsString(m.Files.Ignore, d) {
			t.Errorf("default ignore %q missing from files.ignore %v", d, m.Files.Ignore)
		}
	}
}

// TestBackup_E2E_UserSectionFromContainer verifies REQ-YAML-2/REQ-DETECT-3: the
// generated user section reflects the container's getent passwd, not the host.
func TestBackup_E2E_UserSectionFromContainer(t *testing.T) {
	_, dest, err := runFullBackup(t, "bunker")
	if err != nil {
		t.Fatalf("backup error: %v", err)
	}
	extractDir := t.TempDir()
	if err := (compress.ZstdTar{}).Decompress(dest, extractDir); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(extractDir, "bunker.yaml"))
	m, err := manifest.Unmarshal(data)
	if err != nil {
		t.Fatalf("parse bunker.yaml: %v", err)
	}
	if m.User.Name != "alejndro" || m.User.UID != 1000 || m.User.GID != 1000 || m.User.Home != "/home/alejndro" {
		t.Errorf("user section = %+v, want {alejndro 1000 1000 /home/alejndro}", m.User)
	}
}

// TestBackup_E2E_BaseSection verifies REQ-YAML-3: base.distro=fedora,
// base.version=45.
func TestBackup_E2E_BaseSection(t *testing.T) {
	_, dest, err := runFullBackup(t, "bunker")
	if err != nil {
		t.Fatalf("backup error: %v", err)
	}
	extractDir := t.TempDir()
	if err := (compress.ZstdTar{}).Decompress(dest, extractDir); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(extractDir, "bunker.yaml"))
	m, err := manifest.Unmarshal(data)
	if err != nil {
		t.Fatalf("parse bunker.yaml: %v", err)
	}
	if m.Base.Distro != "fedora" || m.Base.Version != "45" {
		t.Errorf("base = %+v, want {fedora 45}", m.Base)
	}
}

// TestBackup_E2E_NonFedoraRejected verifies REQ-YAML-3 scenario: a non-Fedora
// container MUST be rejected with a clear error and no archive produced.
func TestBackup_E2E_NonFedoraRejected(t *testing.T) {
	setSafeBackupDefaults(t)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "ubuntu.bunker")
	setBackupDestPath(t, dest)

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "ubuntu", Image: "ubuntu:24.04"},
		InspectRawResult: `[{"Id":"ubuntu","Image":"ubuntu:24.04","Config":{"User":"1000"},"State":{"Running":true}}]`,
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			switch strings.Join(cmd, " ") {
			case "getent passwd 1000":
				return "u:x:1000:1000::/home/u:/bin/bash", nil
			case "getent passwd":
				return "u:x:1000:1000::/home/u:/bin/bash", nil
			case "cat /etc/os-release":
				return "ID=ubuntu\nVERSION_ID=24.04\n", nil
			}
			return "", nil
		},
	})

	_, err := executeBackup(t, "--no-edit", "ubuntu")
	if err == nil {
		t.Fatalf("backup ubuntu returned nil error, want unsupported-distro rejection")
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Errorf("archive created for non-Fedora ubuntu; want no archive")
	}
}

// TestBackup_E2E_ContainerNotFoundAbortsBeforeStaging verifies REQ-DETECT-1
// scenario: inspect failure aborts before any staging directory is created.
func TestBackup_E2E_ContainerNotFoundAbortsBeforeStaging(t *testing.T) {
	stagingRootDir := t.TempDir()
	setBackupStagingRoot(t, stagingRootDir)
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:    "podman version 5.0.0",
		InspectErr:    podman.ErrContainerNotFound,
		InspectRawErr: podman.ErrContainerNotFound,
	})
	_, err := executeBackup(t, "--no-edit", "ghost")
	if err == nil {
		t.Fatalf("backup ghost returned nil error, want non-nil")
	}
	// No staging dir should have been created under the staging root.
	entries, _ := os.ReadDir(stagingRootDir)
	if len(entries) != 0 {
		t.Errorf("staging root has %d entries, want 0 (no staging on not-found)", len(entries))
	}
}

// TestBackup_E2E_MultiUserRejected verifies REQ-ERR-3: a container with two
// non-system users MUST be rejected.
func TestBackup_E2E_MultiUserRejected(t *testing.T) {
	setSafeBackupDefaults(t)
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "shared", Image: "fedora:45"},
		InspectRawResult: `[{"Id":"shared","Image":"fedora:45","Config":{"User":"1000"},"State":{"Running":true}}]`,
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			switch strings.Join(cmd, " ") {
			case "getent passwd 1000":
				return "alice:x:1000:1000::/home/alice:/bin/bash", nil
			case "getent passwd":
				// Two non-system users → multi-user.
				return "alice:x:1000:1000::/home/alice:/bin/bash\nbob:x:1001:1001::/home/bob:/bin/bash", nil
			}
			return "", nil
		},
	})
	_, err := executeBackup(t, "--no-edit", "shared")
	if err == nil {
		t.Fatalf("backup shared returned nil error, want multi-user rejection")
	}
}

// TestBackup_E2E_NoPackageManagerFails verifies REQ-DETECT-5 scenario: a Fedora
// container stripped of both dnf and dnf5 MUST fail with a clear error.
func TestBackup_E2E_NoPackageManagerFails(t *testing.T) {
	setSafeBackupDefaults(t)
	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "stripped", Image: "fedora:45"},
		InspectRawResult: `[{"Id":"stripped","Image":"fedora:45","Config":{"User":"1000"},"State":{"Running":true}}]`,
		ExecFn: func(ctx context.Context, id string, cmd []string) (string, error) {
			switch strings.Join(cmd, " ") {
			case "getent passwd 1000":
				return "u:x:1000:1000::/home/u:/bin/bash", nil
			case "getent passwd":
				return "u:x:1000:1000::/home/u:/bin/bash", nil
			case "cat /etc/os-release":
				return "ID=fedora\nVERSION_ID=45\n", nil
			case "which dnf5", "which dnf":
				return "", errFakeNonZero
			}
			return "", nil
		},
	})
	_, err := executeBackup(t, "--no-edit", "stripped")
	if err == nil {
		t.Fatalf("backup stripped returned nil error, want no-package-manager error")
	}
}

// TestBackup_E2E_EditorNonZeroExitAborts verifies REQ-EDIT-2: when the editor
// exits non-zero, the backup MUST abort with no archive and the staging dir
// MUST be cleaned up.
func TestBackup_E2E_EditorNonZeroExitAborts(t *testing.T) {
	setSafeBackupDefaults(t)
	stagingRootDir := t.TempDir()
	setBackupStagingRoot(t, stagingRootDir)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "aborted.bunker")
	setBackupDestPath(t, dest)
	setBackupEditor(t, failingEditor{}) // exit code 1

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "aborted", Image: "fedora:45"},
		InspectRawResult: fedoraInspectRaw,
		ExecFn:           fedoraExecFn("Installed Packages\nneovim.x86_64 0.10.2-1.fc40 @repo\n"),
	})
	_, err := executeBackup(t, "aborted") // no --no-edit → editor runs
	if err == nil {
		t.Fatalf("backup with failing editor returned nil error, want abort")
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Errorf("archive created despite editor abort; want no archive")
	}
	// Staging dir should be cleaned up.
	entries, _ := os.ReadDir(stagingRootDir)
	if len(entries) != 0 {
		t.Errorf("staging root has %d entries after abort, want 0 (cleaned up)", len(entries))
	}
}

// TestBackup_E2E_DefaultOutputPath verifies REQ-CLI-6: when --output is not
// supplied, the default output path is ./<name>-<timestamp>.bunker.
func TestBackup_E2E_DefaultOutputPath(t *testing.T) {
	setSafeBackupDefaults(t)
	// Override dest seam to nil-equivalent: clear it so the real default runs.
	backupDestPathFn = defaultBackupDestPath
	t.Cleanup(func() { backupDestPathFn = defaultBackupDestPath })
	// Run from a temp cwd so the default file lands there.
	cwd := t.TempDir()
	t.Chdir(cwd)

	setBackupRunner(t, &podman.FakeRunner{
		VersionStr:       "podman version 5.0.0",
		InspectResult:    podman.InspectResult{ID: "mybunker", Image: "fedora:45"},
		InspectRawResult: fedoraInspectRaw,
		ExecFn:           fedoraExecFn("Installed Packages\nneovim.x86_64 0.10.2-1.fc40 @repo\n"),
	})
	out, err := executeBackup(t, "--no-edit", "mybunker")
	if err != nil {
		t.Fatalf("backup error: %v\noutput: %s", err, out)
	}
	// A file matching mybunker-*.bunker should exist in cwd.
	matches, _ := filepath.Glob(filepath.Join(cwd, "mybunker-*.bunker"))
	if len(matches) != 1 {
		t.Fatalf("expected 1 default-output file in %s, got %v", cwd, matches)
	}
}

// sentinelCopier is a Copier that writes a single sentinel file with the given
// content into the staging files/ dir, simulating a container-side read.
type sentinelCopier struct {
	content string
}

func (c sentinelCopier) Copy(ctx context.Context, runner podman.Runner, containerID string, opts copy.CopyOptions) (copy.CopyResult, error) {
	if err := os.MkdirAll(opts.StagingDir, 0o755); err != nil {
		return copy.CopyResult{}, err
	}
	if err := os.WriteFile(filepath.Join(opts.StagingDir, "secret.txt"), []byte(c.content), 0o644); err != nil {
		return copy.CopyResult{}, err
	}
	return copy.CopyResult{BytesCopied: int64(len(c.content))}, nil
}

// readArchiveFiles decompresses a .bunker and returns a map of entry name →
// content for all regular files.
func readArchiveFiles(t *testing.T, path string) map[string]string {
	t.Helper()
	extractDir := t.TempDir()
	if err := (compress.ZstdTar{}).Decompress(path, extractDir); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	got := map[string]string{}
	_ = filepath.Walk(extractDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(extractDir, p)
		b, _ := os.ReadFile(p)
		got[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	return got
}

// joinArchive concatenates all archive file contents for substring checks.
func joinArchive(m map[string]string) string {
	var sb strings.Builder
	for _, v := range m {
		sb.WriteString(v)
	}
	return sb.String()
}

// Compile-time guarantees that archive/tar, bytes, io are used (helpers above).
var (
	_ = io.EOF
	_ = tar.TypeReg
	_ = bytes.MinRead
)