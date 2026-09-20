package rules

import (
	"errors"
	"strings"
	"testing"
)

func newFileLineMax(t *testing.T, limit int) Rule {
	t.Helper()
	f, ok := Lookup("file_line_max")
	if !ok {
		t.Fatal("file_line_max is not registered")
	}
	spec := Spec{ID: "test-rule", Check: "file_line_max", Max: limit}
	if err := f.Validate(spec); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	r := f.New(spec)
	r.Init(FileMeta{Path: "SKILL.md", Type: TypeMarkdown})
	return r
}

// feedLines drives n lines through the rule, returning whatever OnLine reported so a
// caller can assert the count never verdicts mid-file.
func feedLines(r Rule, n int) []Violation {
	var out []Violation
	for i := 1; i <= n; i++ {
		out = append(out, r.OnLine(i, "x")...)
	}
	return out
}

func TestFileLineMaxUnderLimitHolds(t *testing.T) {
	r := newFileLineMax(t, 10)
	if v := feedLines(r, 9); len(v) != 0 {
		t.Errorf("OnLine returned %d violations, want 0", len(v))
	}
	if v := r.Finish(); len(v) != 0 {
		t.Errorf("Finish returned %d violations, want 0", len(v))
	}
}

// TestFileLineMaxAtLimitHolds pins max as inclusive, so the message "over the
// maximum of N" is accurate. check-skill-size.sh fails at >= 500, which a
// policy reproduces as max: 499; that mapping only holds if max is inclusive.
func TestFileLineMaxAtLimitHolds(t *testing.T) {
	r := newFileLineMax(t, 10)
	feedLines(r, 10)
	if v := r.Finish(); len(v) != 0 {
		t.Errorf("got %d violations at exactly the limit, want 0", len(v))
	}
}

func TestFileLineMaxOverLimitFailsOnce(t *testing.T) {
	r := newFileLineMax(t, 10)
	if v := feedLines(r, 11); len(v) != 0 {
		t.Fatalf("OnLine reported %d violations mid-file, want 0: the verdict belongs to Finish", len(v))
	}
	v := r.Finish()
	if len(v) != 1 {
		t.Fatalf("got %d violations, want exactly 1 for the whole file", len(v))
	}
	got := v[0]
	if got.RuleID != "test-rule" {
		t.Errorf("RuleID = %q, want test-rule", got.RuleID)
	}
	if got.Path != "SKILL.md" {
		t.Errorf("Path = %q, want SKILL.md", got.Path)
	}
	if !got.FileScoped {
		t.Error("FileScoped = false, want true: length is a property of the whole file")
	}
	if got.Line != 0 {
		t.Errorf("Line = %d, want 0: a file-scoped violation points at no line", got.Line)
	}
	if !strings.Contains(got.Message, "11 lines") || !strings.Contains(got.Message, "maximum of 10") {
		t.Errorf("Message = %q, want it to name both the actual count and the limit", got.Message)
	}
}

// TestFileLineMaxEmptyFileHolds guards the degenerate case: a file with no
// lines is zero lines long, which is under every positive maximum. A rule that
// verdicted here would fail every empty file in a tree.
func TestFileLineMaxEmptyFileHolds(t *testing.T) {
	r := newFileLineMax(t, 1)
	if v := r.Finish(); len(v) != 0 {
		t.Errorf("empty file produced %d violations, want 0", len(v))
	}
}

// TestFileLineMaxRequiresPositiveMax pins the refusal: max 0 would fail every
// file including empty ones, so it is rejected at load rather than defaulted.
func TestFileLineMaxRequiresPositiveMax(t *testing.T) {
	f, ok := Lookup("file_line_max")
	if !ok {
		t.Fatal("file_line_max is not registered")
	}
	for _, max := range []int{0, -1} {
		err := f.Validate(Spec{ID: "r", Check: "file_line_max", Max: max})
		if !errors.Is(err, ErrMaxRequired) {
			t.Errorf("Validate(max=%d) = %v, want ErrMaxRequired", max, err)
		}
	}
}

// TestFileLineMaxAppliesToEveryType pins that a line count needs no comment
// syntax, unlike the comment_line_* checks which drop on unknown types.
func TestFileLineMaxAppliesToEveryType(t *testing.T) {
	f, ok := Lookup("file_line_max")
	if !ok {
		t.Fatal("file_line_max is not registered")
	}
	for _, ty := range []FileType{TypeGo, TypeMarkdown, TypeYAML, TypePerl, TypeUnknown} {
		if !f.AppliesTo(ty) {
			t.Errorf("AppliesTo(%v) = false, want true", ty)
		}
	}
}

func TestNamesIncludesFileLineMax(t *testing.T) {
	found := false
	for _, n := range Names() {
		if n == "file_line_max" {
			found = true
		}
	}
	if !found {
		t.Errorf("Names() = %v, want it to include file_line_max", Names())
	}
}
