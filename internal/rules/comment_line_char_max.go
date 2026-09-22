package rules

import (
	"fmt"
	"unicode/utf8"
)

func init() {
	Register("comment_line_char_max", commentLineCharMaxFactory{})
}

type commentLineCharMaxFactory struct{}

// New reads the bound once per file. Validate has already proved it is present
// and positive, so the error cannot occur here.
func (commentLineCharMaxFactory) New(s Spec) Rule {
	limit, _ := requireMax(s)
	return &commentLineCharMax{spec: s, max: limit}
}

// AppliesTo accepts only types with a line-comment concept: the check has
// nothing to measure in a file whose comment syntax v1 does not know.
func (commentLineCharMaxFactory) AppliesTo(t FileType) bool {
	_, ok := CommentPrefix(t)
	return ok
}

func (commentLineCharMaxFactory) Validate(s Spec) error {
	_, err := requireMax(s)
	return err
}

type commentLineCharMax struct {
	spec   Spec
	max    int
	path   string
	prefix string
}

func (r *commentLineCharMax) Init(f FileMeta) {
	r.path = f.Path
	r.prefix, _ = CommentPrefix(f.Type)
}

// OnLine reports one violation per over-long comment line. Non-comment lines are
// ignored entirely, including a code line carrying a trailing comment.
//
// The length is counted in runes, not bytes and not display width: a byte count
// would fail accented or CJK text that is well within the limit, and display
// width is not decidable without font and terminal knowledge v1 does not have.
//
// The column is the first rune past the limit, which is the first rune a reader
// has to delete to satisfy the rule.
func (r *commentLineCharMax) OnLine(n int, text string) []Violation {
	if r.prefix == "" || !isCommentLine(text, r.prefix) {
		return nil
	}
	length := utf8.RuneCountInString(text)
	if length <= r.max {
		return nil
	}
	return []Violation{{
		RuleID:  r.spec.ID,
		Path:    r.path,
		Line:    n,
		Column:  r.max + 1,
		Message: r.spec.Msg(fmt.Sprintf("comment line is %d characters, over the maximum of %d", length, r.max)),
	}}
}

func (r *commentLineCharMax) Finish() []Violation { return nil }

// Prepare caches the validated bound so New does not re-decode per file.
func (commentLineCharMaxFactory) Prepare(s Spec) (any, error) { return requireMax(s) }
