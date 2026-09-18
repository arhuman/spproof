package rules

import "strings"

// commentPrefixes maps a file type to its line-comment marker. A type absent
// from the table has no line-comment concept as far as v1 is concerned.
//
// Markdown is absent deliberately: it has no comment syntax, only an HTML
// comment block, which is not a line prefix and which v1 cannot recognize
// without a lexer. TypeUnknown is absent because the extension told us nothing,
// and guessing a prefix would invent comments in a file whose syntax we do not
// know. YAML is present: "#" is factually its line comment, it costs one table
// entry, and excluding it would make the comment checks silently unavailable on
// the one file type a policy file is itself written in.
var commentPrefixes = map[FileType]string{
	TypeGo:   "//",
	TypePerl: "#",
	TypeYAML: "#",
}

// CommentPrefix returns the line-comment marker for a file type, and false when
// the type has no line-comment concept (Markdown and unknown extensions). A
// check that needs comment syntax uses the false result as its AppliesTo answer.
func CommentPrefix(t FileType) (string, bool) {
	p, ok := commentPrefixes[t]
	return p, ok
}

// isCommentLine reports whether text is a comment line for the given prefix.
//
// A comment line is a line whose first non-whitespace content is the prefix, so
// "    // foo" is one and "code() // foo" is not. A trailing comment on a code
// line is therefore invisible to every check built on this definition: those
// checks constrain comment lines, not comment text.
//
// This is a prefix test, not a lexer. It has no notion of string literals, so a
// line whose leading content is a prefix inside a string (for instance a Go
// line starting with a raw string containing "//") is read as a comment. That
// limitation is accepted and pinned by test for v1; string-literal awareness is
// deferred rather than partially approximated, since a partial fix would make
// the failure unpredictable instead of merely documented.
func isCommentLine(text, prefix string) bool {
	return strings.HasPrefix(strings.TrimLeft(text, " \t"), prefix)
}
