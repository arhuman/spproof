package rules

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// ErrMaxRequired is returned by Validate when a check needing a positive max was
// declared without one. A max of zero would flag every comment line, so it is a
// refusal rather than a default.
var ErrMaxRequired = errors.New("rules: check requires a positive max")

func init() {
	Register("comment_line_char_max", commentLineCharMaxFactory{})
}

type commentLineCharMaxFactory struct{}

func (commentLineCharMaxFactory) New(s Spec) Rule { return &commentLineCharMax{spec: s} }

// AppliesTo accepts only types with a line-comment concept: the check has
// nothing to measure in a file whose comment syntax v1 does not know.
func (commentLineCharMaxFactory) AppliesTo(t FileType) bool {
	_, ok := CommentPrefix(t)
	return ok
}

func (commentLineCharMaxFactory) Validate(s Spec) error {
	if s.Max <= 0 {
		return fmt.Errorf("%w: %q got max %d", ErrMaxRequired, s.Check, s.Max)
	}
	return rejectSkipCode(s)
}

type commentLineCharMax struct {
	spec   Spec
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
	if length <= r.spec.Max {
		return nil
	}
	return []Violation{{
		RuleID:  r.spec.ID,
		Path:    r.path,
		Line:    n,
		Column:  r.spec.Max + 1,
		Message: r.spec.Msg(fmt.Sprintf("comment line is %d characters, over the maximum of %d", length, r.spec.Max)),
	}}
}

func (r *commentLineCharMax) Finish() []Violation { return nil }
