package ignore

import (
	"reflect"
	"testing"
)

// TestDefaultPatterns_Contents verifies REQ-COPY-4: DefaultPatterns returns
// exactly the 15 specified patterns (the 14 originals plus "/tmp"), in order,
// with no duplicates.
func TestDefaultPatterns_Contents(t *testing.T) {
	got := DefaultPatterns()
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
		"/tmp",
	}
	if len(got) != 15 {
		t.Fatalf("DefaultPatterns len = %d, want 15", len(got))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DefaultPatterns = %v\nwant            = %v", got, want)
	}
	seen := map[string]bool{}
	for _, p := range got {
		if seen[p] {
			t.Errorf("duplicate pattern %q", p)
		}
		seen[p] = true
	}
}

// TestMatcher_ShouldIgnore is a table-driven test covering directory skips, glob
// matches, literal matches, and non-matches. All paths are relative to the copy
// root (the user home), matching how the copy step feeds paths to the matcher.
func TestMatcher_ShouldIgnore(t *testing.T) {
	m, err := NewMatcher(DefaultPatterns())
	if err != nil {
		t.Fatalf("NewMatcher error: %v", err)
	}
	cases := []struct {
		path     string
		ignored  bool
		category string
	}{
		// Directory skip (whole tree under an ignored dir).
		{".cache/opera/cache/C_01", true, "dir skip"},
		{".cache", true, "dir root"},
		// Glob *.log and *.tmp.
		{"app.log", true, "glob *.log"},
		{"logs/build.tmp", true, "glob *.tmp in subdir"},
		{"notes.md", false, "non-matching glob"},
		// Literal directory names anywhere in the path.
		{"projects/app/node_modules/react", true, "literal node_modules"},
		{"target/release/bunkerctl", true, "literal target"},
		// Nested patterns with slash.
		{".cargo/registry/cache/abc", true, "nested .cargo/registry"},
		{".cargo/index", false, ".cargo but not registry"},
		{".local/share/Trash/file", true, "Trash dir"},
		{".local/share/app", false, ".local/share but not Trash"},
		// Regular files must NOT be ignored just for being dotfiles.
		{".bashrc", false, "dotfile not ignored"},
		{".config/fish/config.fish", false, "config file not ignored"},
		// Non-matching bare names.
		{"Downloads", true, "bare Downloads"},
		{"projects/Downloads", true, "Downloads in subdir"},
		{"myDownloads", false, "prefix not a match"},
	}
	for _, c := range cases {
		t.Run(c.category+"/"+c.path, func(t *testing.T) {
			got := m.ShouldIgnore(c.path)
			if got != c.ignored {
				t.Errorf("ShouldIgnore(%q) = %v, want %v (%s)", c.path, got, c.ignored, c.category)
			}
		})
	}
}

// TestMatcher_CaseSensitivity verifies doublestar is case-sensitive on Linux:
// `Downloads` MUST NOT match `downloads`.
func TestMatcher_CaseSensitivity(t *testing.T) {
	m, err := NewMatcher(DefaultPatterns())
	if err != nil {
		t.Fatalf("NewMatcher error: %v", err)
	}
	if m.ShouldIgnore("downloads") {
		t.Errorf("ShouldIgnore(\"downloads\") = true, want false (case-sensitive)")
	}
	if !m.ShouldIgnore("Downloads") {
		t.Errorf("ShouldIgnore(\"Downloads\") = false, want true")
	}
}

// TestMergePatterns_Dedup verifies that merging defaults with extras removes
// duplicates and preserves order (defaults first, then new extras).
func TestMergePatterns_Dedup(t *testing.T) {
	defaults := []string{".cache", "node_modules", "*.log"}
	extras := []string{"build", "*.log", "dist", "node_modules"}
	got := MergePatterns(defaults, extras)
	want := []string{".cache", "node_modules", "*.log", "build", "dist"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MergePatterns = %v\nwant         = %v", got, want)
	}
}

// TestMergePatterns_EmptyExtras verifies that merging with no extras returns the
// defaults unchanged (same content, dedup-safe).
func TestMergePatterns_EmptyExtras(t *testing.T) {
	defaults := []string{".cache", "node_modules"}
	got := MergePatterns(defaults, nil)
	if !reflect.DeepEqual(got, defaults) {
		t.Errorf("MergePatterns(defaults, nil) = %v, want %v", got, defaults)
	}
}

// TestNewMatcher_InvalidPattern verifies that a malformed doublestar pattern is
// rejected at construction time rather than silently never matching.
func TestNewMatcher_InvalidPattern(t *testing.T) {
	// doublestar rejects unclosed brackets as invalid globs.
	_, err := NewMatcher([]string{"[unclosed"})
	if err == nil {
		t.Errorf("NewMatcher([unclosed]) err = nil, want error")
	}
}