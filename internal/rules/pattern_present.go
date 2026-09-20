package rules

import "fmt"

func init() {
	Register("pattern_present", patternPresentFactory{})
}

type patternPresentFactory struct{}

func (patternPresentFactory) New(s Spec) Rule { return &patternPresent{spec: s} }

// AppliesTo accepts every type: a regex over raw lines needs no knowledge of
// the file's syntax.
func (patternPresentFactory) AppliesTo(FileType) bool { return true }

func (patternPresentFactory) Validate(s Spec) error {
	if s.Pattern == nil {
		return fmt.Errorf("%w: %q", ErrPatternRequired, s.Check)
	}
	return rejectSkipCode(s)
}

type patternPresent struct {
	spec  Spec
	path  string
	found bool
}

func (r *patternPresent) Init(f FileMeta) { r.path = f.Path }

// OnLine records whether the pattern has been seen and never reports: absence
// is only decidable once the file is exhausted, so the verdict comes from
// Finish. A file matching fifty times still yields zero violations.
func (r *patternPresent) OnLine(_ int, text string) []Violation {
	if !r.found && r.spec.Pattern.MatchString(text) {
		r.found = true
	}
	return nil
}

// Finish reports exactly one file-scoped violation when the pattern matched no
// line, and nothing otherwise. The pattern is missing from the whole file, so
// there is no line to point at.
func (r *patternPresent) Finish() []Violation {
	if r.found {
		return nil
	}
	return []Violation{{
		RuleID:     r.spec.ID,
		Path:       r.path,
		FileScoped: true,
		Message:    r.spec.Msg(fmt.Sprintf("required pattern %q matched no line", r.spec.Pattern.String())),
	}}
}
