package rules

import (
	"errors"
	"fmt"
	"testing"
)

func newPresent(t *testing.T, pattern string) Rule {
	t.Helper()
	f, ok := Lookup("pattern_present")
	if !ok {
		t.Fatal("pattern_present is not registered")
	}
	spec := Spec{ID: "test-rule", Check: "pattern_present", With: WithYAML(fmt.Sprintf("pattern: %q", pattern))}
	if err := f.Validate(spec); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	r := f.New(spec)
	r.Init(FileMeta{Path: "a.go", Type: TypeGo})
	return r
}

func TestPatternPresentSingleMatchHolds(t *testing.T) {
	r := newPresent(t, "SPDX-License-Identifier")
	if v := r.OnLine(1, "// SPDX-License-Identifier: MIT"); len(v) != 0 {
		t.Errorf("OnLine returned %d violations, want 0", len(v))
	}
	if v := r.Finish(); len(v) != 0 {
		t.Errorf("Finish returned %d violations, want 0", len(v))
	}
}

// TestPatternPresentManyMatchesStillHolds pins the once-per-file semantics
// against the once-per-match semantics of pattern_absent: repeated matches are
// not repeated anything, they are the same single satisfied requirement.
func TestPatternPresentManyMatchesStillHolds(t *testing.T) {
	r := newPresent(t, "ab")
	for n := 1; n <= 50; n++ {
		if v := r.OnLine(n, "ab-ab-ab"); len(v) != 0 {
			t.Fatalf("OnLine(%d) returned %d violations, want 0", n, len(v))
		}
	}
	if v := r.Finish(); len(v) != 0 {
		t.Errorf("Finish returned %d violations, want 0", len(v))
	}
}

// TestPatternPresentNoMatchFailsOnceFromFinish proves the Finish half of the
// Rule contract: nothing is reported while the file is being read, and the
// single verdict arrives only once the file is exhausted.
func TestPatternPresentNoMatchFailsOnceFromFinish(t *testing.T) {
	r := newPresent(t, "SPDX-License-Identifier")
	for n, line := range []string{"package main", "", "func main() {}"} {
		if v := r.OnLine(n+1, line); v != nil {
			t.Fatalf("OnLine(%d) returned %v, want nil", n+1, v)
		}
	}
	v := r.Finish()
	if len(v) != 1 {
		t.Fatalf("Finish returned %d violations, want 1", len(v))
	}
	got := v[0]
	if got.RuleID != "test-rule" {
		t.Errorf("RuleID = %q, want test-rule", got.RuleID)
	}
	if got.Path != "a.go" {
		t.Errorf("Path = %q, want a.go", got.Path)
	}
	if got.Match != "" {
		t.Errorf("Match = %q, want empty: nothing matched", got.Match)
	}
}

// TestPatternPresentViolationIsFileScoped pins that the failure carries no
// position. The pattern is absent from the whole file, so a line or column
// would be an invented location.
func TestPatternPresentViolationIsFileScoped(t *testing.T) {
	r := newPresent(t, "missing")
	v := r.Finish()
	if len(v) != 1 {
		t.Fatalf("Finish returned %d violations, want 1", len(v))
	}
	got := v[0]
	if !got.FileScoped {
		t.Error("FileScoped = false, want true")
	}
	if got.Line != 0 {
		t.Errorf("Line = %d, want 0: a file-scoped violation has no line", got.Line)
	}
	if got.Column != 0 {
		t.Errorf("Column = %d, want 0: a file-scoped violation has no column", got.Column)
	}
}

// TestPatternPresentEmptyFileFails covers the file with no lines at all: OnLine
// is never called, so Finish is the only chance to report, and the pattern is
// absent from it like from any other file lacking it.
func TestPatternPresentEmptyFileFails(t *testing.T) {
	r := newPresent(t, "SPDX-License-Identifier")
	v := r.Finish()
	if len(v) != 1 {
		t.Fatalf("Finish returned %d violations, want 1", len(v))
	}
	if !v[0].FileScoped {
		t.Error("FileScoped = false, want true")
	}
}

func TestPatternPresentAppliesToEveryType(t *testing.T) {
	f, _ := Lookup("pattern_present")
	for _, ty := range []FileType{TypeUnknown, TypeGo, TypeMarkdown, TypePerl, TypeYAML} {
		if !f.AppliesTo(ty) {
			t.Errorf("AppliesTo(%q) = false, want true", ty)
		}
	}
}

func TestPatternPresentValidateRequiresPattern(t *testing.T) {
	f, _ := Lookup("pattern_present")
	err := f.Validate(Spec{ID: "a", Check: "pattern_present"})
	if !errors.Is(err, ErrPatternRequired) {
		t.Errorf("got %v, want ErrPatternRequired", err)
	}
}

func TestPatternPresentIsRegistered(t *testing.T) {
	found := false
	for _, n := range Names() {
		if n == "pattern_present" {
			found = true
		}
	}
	if !found {
		t.Errorf("Names() = %v, missing pattern_present", Names())
	}
}
