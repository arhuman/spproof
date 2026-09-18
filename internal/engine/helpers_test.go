package engine

import (
	"io"
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

func (f stubFactory) New(s rules.Spec) rules.Rule { return &stubRule{spec: s} }

func (f stubFactory) AppliesTo(t rules.FileType) bool { return f.applies[t] }

func (f stubFactory) Validate(rules.Spec) error { return nil }

type stubRule struct {
	spec rules.Spec
	path string
}

func (r *stubRule) Init(f rules.FileMeta) { r.path = f.Path }

func (r *stubRule) OnLine(n int, text string) []rules.Violation {
	if r.spec.Pattern == nil || !r.spec.Pattern.MatchString(text) {
		return nil
	}
	return []rules.Violation{{
		RuleID: r.spec.ID, Path: r.path, Line: n, Column: 1, Message: "stub match",
	}}
}

func (r *stubRule) Finish() []rules.Violation { return nil }
