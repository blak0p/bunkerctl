// Package inspect fetches container metadata via `podman inspect` and detects
// the container's primary user and base distro. Every podman interaction goes
// through podman.Runner so tests drive the package with a fake engine.
//
// Fetch parses the full podman inspect JSON payload (obtained via
// Runner.InspectRaw) into InspectData. DetectUser runs `getent passwd <uid>`
// inside the container with a fallback to `sh -c 'echo $HOME'`. DetectBase
// reads /etc/os-release inside the container and rejects non-Fedora distros.
package inspect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/blak0p/bunkerctl/internal/podman"
)

// InspectData holds the parsed fields of `podman inspect <name>` that the
// backup pipeline needs: the container ID, image ref, configured user (uid or
// name string), environment, and running state.
type InspectData struct {
	ID    string
	Image string
	User  string // Config.User (e.g. "1000" or "alejndro"); empty defaults to "0"
	Env   []string
	State ContainerState
}

// ContainerState is the subset of podman inspect State used by the pipeline.
type ContainerState struct {
	Running bool
}

// rawInspect is the minimal JSON shape of `podman inspect` output. Podman
// returns a JSON array of objects; we only decode the fields we use.
type rawInspect struct {
	ID    string `json:"Id"`
	Image string `json:"Image"`
	Config struct {
		User string   `json:"User"`
		Env  []string `json:"Env"`
	} `json:"Config"`
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
}

// ErrEmptyInspect is returned when `podman inspect` returns an empty JSON
// array (no matching container).
var ErrEmptyInspect = errors.New("podman inspect returned no results")

// Fetch runs `podman inspect <name>` via Runner.InspectRaw and parses the JSON
// into InspectData. An empty Config.User is normalized to "0" (root), matching
// podman's default for unnamed users (REQ-DETECT-1).
func Fetch(ctx context.Context, runner podman.Runner, name string) (InspectData, error) {
	raw, err := runner.InspectRaw(ctx, name)
	if err != nil {
		return InspectData{}, fmt.Errorf("running podman inspect %q: %w", name, err)
	}
	var arr []rawInspect
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return InspectData{}, fmt.Errorf("parsing podman inspect output: %w", err)
	}
	if len(arr) == 0 {
		return InspectData{}, ErrEmptyInspect
	}
	r := arr[0]
	user := strings.TrimSpace(r.Config.User)
	if user == "" {
		user = "0"
	}
	return InspectData{
		ID:    r.ID,
		Image: r.Image,
		User:  user,
		Env:   r.Config.Env,
		State: ContainerState{Running: r.State.Running},
	}, nil
}

// UIDFromUser resolves the Config.User string to a numeric uid. If user is
// already numeric, it is parsed directly. If it is a name, the caller is
// expected to resolve it via getent; this helper only handles the numeric
// case and returns 0 (root) for an unparseable value.
func UIDFromUser(user string) int {
	user = strings.TrimSpace(user)
	n, err := strconv.Atoi(user)
	if err != nil {
		return 0
	}
	return n
}