// Package copy copies files from INSIDE a running container to a host staging
// directory via a tar pipe through `podman exec`. This is the fix for the
// v0.1.0 host-side preserve-list bug (REQ-COPY-1): bytes originate from the
// container, never from the host filesystem.
//
// DefaultCopier runs
// `podman exec <id> sh -c 'tar -cf - -C <home> --exclude-from <f> . 2>/dev/null'`
// through podman.Runner.Exec, captures the tar bytes, writes them to a temp
// file, and extracts them with the host `tar -xf -` into <staging>/files. The
// stderr redirect is required because Runner.Exec returns CombinedOutput and tar
// warnings such as "socket ignored" would otherwise corrupt the archive stream.
// The ignore list is written to a temp file and passed via --exclude-from so the
// filtering happens container-side (REQ-COPY-2). After extraction, the total
// bytes are measured; a warning is returned if they exceed 500MB (REQ-COPY-3).
package copy

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/blak0p/bunkerctl/internal/podman"
)

// shellQuote returns a single-quoted shell literal for s. If s contains a
// single quote, it is escaped by ending the quoted string, injecting an
// escaped quote, and resuming the quote: that's the only special character
// inside single-quoted POSIX strings.
func shellQuote(s string) string {
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// largeCopyThreshold is the byte count above which Copy returns a warning
// (REQ-COPY-3). 500 MB.
const largeCopyThreshold = 500 * 1024 * 1024

// Copier copies files from inside a container to a host staging directory.
type Copier interface {
	Copy(ctx context.Context, runner podman.Runner, containerID string, opts CopyOptions) (CopyResult, error)
}

// CopyOptions configures a Copy call.
type CopyOptions struct {
	Home       string   // container home directory (e.g. /home/alejndro)
	CopyEtc    []string // extra absolute container paths (reserved for future SDDs)
	Ignore     []string // patterns to exclude via tar --exclude-from
	StagingDir string   // host path to extract files into (e.g. <staging>/files)
}

// CopyResult reports the outcome of a Copy call.
type CopyResult struct {
	BytesCopied int64  // total bytes of extracted files
	Warning     string // non-empty when BytesCopied > largeCopyThreshold
}

// ErrCopyFailed is returned when the tar pipe or extraction fails.
var ErrCopyFailed = errors.New("container file copy failed")

// DefaultCopier runs the container-side tar command and extracts on the host.
type DefaultCopier struct {
	// SizeOf, when non-nil, overrides the default directory-size walker. Tests
	// inject this to simulate large copies without writing 500MB to disk.
	SizeOf func(root string) (int64, error)
}

// Compile-time guarantee.
var _ Copier = DefaultCopier{}

// Copy runs
// `podman exec <id> sh -c 'tar -cf - -C <home> --exclude-from <f> . 2>/dev/null'`,
// writes the captured tar bytes to a temp file, and extracts them into
// opts.StagingDir using the host `tar -xf -`. It returns the total extracted
// bytes and a warning if they exceed 500MB (REQ-COPY-3).
func (c DefaultCopier) Copy(ctx context.Context, runner podman.Runner, containerID string, opts CopyOptions) (CopyResult, error) {
	if opts.StagingDir == "" {
		return CopyResult{}, fmt.Errorf("%w: empty staging dir", ErrCopyFailed)
	}
	if opts.Home == "" {
		return CopyResult{}, fmt.Errorf("%w: empty home dir", ErrCopyFailed)
	}
	if err := os.MkdirAll(opts.StagingDir, 0o755); err != nil {
		return CopyResult{}, fmt.Errorf("%w: creating staging dir: %v", ErrCopyFailed, err)
	}

	// Build the tar command inside a shell so we can redirect stderr to
	// /dev/null. podman exec runs commands without a shell by default, so we
	// wrap the tar invocation in `sh -c '... 2>/dev/null'`. This prevents tar
	// warnings (e.g. "socket ignored") from corrupting the tar byte stream
	// captured via Runner.Exec, which returns CombinedOutput.
	var shellCmd strings.Builder
	shellCmd.WriteString("tar -cf - -C ")
	shellCmd.WriteString(shellQuote(opts.Home))
	if len(opts.Ignore) > 0 {
		ef, err := writeExcludeFile(opts.Ignore)
		if err != nil {
			return CopyResult{}, fmt.Errorf("%w: writing exclude file: %v", ErrCopyFailed, err)
		}
		defer os.Remove(ef)
		shellCmd.WriteString(" --exclude-from ")
		shellCmd.WriteString(shellQuote(ef))
	}
	shellCmd.WriteString(" . 2>/dev/null")
	cmd := []string{"sh", "-c", shellCmd.String()}

	// Execute inside the container. Runner.Exec returns the tar bytes as a
	// string; Go strings are byte-sequences, so []byte(out) is lossless even
	// for binary tar content. Because stderr is discarded inside the
	// container, only the tar stream reaches us.
	out, err := runner.Exec(ctx, containerID, cmd)
	if err != nil {
		return CopyResult{}, fmt.Errorf("%w: podman exec tar: %v", ErrCopyFailed, err)
	}

	// Write the tar bytes to a temp file and extract with host tar.
	tarFile, err := os.CreateTemp("", "bunker-copy-*.tar")
	if err != nil {
		return CopyResult{}, fmt.Errorf("%w: creating temp tar: %v", ErrCopyFailed, err)
	}
	defer os.Remove(tarFile.Name())
	if _, err := tarFile.WriteString(out); err != nil {
		tarFile.Close()
		return CopyResult{}, fmt.Errorf("%w: writing temp tar: %v", ErrCopyFailed, err)
	}
	if err := tarFile.Close(); err != nil {
		return CopyResult{}, fmt.Errorf("%w: closing temp tar: %v", ErrCopyFailed, err)
	}

	extractCmd := exec.CommandContext(ctx, "tar", "-xf", tarFile.Name(), "-C", opts.StagingDir)
	if extractOut, err := extractCmd.CombinedOutput(); err != nil {
		return CopyResult{}, fmt.Errorf("%w: host tar extract: %v\n%s", ErrCopyFailed, err, extractOut)
	}

	// Measure total bytes copied.
	bytes, err := c.sizeOf(opts.StagingDir)
	if err != nil {
		return CopyResult{}, fmt.Errorf("%w: measuring staging size: %v", ErrCopyFailed, err)
	}
	res := CopyResult{BytesCopied: bytes}
	if bytes > largeCopyThreshold {
		res.Warning = fmt.Sprintf("warning: copy exceeds 500MB (%d bytes); continuing", bytes)
	}
	return res, nil
}

// writeExcludeFile writes the ignore patterns (one per line) to a temp file
// and returns its path. The caller is responsible for removing it.
func writeExcludeFile(patterns []string) (string, error) {
	f, err := os.CreateTemp("", "bunker-ignore-*.txt")
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = f.WriteString(strings.Join(patterns, "\n"))
	if err != nil {
		return "", err
	}
	return f.Name(), nil
}

// sizeOf returns the total byte size of all regular files under root. It
// delegates to the injected SizeOf when set (for tests), otherwise walks the
// tree with filepath.WalkDir.
func (c DefaultCopier) sizeOf(root string) (int64, error) {
	if c.SizeOf != nil {
		return c.SizeOf(root)
	}
	var total int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
