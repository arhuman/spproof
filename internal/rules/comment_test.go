package rules

import "testing"

func TestCommentPrefixTable(t *testing.T) {
	tests := []struct {
		ty     FileType
		want   string
		wantOK bool
	}{
		{TypeGo, "//", true},
		{TypePerl, "#", true},
		{TypeYAML, "#", true},
		{TypeMarkdown, "", false},
		{TypeUnknown, "", false},
	}
	for _, tc := range tests {
		got, ok := CommentPrefix(tc.ty)
		if ok != tc.wantOK {
			t.Errorf("CommentPrefix(%q) ok = %v, want %v", tc.ty, ok, tc.wantOK)
		}
		if got != tc.want {
			t.Errorf("CommentPrefix(%q) = %q, want %q", tc.ty, got, tc.want)
		}
	}
}

// TestCommentLineDefinition pins what counts as a comment line: leading
// whitespace is allowed, and a trailing comment on a code line is not one.
func TestCommentLineDefinition(t *testing.T) {
	tests := []struct {
		text   string
		prefix string
		want   bool
	}{
		{"// foo", "//", true},
		{"    // foo", "//", true},
		{"\t\t// foo", "//", true},
		{"code() // foo", "//", false},
		{"", "//", false},
		{"   ", "//", false},
		{"package main", "//", false},
		{"# perl comment", "#", true},
		{"  # yaml comment", "#", true},
		{"key: value # trailing", "#", false},
	}
	for _, tc := range tests {
		if got := isCommentLine(tc.text, tc.prefix); got != tc.want {
			t.Errorf("isCommentLine(%q, %q) = %v, want %v", tc.text, tc.prefix, got, tc.want)
		}
	}
}

// TestCommentPrefixHasNoStringLiteralAwareness pins a known and accepted v1
// limitation rather than leaving it accidental: the table is a prefix test, not
// a lexer, so a "//" reached first on a line is a comment even when it is inside
// a Go string. Fixing this needs real lexing and is deferred; a partial
// heuristic would trade a documented limitation for an unpredictable one.
func TestCommentPrefixHasNoStringLiteralAwareness(t *testing.T) {
	// A line inside a Go raw string literal. Lexically it is string content, but
	// its first non-whitespace token is "//", so v1 reads it as a comment line.
	const insideRawString = "  // still inside a raw string block"
	if !isCommentLine(insideRawString, "//") {
		t.Error("v1 is expected to read this as a comment line; the limitation moved, update the doc comment on isCommentLine")
	}

	// The converse half of the limitation: a // that is not leading is invisible,
	// so a code line with a URL in a string is correctly not a comment line.
	if isCommentLine(`url := "http://x"`, "//") {
		t.Error("a non-leading // must not read as a comment line")
	}

	r := newCharMax(t, TypeGo, 10)
	if v := r.OnLine(1, insideRawString); len(v) != 1 {
		t.Fatalf("got %d violations, want 1: the raw-string line is measured as a comment line", len(v))
	}
}
