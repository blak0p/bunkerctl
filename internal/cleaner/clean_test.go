package cleaner

import (
	"context"
	"errors"
	"testing"

	"github.com/blak0p/bunkerctl/internal/packages"
	"github.com/blak0p/bunkerctl/internal/podman"
)

// recordingRunner records every Exec invocation for assertion.
type recordingRunner struct {
	calls    [][]string // each element is the cmd slice passed to Exec
	execErr  error
}

func (r *recordingRunner) Version(ctx context.Context) (string, error) {
	return "podman version 5.0.0", nil
}
func (r *recordingRunner) List(ctx context.Context, all bool) ([]podman.Container, error) {
	return nil, nil
}
func (r *recordingRunner) Inspect(ctx context.Context, id string) (podman.InspectResult, error) {
	return podman.InspectResult{}, nil
}
func (r *recordingRunner) Commit(ctx context.Context, id, image string) error { return nil }
func (r *recordingRunner) Save(ctx context.Context, image, format, dest string) error {
	return nil
}
func (r *recordingRunner) Exec(ctx context.Context, id string, cmd []string) (string, error) {
	// Copy to avoid aliasing the backing array across calls.
	c := make([]string, len(cmd))
	copy(c, cmd)
	r.calls = append(r.calls, c)
	return "", r.execErr
}

// TestCleaner_Clean_Apt is the RED anchor: DefaultCleaner.Clean with [ManagerApt]
// MUST call runner.Exec exactly once with ["rm","-rf","/var/cache/apt/archives"].
func TestCleaner_Clean_Apt(t *testing.T) {
	r := &recordingRunner{}
	c := DefaultCleaner{}
	if err := c.Clean(context.Background(), r, "mybunker", []packages.Manager{packages.ManagerApt}); err != nil {
		t.Fatalf("Clean error: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("Exec calls = %d, want 1", len(r.calls))
	}
	want := []string{"rm", "-rf", "/var/cache/apt/archives"}
	if got := r.calls[0]; !sliceEq(got, want) {
		t.Errorf("Exec cmd = %v, want %v", got, want)
	}
}

// TestCleaner_Clean_AptPlusPacman triangulates: with two managers the cleaner
// MUST call Exec twice, once per manager, in the order given and with the right
// cache paths.
func TestCleaner_Clean_AptPlusPacman(t *testing.T) {
	r := &recordingRunner{}
	c := DefaultCleaner{}
	if err := c.Clean(context.Background(), r, "mybunker", []packages.Manager{packages.ManagerApt, packages.ManagerPacman}); err != nil {
		t.Fatalf("Clean error: %v", err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("Exec calls = %d, want 2", len(r.calls))
	}
	want0 := []string{"rm", "-rf", "/var/cache/apt/archives"}
	want1 := []string{"rm", "-rf", "/var/cache/pacman/pkg"}
	if !sliceEq(r.calls[0], want0) {
		t.Errorf("Exec[0] = %v, want %v", r.calls[0], want0)
	}
	if !sliceEq(r.calls[1], want1) {
		t.Errorf("Exec[1] = %v, want %v", r.calls[1], want1)
	}
}

// TestCleaner_Clean_UnknownSkipped triangulates: ManagerUnknown has no known
// cache path, so Clean MUST NOT call Exec at all.
func TestCleaner_Clean_UnknownSkipped(t *testing.T) {
	r := &recordingRunner{}
	c := DefaultCleaner{}
	if err := c.Clean(context.Background(), r, "mybunker", []packages.Manager{packages.ManagerUnknown}); err != nil {
		t.Fatalf("Clean error: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("Exec calls = %d, want 0 (ManagerUnknown skipped)", len(r.calls))
	}
}

// TestCleaner_Clean_ExecErrorWrapped triangulates the error boundary: when
// runner.Exec returns an error, Clean MUST return ErrCacheCleanFailed (wrapped,
// not the raw error).
func TestCleaner_Clean_ExecErrorWrapped(t *testing.T) {
	r := &recordingRunner{execErr: errors.New("boom")}
	c := DefaultCleaner{}
	err := c.Clean(context.Background(), r, "mybunker", []packages.Manager{packages.ManagerApt})
	if !errors.Is(err, ErrCacheCleanFailed) {
		t.Errorf("Clean err = %v, want ErrCacheCleanFailed", err)
	}
}

// sliceEq is a small helper comparing string slices element-wise.
func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}