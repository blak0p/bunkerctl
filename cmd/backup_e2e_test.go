package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/blak0p/bunkerctl/internal/packages"
	"github.com/blak0p/bunkerctl/internal/podman"
)

// errFakeNonZero simulates a non-zero exit from a `which` probe (manager absent).
var errFakeNonZero = errors.New("fake non-zero exit")

// TestDedupeManagers is a unit test for the pure dedupe helper: when both dnf
// and dnf5 are detected, dnf is dropped; other managers are passed through in
// order. (Kept from v0.1.0; the helper is still used by the v1 pipeline when
// normalizing the manager slice passed to the cleaner.)
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
		{"dnf5 then dnf (canonical order)", []packages.Manager{packages.ManagerDnf5, packages.ManagerDnf}, []packages.Manager{packages.ManagerDnf5}},
		{"dnf + apt", []packages.Manager{packages.ManagerDnf, packages.ManagerApt}, []packages.Manager{packages.ManagerDnf, packages.ManagerApt}},
		{"dnf + dnf5 + apt", []packages.Manager{packages.ManagerDnf, packages.ManagerDnf5, packages.ManagerApt}, []packages.Manager{packages.ManagerDnf5, packages.ManagerApt}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dedupeManagers(c.in)
			if !equalManagers(got, c.want) {
				t.Errorf("dedupeManagers(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// equalManagers compares two manager slices for equality.
func equalManagers(a, b []packages.Manager) bool {
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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dedupeStrings(c.in)
			if !equalStrings(got, c.want) {
				t.Errorf("dedupeStrings(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// equalStrings compares two string slices for equality.
func equalStrings(a, b []string) bool {
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

// fakeExecCtx is a shorthand used by E2E tests in backup_e2e_test.go.
var _ = context.Background

// _ ensures podman.FakeRunner is referenced (used in 3f E2E tests).
var _ *podman.FakeRunner