package rules

import (
	"errors"
	"fmt"
	"testing"
)

func newAbsent(t *testing.T, pattern string) Rule {
	t.Helper()
	f, ok := Lookup("pattern_absent")
	if !ok {
		t.Fatal("pattern_absent is not registered")
	}
	spec := Spec{ID: "test-rule", Check: "pattern_absent", With: WithYAML(fmt.Sprintf("pattern: %q", pattern))}
	if err := f.Validate(spec); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	r := f.New(spec)
	r.Init(FileMeta{Path: "a.md", Type: TypeMarkdown})
	return r
}

func TestPatternAbsentNoMatch(t *testing.T) {
	r := newAbsent(t, "TODO")
	if v := r.OnLine(1, "a clean line"); len(v) != 0 {
		t.Errorf("got %d violations, want 0", len(v))
	}
	if v := r.Finish(); len(v) != 0 {
		t.Errorf("Finish returned %d violations, want 0", len(v))
	}
}

func TestPatternAbsentSingleMatch(t *testing.T) {
	r := newAbsent(t, "TODO")
	v := r.OnLine(7, "a TODO here")
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1", len(v))
	}
	got := v[0]
	if got.RuleID != "test-rule" {
		t.Errorf("RuleID = %q, want test-rule", got.RuleID)
	}
	if got.Path != "a.md" {
		t.Errorf("Path = %q, want a.md", got.Path)
	}
	if got.Line != 7 {
		t.Errorf("Line = %d, want 7", got.Line)
	}
	if got.Column != 3 {
		t.Errorf("Column = %d, want 3", got.Column)
	}
	if got.Match != "TODO" {
		t.Errorf("Match = %q, want TODO", got.Match)
	}
}

// TestPatternAbsentMultipleMatchesOnOneLine pins the once-per-match semantics:
// three occurrences on a single line are three separate violations.
func TestPatternAbsentMultipleMatchesOnOneLine(t *testing.T) {
	r := newAbsent(t, "ab")
	v := r.OnLine(1, "ab-ab-ab")
	if len(v) != 3 {
		t.Fatalf("got %d violations, want 3", len(v))
	}
	wantCols := []int{1, 4, 7}
	for i, w := range wantCols {
		if v[i].Column != w {
			t.Errorf("violation %d column = %d, want %d", i, v[i].Column, w)
		}
		if v[i].Line != 1 {
			t.Errorf("violation %d line = %d, want 1", i, v[i].Line)
		}
	}
}

// TestPatternAbsentColumnCountsRunes proves the column is usable by an editor
// on a UTF-8 line, where a byte offset would point at the wrong character.
func TestPatternAbsentColumnCountsRunes(t *testing.T) {
	r := newAbsent(t, "X")
	v := r.OnLine(1, "éééX")
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1", len(v))
	}
	if v[0].Column != 4 {
		t.Errorf("Column = %d, want 4 (runes, not bytes)", v[0].Column)
	}
}

func TestPatternAbsentAppliesToEveryType(t *testing.T) {
	f, _ := Lookup("pattern_absent")
	for _, ty := range []FileType{TypeUnknown, TypeGo, TypeMarkdown, TypePerl, TypeYAML} {
		if !f.AppliesTo(ty) {
			t.Errorf("AppliesTo(%q) = false, want true", ty)
		}
	}
}

func TestPatternAbsentValidateRequiresPattern(t *testing.T) {
	f, _ := Lookup("pattern_absent")
	err := f.Validate(Spec{ID: "a", Check: "pattern_absent"})
	if !errors.Is(err, ErrPatternRequired) {
		t.Errorf("got %v, want ErrPatternRequired", err)
	}
}

func TestTypeOf(t *testing.T) {
	tests := []struct {
		path string
		want FileType
	}{
		{"main.go", TypeGo},
		{"README.md", TypeMarkdown},
		{"doc/a.markdown", TypeMarkdown},
		{"script.pl", TypePerl},
		{"conf.yml", TypeYAML},
		{"conf.YAML", TypeYAML},
		{"Makefile", TypeUnknown},
		{"archive.tar.gz", TypeUnknown},
	}
	for _, tc := range tests {
		if got := TypeOf(tc.path); got != tc.want {
			t.Errorf("TypeOf(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestNamesIncludesRegisteredCheck(t *testing.T) {
	found := false
	for _, n := range Names() {
		if n == "pattern_absent" {
			found = true
		}
	}
	if !found {
		t.Errorf("Names() = %v, missing pattern_absent", Names())
	}
}
