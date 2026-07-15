package manifest

import (
	"reflect"
	"testing"
)

// sampleManifest returns a fully-populated BunkerManifest used across round-trip
// and validation tests. It exercises every required field so a missing-field
// check can be derived by zeroing one field at a time.
func sampleManifest() *BunkerManifest {
	return &BunkerManifest{
		FormatVersion: 1,
		Name:          "bunker",
		Created:       "2026-07-15",
		User: UserInfo{
			Name: "alejndro",
			UID:  1000,
			GID:  1000,
			Home: "/home/alejndro",
		},
		Base: BaseInfo{
			Distro:  "fedora",
			Version: "45",
		},
		Packages: map[string][]Package{
			"dnf": {
				{Name: "neovim", Version: "0.10.2"},
				{Name: "fish", Version: "3.7.0"},
			},
		},
		Files: FilesConfig{
			Copy:    "auto",
			CopyEtc: []string{},
			Ignore:  DefaultIgnoreList(),
		},
		Custom: CustomConfig{
			Environment: map[string]string{
				"EDITOR": "nvim",
				"TERM":   "kitty",
			},
		},
		Verify: VerifyConfig{
			Auto: true,
		},
	}
}

// TestMarshalUnmarshal_RoundTrip verifies that a fully-populated BunkerManifest
// survives a marshal → unmarshal cycle with all fields intact. This is the RED
// anchor: neither Marshal nor Unmarshal nor the struct types exist yet.
func TestMarshalUnmarshal_RoundTrip(t *testing.T) {
	original := sampleManifest()
	data, err := Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Errorf("round-trip mismatch:\n got  = %+v\n want = %+v", got, original)
	}
}

// TestUnmarshal_FormatVersionMustBe1 verifies REQ-YAML-1: format_version MUST be
// the integer 1. A version of 2 (the only other realistic value here) MUST be
// rejected with ErrInvalidFormatVersion.
func TestUnmarshal_FormatVersionMustBe1(t *testing.T) {
	data := []byte("format_version: 2\nname: x\ncreated: 2026-07-15\n")
	_, err := Unmarshal(data)
	if err == nil {
		t.Fatalf("Unmarshal(format_version=2) err = nil, want error")
	}
}

// TestMarshal_FormatVersionIsIntNotString verifies REQ-YAML-1 scenario: the
// serialized YAML MUST contain `format_version: 1` as an integer, not the quoted
// string `"1"`.
func TestMarshal_FormatVersionIsIntNotString(t *testing.T) {
	data, err := Marshal(sampleManifest())
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	s := string(data)
	want := "format_version: 1\n"
	if !contains(s, want) {
		t.Errorf("marshaled YAML missing %q; got:\n%s", want, s)
	}
	bad := "format_version: \"1\""
	if contains(s, bad) {
		t.Errorf("marshaled YAML serialized format_version as a string; got:\n%s", s)
	}
}

// TestValidate_RequiredFields is a table-driven test for REQ-YAML-2 through
// REQ-YAML-7: zeroing each required field MUST cause Validate to return a
// non-nil error. The baseline (fully populated) MUST validate cleanly.
func TestValidate_RequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*BunkerManifest)
		wantErr bool
	}{
		{"baseline valid", func(m *BunkerManifest) {}, false},
		{"format_version 0", func(m *BunkerManifest) { m.FormatVersion = 0 }, true},
		{"format_version 2", func(m *BunkerManifest) { m.FormatVersion = 2 }, true},
		{"missing user.name", func(m *BunkerManifest) { m.User.Name = "" }, true},
		{"missing user.home", func(m *BunkerManifest) { m.User.Home = "" }, true},
		{"missing base.distro", func(m *BunkerManifest) { m.Base.Distro = "" }, true},
		{"missing base.version", func(m *BunkerManifest) { m.Base.Version = "" }, true},
		{"nil packages", func(m *BunkerManifest) { m.Packages = nil }, true},
		{"package missing name", func(m *BunkerManifest) {
			m.Packages = map[string][]Package{"dnf": {{Name: "", Version: "1.0"}}}
		}, true},
		{"package missing version", func(m *BunkerManifest) {
			m.Packages = map[string][]Package{"dnf": {{Name: "fish", Version: ""}}}
		}, true},
		{"files.copy not auto", func(m *BunkerManifest) { m.Files.Copy = "manual" }, true},
		{"files.ignore empty", func(m *BunkerManifest) { m.Files.Ignore = []string{} }, true},
		{"custom.environment nil", func(m *BunkerManifest) { m.Custom.Environment = nil }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := sampleManifest()
			c.mutate(m)
			err := m.Validate()
			if c.wantErr && err == nil {
				t.Errorf("Validate() err = nil, want non-nil for %s", c.name)
			}
			if !c.wantErr && err != nil {
				t.Errorf("Validate() err = %v, want nil for %s", err, c.name)
			}
		})
	}
}

// TestDefaultIgnoreList_Contents verifies REQ-COPY-4: the default ignore list
// MUST contain exactly the 16 specified patterns (the 15 originals plus "/tmp"),
// in order, with no duplicates.
func TestDefaultIgnoreList_Contents(t *testing.T) {
	got := DefaultIgnoreList()
	want := []string{
		".cache",
		"node_modules",
		"target",
		".cargo/registry",
		".npm",
		".next",
		".git",
		"Downloads",
		".local/share/Trash",
		"__pycache__",
		".venv",
		"venv",
		"*.log",
		"*.tmp",
		"*.sock",
		"/tmp",
	}
	if len(got) != 16 {
		t.Fatalf("DefaultIgnoreList len = %d, want 16", len(got))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DefaultIgnoreList = %v\nwant           = %v", got, want)
	}
	// No duplicates.
	seen := map[string]bool{}
	for _, p := range got {
		if seen[p] {
			t.Errorf("duplicate pattern %q in DefaultIgnoreList", p)
		}
		seen[p] = true
	}
}

// TestUnmarshal_RejectsMissingTopLevelSections verifies that YAML missing an
// entire required section (user, base, packages, files, custom, verify) is
// rejected by Unmarshal's internal Validate call.
func TestUnmarshal_RejectsMissingTopLevelSections(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"missing user", "format_version: 1\nname: x\ncreated: 2026-07-15\n"},
		{"missing base", "format_version: 1\nname: x\ncreated: 2026-07-15\nuser:\n  name: a\n  uid: 1\n  gid: 1\n  home: /h\n"},
		{"missing custom.environment", "format_version: 1\nname: x\ncreated: 2026-07-15\nuser:\n  name: a\n  uid: 1\n  gid: 1\n  home: /h\nbase:\n  distro: fedora\n  version: \"45\"\npackages:\n  dnf: []\nfiles:\n  copy: auto\n  copy_etc: []\n  ignore: [\".cache\"]\nverify:\n  auto: true\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Unmarshal([]byte(c.yaml)); err == nil {
				t.Errorf("Unmarshal(%s) err = nil, want non-nil", c.name)
			}
		})
	}
}

// contains is a minimal substring helper to avoid importing strings just for one
// call site.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
