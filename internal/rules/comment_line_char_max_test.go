package rules

import (
	"errors"
	"fmt"
	"testing"
	"unicode/utf8"
)

func newCharMax(t *testing.T, ty FileType, limit int) Rule {
	t.Helper()
	f, ok := Lookup("comment_line_char_max")
	if !ok {
		t.Fatal("comment_line_char_max is not registered")
	}
	spec := Spec{ID: "test-rule", Check: "comment_line_char_max", With: WithYAML(fmt.Sprintf("max: %d", limit))}
	if err := f.Validate(spec); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	r := f.New(spec)
	r.Init(FileMeta{Path: "a.go", Type: ty})
	return r
}

func TestCommentLineCharMaxUnderLimitHolds(t *testing.T) {
	r := newCharMax(t, TypeGo, 20)
	if v := r.OnLine(1, "// short enough"); len(v) != 0 {
		t.Errorf("got %d violations, want 0", len(v))
	}
	if v := r.Finish(); len(v) != 0 {
		t.Errorf("Finish returned %d violations, want 0", len(v))
	}
}

// TestCommentLineCharMaxAtLimitHolds pins max as inclusive: a line of exactly
// max runes is allowed, so the message "over the maximum of N" is accurate.
func TestCommentLineCharMaxAtLimitHolds(t *testing.T) {
	r := newCharMax(t, TypeGo, 10)
	line := "// 1234567"
	if got := utf8.RuneCountInString(line); got != 10 {
		t.Fatalf("fixture is %d runes, want 10", got)
	}
	if v := r.OnLine(1, line); len(v) != 0 {
		t.Errorf("got %d violations, want 0: max is inclusive", len(v))
	}
}

func TestCommentLineCharMaxOverLimitFailsPerLine(t *testing.T) {
	r := newCharMax(t, TypeGo, 10)
	v := r.OnLine(4, "// far too long for the limit")
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1", len(v))
	}
	got := v[0]
	if got.RuleID != "test-rule" {
		t.Errorf("RuleID = %q, want test-rule", got.RuleID)
	}
	if got.Path != "a.go" {
		t.Errorf("Path = %q, want a.go", got.Path)
	}
	if got.Line != 4 {
		t.Errorf("Line = %d, want 4", got.Line)
	}
	if got.Column != 11 {
		t.Errorf("Column = %d, want 11 (first rune past the limit)", got.Column)
	}
	if got.FileScoped {
		t.Error("FileScoped = true, want false: the violation points at a real line")
	}

	second := r.OnLine(5, "// another line that is also much too long")
	if len(second) != 1 {
		t.Errorf("got %d violations on the second line, want 1", len(second))
	}
}

// TestCommentLineCharMaxCountsRunesNotBytes uses a line well under the limit in
// runes and well over it in bytes: a byte count would fail it spuriously.
func TestCommentLineCharMaxCountsRunesNotBytes(t *testing.T) {
	line := "// éàüçñ 日本語テキスト"
	runes, bytes := utf8.RuneCountInString(line), len(line)
	if runes >= 30 || bytes <= 30 {
		t.Fatalf("fixture is %d runes / %d bytes, want under / over 30", runes, bytes)
	}
	r := newCharMax(t, TypeGo, 30)
	if v := r.OnLine(1, line); len(v) != 0 {
		t.Errorf("got %d violations, want 0: %d runes is within the limit of 30", len(v), runes)
	}
}

func TestCommentLineCharMaxIgnoresNonCommentLines(t *testing.T) {
	r := newCharMax(t, TypeGo, 10)
	lines := []string{
		"func aVeryLongFunctionNameIndeed() error { return nil }",
		"code() // a trailing comment that is far past the limit",
		"",
		"    ",
	}
	for n, line := range lines {
		if v := r.OnLine(n+1, line); len(v) != 0 {
			t.Errorf("line %d %q produced %d violations, want 0", n+1, line, len(v))
		}
	}
}

// TestCommentLineCharMaxIndentedCommentIsMeasuredWhole pins that the limit
// applies to the full line including its indentation, which is what a reader
// sees and what a column in an editor counts.
func TestCommentLineCharMaxIndentedCommentIsMeasuredWhole(t *testing.T) {
	r := newCharMax(t, TypeGo, 10)
	if v := r.OnLine(1, "        // short"); len(v) != 1 {
		t.Errorf("got %d violations, want 1: indentation counts toward the limit", len(v))
	}
}

func TestCommentLineCharMaxAppliesOnlyWhereCommentsExist(t *testing.T) {
	f, _ := Lookup("comment_line_char_max")
	want := map[FileType]bool{
		TypeGo:       true,
		TypePerl:     true,
		TypeYAML:     true,
		TypeMarkdown: false,
		TypeUnknown:  false,
	}
	for ty, w := range want {
		if got := f.AppliesTo(ty); got != w {
			t.Errorf("AppliesTo(%q) = %v, want %v", ty, got, w)
		}
	}
}

func TestCommentLineCharMaxValidateRequiresPositiveMax(t *testing.T) {
	f, _ := Lookup("comment_line_char_max")
	for _, limit := range []int{0, -1} {
		err := f.Validate(Spec{ID: "a", Check: "comment_line_char_max", With: WithYAML(fmt.Sprintf("max: %d", limit))})
		if !errors.Is(err, ErrMaxRequired) {
			t.Errorf("Validate(max=%d) = %v, want ErrMaxRequired", limit, err)
		}
	}
	if err := f.Validate(Spec{ID: "a", Check: "comment_line_char_max", With: WithYAML("max: 1")}); err != nil {
		t.Errorf("Validate(max=1) = %v, want nil", err)
	}
}

func TestCommentLineCharMaxIsRegistered(t *testing.T) {
	found := false
	for _, n := range Names() {
		if n == "comment_line_char_max" {
			found = true
		}
	}
	if !found {
		t.Errorf("Names() = %v, missing comment_line_char_max", Names())
	}
}
