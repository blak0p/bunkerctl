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
	Name  string
	UID   int
	GID   int
	Home  string
	Shell string
}

// ErrMultiUser is returned by DetectMultiUser when more than one non-system
// user (UID >= 1000 with a home directory) is detected (REQ-ERR-3).
var ErrMultiUser = errors.New("multi-user container detected: not supported in this version")

// ErrUserDetect is returned when both getent passwd and the echo $HOME fallback
// fail to produce user information.
var ErrUserDetect = errors.New("could not detect container user: getent and echo $HOME both failed")

// ErrMultiUserAmbiguous is returned by ResolveUser when the container has more
// than one real user and Config.User indicates root, so the backup target user
// cannot be decided without an interactive chooser. The embedded RealUsers slice
// carries the detected users for a future UI to present.
var ErrMultiUserAmbiguous = errors.New("ambiguous real-user container: multiple candidates found")

// MultiUserError extends ErrMultiUserAmbiguous with the list of real users
// detected in the container.
type MultiUserError struct {
	RealUsers []UserInfo
}

func (e *MultiUserError) Error() string {
	var names []string
	for _, u := range e.RealUsers {
		names = append(names, fmt.Sprintf("%s (%d:%d)", u.Name, u.UID, u.GID))
	}
	return fmt.Sprintf("%s: %s", ErrMultiUserAmbiguous, strings.Join(names, ", "))
}

func (e *MultiUserError) Unwrap() error { return ErrMultiUserAmbiguous }

// DetectUser resolves user info via `getent passwd <uid>` inside the container,
// falling back to `sh -c 'echo $HOME'` when getent returns nothing or fails
// (REQ-DETECT-2). The uid is taken from InspectData (parsed from Config.User),
// NOT from the host (REQ-DETECT-3).
//
// Deprecated: prefer ResolveUser, which considers the container's real users
// when Config.User is root.
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
		Name:  fields[0],
		UID:   uid,
		GID:   gid,
		Home:  fields[5],
		Shell: fields[6],
	}, nil
}

// nonRealShells are login shells that indicate a service/system account, not a
// human user. A user is a "real user" only if its shell is NOT in this set.
var nonRealShells = map[string]bool{
	"":                  true, // empty shell
	"/usr/sbin/nologin": true,
	"/sbin/nologin":     true,
	"/bin/false":        true,
	"/usr/bin/false":    true,
	"/bin/true":         true,
	"/usr/bin/true":     true,
}

// nonRealHomes are home directories that indicate a system account, not a real
// user. A user is a "real user" only if its home is NOT in this set.
var nonRealHomes = map[string]bool{
	"":             true, // empty home
	"/":            true,
	"/nonexistent": true,
}

// isRealUser reports whether a parsed passwd entry represents a genuine human
// user rather than a system/service account. A real user has ALL of: UID >= 1000,
// a real login shell (not nologin/false/true/empty), and a real home directory
// (not /, /nonexistent, or empty). Counting on UID alone is wrong because real
// Fedora containers carry 20+ system users (nobody at UID 65534, unbound,
// systemd-coredump, etc.) whose shells/homes disqualify them.
func isRealUser(info UserInfo) bool {
	return info.UID >= 1000 && !nonRealShells[info.Shell] && !nonRealHomes[info.Home]
}

// DetectMultiUser checks whether the container has more than one real user. A
// "real user" requires UID >= 1000 AND a real login shell AND a real home
// directory; system/service accounts (nologin/false/true shells, / or
// /nonexistent homes) are excluded. It runs `getent passwd` (no uid arg) and
// counts matching lines. Returns nil if single-user or no real users; returns
// ErrMultiUser if more than one (REQ-ERR-3).
func DetectMultiUser(ctx context.Context, runner podman.Runner, name string) error {
	users, err := detectRealUsers(ctx, runner, name)
	if err != nil {
		if errors.Is(err, ErrMultiUserAmbiguous) {
			return ErrMultiUser
		}
		return err
	}
	_ = users
	return nil
}

// detectRealUsers returns every real user in the container. A "real user"
// requires UID >= 1000 AND a real login shell AND a real home directory.
// If getent fails, it returns an empty slice and no error. If more than one
// real user is found, it returns ErrMultiUserAmbiguous with all real users.
func detectRealUsers(ctx context.Context, runner podman.Runner, name string) ([]UserInfo, error) {
	out, err := runner.Exec(ctx, name, []string{"getent", "passwd"})
	if err != nil {
		// If getent itself fails, treat as single-user (nothing to flag).
		return nil, nil
	}
	var users []UserInfo
	for _, line := range strings.Split(out, "\n") {
		info, perr := parsePasswdLine(line)
		if perr != nil {
			continue
		}
		if isRealUser(info) {
			users = append(users, info)
		}
	}
	if len(users) > 1 {
		return users, &MultiUserError{RealUsers: users}
	}
	return users, nil
}

// ResolveUser chooses the correct backup target user for the container.
//
// If configUser does not indicate root (numeric 0, the string "root", or the
// form "root:root"), it resolves configUser to a uid and returns the matching
// passwd entry when possible.
//
// If configUser indicates root, it prefers the container's real users over root:
//   - Exactly one real user: use it.
//   - Zero real users: fall back to root (the legacy behavior).
//   - More than one real user: return an error wrapping ErrMultiUserAmbiguous
//     that carries the list of real users for a future chooser.
func ResolveUser(ctx context.Context, runner podman.Runner, name string, configUser string) (UserInfo, error) {
	if !isRootLike(configUser) {
		return resolveNonRootUser(ctx, runner, name, configUser)
	}

	realUsers, err := detectRealUsers(ctx, runner, name)
	if err != nil {
		return UserInfo{}, err
	}
	switch len(realUsers) {
	case 0:
		return DetectUser(ctx, runner, name, 0)
	case 1:
		return realUsers[0], nil
	default:
		return UserInfo{}, err
	}
}

// isRootLike reports whether the configured user string should be treated as
// root for the purposes of preferring a real container user.
func isRootLike(user string) bool {
	user = strings.TrimSpace(user)
	if user == "" {
		return true
	}
	// Split "root:root" form.
	name, _, _ := strings.Cut(user, ":")
	name = strings.TrimSpace(name)
	if name == "root" {
		return true
	}
	if uid, err := strconv.Atoi(name); err == nil && uid == 0 {
		return true
	}
	return false
}

// resolveNonRootUser resolves an explicit numeric uid or username to a passwd
// entry inside the container. Numeric values are looked up by uid; names are
// resolved via `getent passwd <name>`.
func resolveNonRootUser(ctx context.Context, runner podman.Runner, name string, configUser string) (UserInfo, error) {
	configUser = strings.TrimSpace(configUser)
	// Numeric uid: use getent passwd <uid>.
	if uid, err := strconv.Atoi(configUser); err == nil {
		return DetectUser(ctx, runner, name, uid)
	}
	// Username: resolve via getent passwd <name>.
	out, err := runner.Exec(ctx, name, []string{"getent", "passwd", configUser})
	if err == nil {
		if info, perr := parsePasswdLine(out); perr == nil {
			if info.Name == configUser {
				return info, nil
			}
		}
	}
	// Fallback: try uid 0 for unresolvable non-root values.
	return DetectUser(ctx, runner, name, 0)
}
