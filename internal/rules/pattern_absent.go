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
	re, sc, _ := requirePattern(s)
	return &patternAbsent{spec: s, pattern: re, scope: sc}
}

// AppliesTo accepts every type: a regex over raw lines needs no knowledge of
// the file's syntax. A rule naming a fence language narrows this; see
// AppliesToSpec.
func (patternAbsentFactory) AppliesTo(FileType) bool { return true }

// AppliesToSpec drops the rule for every non-markdown file once the policy names
// a fence language. Fences are markdown's concept, so such a rule can decide
// nothing about a Go file: running it there would scan every line of a file that
// has no blocks to select, which is a broader rule than the policy declared.
func (f patternAbsentFactory) AppliesToSpec(s Spec, t FileType) bool {
	_, sc, err := requirePattern(s)
	if err != nil || sc.fenceLang == "" {
		return f.AppliesTo(t)
	}
	return t == TypeMarkdown
}

func (patternAbsentFactory) Validate(s Spec) error {
	_, _, err := requirePattern(s)
	return err
}

type patternAbsent struct {
	spec    Spec
	pattern *regexp.Regexp
	scope   scope
	path    string
	class   LineClass
}

func (r *patternAbsent) Init(f FileMeta) { r.path = f.Path }

// OnClass records the current line's prose/code split for the OnLine that
// follows it. It is a no-op unless the policy narrowed the rule's scope.
func (r *patternAbsent) OnClass(c LineClass) { r.class = c }

// OnLine reports one violation per match, so a line containing the forbidden
// pattern three times fails three times.
//
// Where the pattern may look is the policy's choice, and both narrowings run it
// against a blanked copy rather than a shortened one: skip_code drops all code,
// fence_lang drops everything but one block's code. Offsets survive either, so
// the column and the reported match are still taken from the line the reader
// sees rather than from the copy.
func (r *patternAbsent) OnLine(n int, text string) []Violation {
	subject := text
	switch {
	case r.scope.skipCode:
		subject = r.class.Masked(text)
	case r.scope.fenceLang != "":
		subject = r.class.KeptTo(r.scope.fenceLang, text)
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

// Prepare caches the compiled regex and scope so New does not re-decode per file.
func (patternAbsentFactory) Prepare(s Spec) (any, error) {
	re, sc, err := requirePattern(s)
	if err != nil {
		return nil, err
	}
	return compiledPattern{re: re, scope: sc}, nil
}
