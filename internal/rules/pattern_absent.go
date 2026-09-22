package rules

import (
	"fmt"
	"regexp"
	"unicode/utf8"
)

func init() {
	Register("pattern_absent", patternAbsentFactory{})
}

type patternAbsentFactory struct{}

// New compiles the pattern once per file. Validate has already proved the
// config decodes and the regex compiles, so a failure here cannot happen and
// the error is dropped rather than carried through the Rule contract.
func (patternAbsentFactory) New(s Spec) Rule {
	re, skipCode, _ := requirePattern(s)
	return &patternAbsent{spec: s, pattern: re, skipCode: skipCode}
}

// AppliesTo accepts every type: a regex over raw lines needs no knowledge of
// the file's syntax.
func (patternAbsentFactory) AppliesTo(FileType) bool { return true }

func (patternAbsentFactory) Validate(s Spec) error {
	_, _, err := requirePattern(s)
	return err
}

type patternAbsent struct {
	spec     Spec
	pattern  *regexp.Regexp
	skipCode bool
	path     string
	class    LineClass
}

func (r *patternAbsent) Init(f FileMeta) { r.path = f.Path }

// OnClass records the current line's prose/code split for the OnLine that
// follows it. It is a no-op unless the policy set skip_code.
func (r *patternAbsent) OnClass(c LineClass) { r.class = c }

// OnLine reports one violation per match, so a line containing the forbidden
// pattern three times fails three times.
//
// Under skip_code the pattern runs against a masked copy in which code is
// blanked. Offsets survive masking, so the column and the reported match are
// still taken from the line the reader sees rather than from the copy.
func (r *patternAbsent) OnLine(n int, text string) []Violation {
	subject := text
	if r.skipCode {
		subject = r.class.Masked(text)
	}
	locs := r.pattern.FindAllStringIndex(subject, -1)
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
			Message: r.spec.Msg(fmt.Sprintf("forbidden pattern %q matched %q", r.pattern.String(), match)),
			Match:   match,
		})
	}
	return out
}

func (r *patternAbsent) Finish() []Violation { return nil }

// Prepare caches the compiled regex so New does not re-compile per file.
func (patternAbsentFactory) Prepare(s Spec) (any, error) {
	re, skipCode, err := requirePattern(s)
	if err != nil {
		return nil, err
	}
	return compiledPattern{re: re, skipCode: skipCode}, nil
}
