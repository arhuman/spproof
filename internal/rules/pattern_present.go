package rules

import (
	"fmt"
	"regexp"
)

func init() {
	Register("pattern_present", patternPresentFactory{})
}

type patternPresentFactory struct{}

// New compiles the pattern once per file. Validate has already proved it
// compiles, so the error cannot occur here.
func (patternPresentFactory) New(s Spec) Rule {
	re, _ := patternPresentConfig(s)
	return &patternPresent{spec: s, pattern: re}
}

// AppliesTo accepts every type: a regex over raw lines needs no knowledge of
// the file's syntax.
func (patternPresentFactory) AppliesTo(FileType) bool { return true }

func (patternPresentFactory) Validate(s Spec) error {
	_, err := patternPresentConfig(s)
	return err
}

// patternPresentConfig decodes this check's own `with:` block, which is a
// pattern alone. It does not reuse patternConfig because skip_code is
// meaningless here: absence is decided over the whole file, and a pattern found
// only inside a fence has still been found. Omitting the field from the struct
// is what makes skip_code an unknown key rather than a silently ignored one.
func patternPresentConfig(s Spec) (*regexp.Regexp, error) {
	if re, ok := s.Config.(*regexp.Regexp); ok {
		return re, nil
	}
	var cfg struct {
		Pattern string `yaml:"pattern"`
	}
	if err := s.DecodeWith(&cfg); err != nil {
		return nil, err
	}
	if cfg.Pattern == "" {
		return nil, fmt.Errorf("%w: %q", ErrPatternRequired, s.Check)
	}
	re, err := regexp.Compile(cfg.Pattern)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %v", ErrBadPattern, s.Check, err)
	}
	return re, nil
}

type patternPresent struct {
	spec    Spec
	pattern *regexp.Regexp
	path    string
	found   bool
}

func (r *patternPresent) Init(f FileMeta) { r.path = f.Path }

// OnLine records whether the pattern has been seen and never reports: absence
// is only decidable once the file is exhausted, so the verdict comes from
// Finish. A file matching fifty times still yields zero violations.
func (r *patternPresent) OnLine(_ int, text string) []Violation {
	if !r.found && r.pattern.MatchString(text) {
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
		Message:    r.spec.Msg(fmt.Sprintf("required pattern %q matched no line", r.pattern.String())),
	}}
}

// Prepare caches the compiled regex so New does not re-compile per file.
func (patternPresentFactory) Prepare(s Spec) (any, error) { return patternPresentConfig(s) }
