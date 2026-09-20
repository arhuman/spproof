package rules

import "fmt"

func init() {
	Register("comment_line_consecutive_max", commentLineConsecutiveMaxFactory{})
}

type commentLineConsecutiveMaxFactory struct{}

func (commentLineConsecutiveMaxFactory) New(s Spec) Rule {
	return &commentLineConsecutiveMax{spec: s}
}

// AppliesTo accepts only types with a line-comment concept: the check has
// nothing to count in a file whose comment syntax v1 does not know.
func (commentLineConsecutiveMaxFactory) AppliesTo(t FileType) bool {
	_, ok := CommentPrefix(t)
	return ok
}

func (commentLineConsecutiveMaxFactory) Validate(s Spec) error {
	if s.Max <= 0 {
		return fmt.Errorf("%w: %q got max %d", ErrMaxRequired, s.Check, s.Max)
	}
	return nil
}

// commentLineConsecutiveMax counts the current run of comment lines. Its state
// is two integers regardless of file size: the rule never buffers lines, so peak
// memory stays bounded by the longest line as the project requires.
type commentLineConsecutiveMax struct {
	spec   Spec
	path   string
	prefix string
	start  int
	length int
}

func (r *commentLineConsecutiveMax) Init(f FileMeta) {
	r.path = f.Path
	r.prefix, _ = CommentPrefix(f.Type)
}

// OnLine extends the current run on a comment line and closes it on anything
// else, reporting the closed run when it was too long.
//
// Any non-comment line breaks the run, a blank line included: a blank line is
// not a comment line under the definition in isCommentLine, and treating it as
// transparent would make the rule depend on a second, unstated notion of what
// continues a comment block. One rule, one definition.
func (r *commentLineConsecutiveMax) OnLine(n int, text string) []Violation {
	if r.prefix != "" && isCommentLine(text, r.prefix) {
		if r.length == 0 {
			r.start = n
		}
		r.length++
		return nil
	}
	return r.close()
}

// Finish flushes a run still open when the file ends. Without it, a file ending
// in an over-long comment block would report nothing at all.
func (r *commentLineConsecutiveMax) Finish() []Violation { return r.close() }

// close ends the current run, returning at most one violation for the whole run
// rather than one per line: an over-long block is a single problem, and a
// per-line verdict would report it (length - max) times.
//
// The violation points at the first line of the run, which is where a reader
// must start reading to see the block, and which stays stable as lines are
// added to the end of the block.
func (r *commentLineConsecutiveMax) close() []Violation {
	length, start := r.length, r.start
	r.length, r.start = 0, 0
	if length <= r.spec.Max {
		return nil
	}
	return []Violation{{
		RuleID:  r.spec.ID,
		Path:    r.path,
		Line:    start,
		Column:  1,
		Message: r.spec.Msg(fmt.Sprintf("run of %d consecutive comment lines, over the maximum of %d", length, r.spec.Max)),
	}}
}
