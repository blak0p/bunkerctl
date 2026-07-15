package inspect

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/blak0p/bunkerctl/internal/podman"
)

// UserInfo holds the detected user information from inside the container. The
// uid/gid come from the container's /etc/passwd via getent, never from the host
// (REQ-DETECT-3). When only the fallback (echo $HOME) succeeds, Name/GID are
// empty and UID is preserved from the input.
type UserInfo struct {
	Name string
	UID  int
	GID  int
	Home string
}

// ErrMultiUser is returned by DetectMultiUser when more than one non-system
// user (UID >= 1000 with a home directory) is detected (REQ-ERR-3).
var ErrMultiUser = errors.New("multi-user container detected: not supported in this version")

// ErrUserDetect is returned when both getent passwd and the echo $HOME fallback
// fail to produce user information.
var ErrUserDetect = errors.New("could not detect container user: getent and echo $HOME both failed")

// DetectUser resolves user info via `getent passwd <uid>` inside the container,
// falling back to `sh -c 'echo $HOME'` when getent returns nothing or fails
// (REQ-DETECT-2). The uid is taken from InspectData (parsed from Config.User),
// NOT from the host (REQ-DETECT-3).
func DetectUser(ctx context.Context, runner podman.Runner, name string, uid int) (UserInfo, error) {
	// Primary: getent passwd <uid>.
	out, err := runner.Exec(ctx, name, []string{"getent", "passwd", strconv.Itoa(uid)})
	if err == nil {
		if info, perr := parsePasswdLine(out); perr == nil {
			if info.UID == uid {
				return info, nil
			}
		}
	}
	// Fallback: sh -c 'echo $HOME' — yields only the home directory.
	home, herr := runner.Exec(ctx, name, []string{"sh", "-c", "echo $HOME"})
	if herr == nil {
		home = strings.TrimSpace(home)
		if home != "" {
			return UserInfo{UID: uid, Home: home}, nil
		}
	}
	return UserInfo{}, ErrUserDetect
}

// parsePasswdLine parses a single /etc/passwd line:
//
//	name:x:uid:gid:gecos:home:shell
//
// It requires exactly 7 colon-separated fields and numeric uid/gid.
func parsePasswdLine(line string) (UserInfo, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return UserInfo{}, fmt.Errorf("empty passwd line")
	}
	fields := strings.Split(line, ":")
	if len(fields) != 7 {
		return UserInfo{}, fmt.Errorf("malformed passwd line: want 7 fields, got %d", len(fields))
	}
	uid, err := strconv.Atoi(fields[2])
	if err != nil {
		return UserInfo{}, fmt.Errorf("non-numeric uid %q: %w", fields[2], err)
	}
	gid, err := strconv.Atoi(fields[3])
	if err != nil {
		return UserInfo{}, fmt.Errorf("non-numeric gid %q: %w", fields[3], err)
	}
	return UserInfo{
		Name: fields[0],
		UID:  uid,
		GID:  gid,
		Home: fields[5],
	}, nil
}

// DetectMultiUser checks whether the container has more than one non-system
// user (UID >= 1000 with a non-empty home directory). It runs `getent passwd`
// (no uid arg) and counts matching lines. Returns nil if single-user or no
// non-system users; returns ErrMultiUser if more than one (REQ-ERR-3).
func DetectMultiUser(ctx context.Context, runner podman.Runner, name string) error {
	out, err := runner.Exec(ctx, name, []string{"getent", "passwd"})
	if err != nil {
		// If getent itself fails, treat as single-user (nothing to flag).
		return nil
	}
	count := 0
	for _, line := range strings.Split(out, "\n") {
		info, perr := parsePasswdLine(line)
		if perr != nil {
			continue
		}
		if info.UID >= 1000 && info.Home != "" {
			count++
		}
	}
	if count > 1 {
		return ErrMultiUser
	}
	return nil
}