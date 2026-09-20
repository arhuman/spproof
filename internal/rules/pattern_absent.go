package rules

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// ErrPatternRequired is returned by Validate when a check needing a regex was
// declared without one.
var ErrPatternRequired = errors.New("rules: check requires a pattern")

func init() {
	Register("pattern_absent", patternAbsentFactory{})
}

type patternAbsentFactory struct{}

func (patternAbsentFactory) New(s Spec) Rule { return &patternAbsent{spec: s} }

// AppliesTo accepts every type: a regex over raw lines needs no knowledge of
// the file's syntax.
func (patternAbsentFactory) AppliesTo(FileType) bool { return true }

func (patternAbsentFactory) Validate(s Spec) error {
	if s.Pattern == nil {
		return fmt.Errorf("%w: %q", ErrPatternRequired, s.Check)
	}
	return nil
}

type patternAbsent struct {
	spec Spec
	path string
}

func (r *patternAbsent) Init(f FileMeta) { r.path = f.Path }

// OnLine reports one violation per match, so a line containing the forbidden
// pattern three times fails three times.
func (r *patternAbsent) OnLine(n int, text string) []Violation {
	locs := r.spec.Pattern.FindAllStringIndex(text, -1)
	if locs == nil {
		return nil
	}
	out := make([]Violation, 0, len(locs))
	for _, loc := range locs {
		match := text[loc[0]:loc[1]]
		out = append(out, Violation{
			RuleID:  r.spec.ID,
			Path:    r.path,
			Line:    n,
			Column:  utf8.RuneCountInString(text[:loc[0]]) + 1,
			Message: r.spec.Msg(fmt.Sprintf("forbidden pattern %q matched %q", r.spec.Pattern.String(), match)),
			Match:   match,
		})
	}
	return out
}

func (r *patternAbsent) Finish() []Violation { return nil }
