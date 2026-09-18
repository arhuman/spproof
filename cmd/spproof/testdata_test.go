package main

import (
	"strings"
	"testing"

	"github.com/arhuman/spproof/internal/rules"
)

// Test-only checks restricted to Go files. The shipped pattern_absent applies
// to every type, so any test whose subject is the file TYPE rather than the
// file glob needs a narrower factory to keep the two apart.
const (
	// goOnlyCheck never reports, for asserting a rule is not evaluated at all.
	goOnlyCheck = "go_only_test_check"
	// goOnlyViolatingCheck reports on a pattern match, for asserting a rule
	// both applies and fires.
	goOnlyViolatingCheck = "go_only_violating_test_check"
)

func init() {
	rules.Register(goOnlyCheck, goOnlyFactory{})
	rules.Register(goOnlyViolatingCheck, goOnlyFactory{reports: true})
}

type goOnlyFactory struct{ reports bool }

func (f goOnlyFactory) New(s rules.Spec) rules.Rule {
	return &goOnlyRule{spec: s, reports: f.reports}
}

func (goOnlyFactory) AppliesTo(t rules.FileType) bool { return t == rules.TypeGo }

func (goOnlyFactory) Validate(rules.Spec) error { return nil }

type goOnlyRule struct {
	spec    rules.Spec
	path    string
	reports bool
}

func (r *goOnlyRule) Init(f rules.FileMeta) { r.path = f.Path }

func (r *goOnlyRule) OnLine(n int, text string) []rules.Violation {
	if !r.reports || r.spec.Pattern == nil || !r.spec.Pattern.MatchString(text) {
		return nil
	}
	return []rules.Violation{{
		RuleID: r.spec.ID, Path: r.path, Line: n, Column: 1, Message: "go-only test match",
	}}
}

func (r *goOnlyRule) Finish() []rules.Violation { return nil }

// TestCommittedPolicyCatchesFixtureViolations proves the em-dash policy is a
// working rule and not merely a well-formed file: the clean fixture passes and
// the violation fixture fails at the expected positions.
func TestCommittedPolicyCatchesFixtureViolations(t *testing.T) {
	const policy = "../../testdata/policy-no-em-dash.yml"

	t.Run("clean fixture passes", func(t *testing.T) {
		var out, errb strings.Builder
		code := run([]string{"check", "--policy", policy, "--stdin", "--as=clean.md"},
			strings.NewReader("Clean document.\n\nNo forbidden punctuation here.\n"), &out, &errb)
		if code != exitOK {
			t.Errorf("exit = %d, want 0. output: %s%s", code, out.String(), errb.String())
		}
	})

	t.Run("violation fixture fails", func(t *testing.T) {
		var out, errb strings.Builder
		// The dash characters are the data under test, written as escapes to
		// keep them out of this source file.
		content := "A line with an em dash \u2014 right here.\nAnd an en dash \u2013 too.\n"
		code := run([]string{"check", "--policy", policy, "--stdin", "--as=violations.md"},
			strings.NewReader(content), &out, &errb)
		if code != exitViolated {
			t.Fatalf("exit = %d, want 1", code)
		}
		for _, want := range []string{"violations.md:1:24:", "violations.md:2:16:", "[no-em-dash]"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("output missing %q:\n%s", want, out.String())
			}
		}
	})
}
