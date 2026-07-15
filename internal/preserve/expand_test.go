package preserve

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
)

// TestParseLine_Empty verifies that a blank/whitespace-only line returns a
// zero Entry (Raw == "") and no error — callers skip blank entries.
func TestParseLine_Empty(t *testing.T) {
	e, err := ParseLine("")
	if err != nil {
		t.Errorf("ParseLine(\"\") err = %v, want nil", err)
	}
	if e.Raw != "" {
		t.Errorf("ParseLine(\"\") Raw = %q, want empty", e.Raw)
	}
	if e.IsGlob {
		t.Errorf("ParseLine(\"\") IsGlob = true, want false")
	}
}

// TestParseLine_WhitespaceTrimmed triangulates: a literal path with surrounding
// whitespace is trimmed and classified as a non-glob literal.
func TestParseLine_WhitespaceTrimmed(t *testing.T) {
	e, err := ParseLine("  /etc/passwd  ")
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if e.Raw != "/etc/passwd" {
		t.Errorf("ParseLine Raw = %q, want %q", e.Raw, "/etc/passwd")
	}
	if e.IsGlob {
		t.Errorf("ParseLine IsGlob = true, want false for literal path")
	}
}

// TestParseLine_GlobDetected triangulates: a line with glob metacharacters is
// classified as a glob entry.
func TestParseLine_GlobDetected(t *testing.T) {
	e, err := ParseLine("~/projects/**")
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if e.Raw != "~/projects/**" {
		t.Errorf("ParseLine Raw = %q, want %q", e.Raw, "~/projects/**")
	}
	if !e.IsGlob {
		t.Errorf("ParseLine IsGlob = false, want true for glob pattern")
	}
}

// TestParseLine_CommentSkipped triangulates: lines starting with '#' are
// treated as comments and return a zero Entry (Raw == "") for callers to skip.
func TestParseLine_CommentSkipped(t *testing.T) {
	e, err := ParseLine("# this is a comment")
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if e.Raw != "" {
		t.Errorf("ParseLine(comment) Raw = %q, want empty (skip)", e.Raw)
	}
}

// TestParseLine_GlobQuestionMark triangulates the '?' metacharacter triggers
// glob classification (in addition to '*').
func TestParseLine_GlobQuestionMark(t *testing.T) {
	e, err := ParseLine("/var/log/syslog.?")
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if !e.IsGlob {
		t.Errorf("ParseLine('?') IsGlob = false, want true")
	}
}

// TestParseLine_GlobBrackets triangulates the '[' metacharacter triggers glob
// classification.
func TestParseLine_GlobBrackets(t *testing.T) {
	e, err := ParseLine("/etc/conf.d/[abc]*")
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if !e.IsGlob {
		t.Errorf("ParseLine('[') IsGlob = false, want true")
	}
}

// TestParseLine_GlobBraces triangulates the '{' metacharacter triggers glob
// classification.
func TestParseLine_GlobBraces(t *testing.T) {
	e, err := ParseLine("/data/{src,out}/**")
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if !e.IsGlob {
		t.Errorf("ParseLine('{') IsGlob = false, want true")
	}
}

// TestParseLine_SemicolonNotGlob triangulates: a ';' in a preserve path does
// NOT trigger glob classification (the threat matrix is for container names;
// preserve paths go through doublestar which has its own escape rules).
func TestParseLine_SemicolonNotGlob(t *testing.T) {
	e, err := ParseLine("/path/with;semicolon")
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if e.IsGlob {
		t.Errorf("ParseLine(';') IsGlob = true, want false")
	}
	if e.Raw != "/path/with;semicolon" {
		t.Errorf("ParseLine(';') Raw = %q, want %q", e.Raw, "/path/with;semicolon")
	}
}

// makeFS builds a fstest.MapFS with a small tree for glob expansion tests.
//
//	/projects/proj1/main.go
//	/projects/proj1/README.md
//	/projects/proj2/main.go
//	/projects/proj1/node_modules/lib.js   (default-excluded)
//	/projects/proj1/.git/config          (default-excluded)
func makeFS() fstest.MapFS {
	return fstest.MapFS{
		"projects/proj1/main.go":          {Data: []byte("p1-main")},
		"projects/proj1/README.md":        {Data: []byte("p1-readme")},
		"projects/proj2/main.go":          {Data: []byte("p2-main")},
		"projects/proj1/node_modules/lib.js": {Data: []byte("excluded")},
		"projects/proj1/.git/config":        {Data: []byte("excluded")},
	}
}

// TestExpand_GlobMultipleMatchesWithExclusions verifies glob expansion on a
// MapFS: the glob matches files under projects/**, default-excluded dirs are
// skipped silently, and the non-excluded matches are returned.
func TestExpand_GlobMultipleMatchesWithExclusions(t *testing.T) {
	expander := Expander{FS: makeFS()}
	entry := Entry{Raw: "projects/**", IsGlob: true}
	got, err := expander.Expand(entry)
	if err != nil {
		t.Fatalf("Expand error: %v", err)
	}
	// Expect 3 matches: p1/main.go, p1/README.md, p2/main.go. The two excluded
	// paths (node_modules/lib.js, .git/config) MUST NOT appear.
	if len(got) != 3 {
		t.Fatalf("Expand len = %d %v, want 3", len(got), got)
	}
	wantSet := map[string]bool{
		"projects/proj1/main.go":   true,
		"projects/proj1/README.md": true,
		"projects/proj2/main.go":   true,
	}
	for _, p := range got {
		if !wantSet[p] {
			t.Errorf("Expand unexpected match %q", p)
		}
	}
}

// TestExpand_GlobNoMatchReturnsErrGlobNoMatch triangulates: a glob that matches
// nothing returns an empty slice and ErrGlobNoMatch (callers treat as warning).
func TestExpand_GlobNoMatchReturnsErrGlobNoMatch(t *testing.T) {
	expander := Expander{FS: makeFS()}
	entry := Entry{Raw: "nowhere/**", IsGlob: true}
	got, err := expander.Expand(entry)
	if !errors.Is(err, ErrGlobNoMatch) {
		t.Errorf("Expand(no-match) err = %v, want ErrGlobNoMatch", err)
	}
	if len(got) != 0 {
		t.Errorf("Expand(no-match) len = %d, want 0", len(got))
	}
}

// TestExpand_LiteralExists triangulates: a literal path that exists in the FS
// returns a single-element slice with that path and no error.
func TestExpand_LiteralExists(t *testing.T) {
	expander := Expander{FS: makeFS()}
	entry := Entry{Raw: "projects/proj1/main.go", IsGlob: false}
	got, err := expander.Expand(entry)
	if err != nil {
		t.Fatalf("Expand(literal-exists) error: %v", err)
	}
	if len(got) != 1 || got[0] != "projects/proj1/main.go" {
		t.Errorf("Expand(literal-exists) = %v, want [projects/proj1/main.go]", got)
	}
}

// TestExpand_LiteralMissingReturnsEmpty triangulates: a literal path that does
// NOT exist returns an empty slice and nil error (NOT an error — missing
// literals are skipped silently, like a no-match warning).
func TestExpand_LiteralMissingReturnsEmpty(t *testing.T) {
	expander := Expander{FS: makeFS()}
	entry := Entry{Raw: "projects/proj1/missing.go", IsGlob: false}
	got, err := expander.Expand(entry)
	if err != nil {
		t.Errorf("Expand(literal-missing) err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("Expand(literal-missing) len = %d, want 0", len(got))
	}
}

// TestDefaultExclusions_ContainsExpected verifies the exported exclusion list
// contains the documented default exclusions (case-insensitive matching is done
// downstream; here we assert presence).
func TestDefaultExclusions_ContainsExpected(t *testing.T) {
	ex := DefaultExclusions()
	want := []string{"node_modules", ".git", "target", "dist", "__pycache__", "vendor", ".cache"}
	for _, w := range want {
		found := false
		for _, got := range ex {
			if got == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("DefaultExclusions missing %q in %v", w, ex)
		}
	}
}

// TestExpand_ExclusionCaseInsensitive triangulates that exclusions match
// case-insensitively (e.g. "Node_Modules" is excluded just like "node_modules").
func TestExpand_ExclusionCaseInsensitive(t *testing.T) {
	fsys := fstest.MapFS{
		"src/Node_Modules/lib.js": {Data: []byte("excluded")},
		"src/app.go":              {Data: []byte("kept")},
	}
	expander := Expander{FS: fsys}
	entry := Entry{Raw: "src/**", IsGlob: true}
	got, err := expander.Expand(entry)
	if err != nil {
		t.Fatalf("Expand error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Expand(case-insensitive) len = %d %v, want 1 (only src/app.go)", len(got), got)
	}
	if got[0] != "src/app.go" {
		t.Errorf("Expand(case-insensitive) = %v, want [src/app.go]", got)
	}
}

// Compile-time guarantee Expander is usable as an fs.FS consumer.
var _ = fs.FS(nil)