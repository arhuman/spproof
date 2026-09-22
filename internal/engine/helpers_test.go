package engine

import (
	"io"
	"regexp"
	"strings"

	"github.com/arhuman/spproof/internal/rules"
)

type readCloser = io.ReadCloser

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }

// stubFactory is a pattern_absent clone whose applicability is restricted to a
// set of types, so the per (file, rule) drop is observable while P1 ships only
// a check that applies everywhere.
type stubFactory struct {
	applies map[rules.FileType]bool
}

func (f stubFactory) New(s rules.Spec) rules.Rule {
	return &stubRule{spec: s, pattern: stubPattern(s)}
}

func (f stubFactory) AppliesTo(t rules.FileType) bool { return f.applies[t] }

func (f stubFactory) Validate(rules.Spec) error { return nil }

// stubPattern reads the regex out of the rule's with block, mirroring how a
// real check decodes its own configuration. It returns nil when the spec
// carries none, which the stub treats as matching nothing.
func stubPattern(s rules.Spec) *regexp.Regexp {
	var cfg struct {
		Pattern string `yaml:"pattern"`
	}
	if err := s.DecodeWith(&cfg); err != nil || cfg.Pattern == "" {
		return nil
	}
	return regexp.MustCompile(cfg.Pattern)
}

type stubRule struct {
	spec    rules.Spec
	pattern *regexp.Regexp
	path    string
}

func (r *stubRule) Init(f rules.FileMeta) { r.path = f.Path }

func (r *stubRule) OnLine(n int, text string) []rules.Violation {
	if r.pattern == nil || !r.pattern.MatchString(text) {
		return nil
	}
	return []rules.Violation{{
		RuleID: r.spec.ID, Path: r.path, Line: n, Column: 1, Message: "stub match",
	}}
}

func (r *stubRule) Finish() []rules.Violation { return nil }
