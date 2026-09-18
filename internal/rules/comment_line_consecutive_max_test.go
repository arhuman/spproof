package rules

import (
	"errors"
	"testing"
)

func newConsecutiveMax(t *testing.T, ty FileType, limit int) Rule {
	t.Helper()
	f, ok := Lookup("comment_line_consecutive_max")
	if !ok {
		t.Fatal("comment_line_consecutive_max is not registered")
	}
	spec := Spec{ID: "test-rule", Check: "comment_line_consecutive_max", Max: limit}
	if err := f.Validate(spec); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	r := f.New(spec)
	r.Init(FileMeta{Path: "a.go", Type: ty})
	return r
}

// feed runs the rule over a whole file and returns every violation it produced,
// from OnLine and from Finish alike.
func feed(r Rule, lines []string) []Violation {
	var out []Violation
	for n, line := range lines {
		out = append(out, r.OnLine(n+1, line)...)
	}
	return append(out, r.Finish()...)
}

func TestCommentLineConsecutiveMaxUnderLimitHolds(t *testing.T) {
	r := newConsecutiveMax(t, TypeGo, 3)
	v := feed(r, []string{"// one", "// two", "// three", "code()"})
	if len(v) != 0 {
		t.Errorf("got %d violations, want 0: a run of exactly max is allowed", len(v))
	}
}

// TestCommentLineConsecutiveMaxOverLimitFailsOncePerRun pins the once-per-run
// verdict. A per-line verdict would report this single block twice.
func TestCommentLineConsecutiveMaxOverLimitFailsOncePerRun(t *testing.T) {
	r := newConsecutiveMax(t, TypeGo, 3)
	v := feed(r, []string{"code()", "// one", "// two", "// three", "// four", "// five", "code()"})
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1", len(v))
	}
	got := v[0]
	if got.Line != 2 {
		t.Errorf("Line = %d, want 2 (the first line of the run)", got.Line)
	}
	if got.Column != 1 {
		t.Errorf("Column = %d, want 1", got.Column)
	}
	if got.FileScoped {
		t.Error("FileScoped = true, want false: the run has a first line to point at")
	}
	if got.RuleID != "test-rule" {
		t.Errorf("RuleID = %q, want test-rule", got.RuleID)
	}
}

func TestCommentLineConsecutiveMaxRunBrokenByCodeLine(t *testing.T) {
	r := newConsecutiveMax(t, TypeGo, 2)
	v := feed(r, []string{"// one", "// two", "code()", "// three", "// four"})
	if len(v) != 0 {
		t.Errorf("got %d violations, want 0: the code line splits four comments into two runs of two", len(v))
	}
}

// TestCommentLineConsecutiveMaxRunBrokenByBlankLine pins the decided semantics:
// a blank line is not a comment line, so it breaks the run like any other
// non-comment line. One definition of "comment line" governs both checks.
func TestCommentLineConsecutiveMaxRunBrokenByBlankLine(t *testing.T) {
	r := newConsecutiveMax(t, TypeGo, 2)
	v := feed(r, []string{"// one", "// two", "", "// three", "// four"})
	if len(v) != 0 {
		t.Errorf("got %d violations, want 0: a blank line breaks the run", len(v))
	}
}

// TestCommentLineConsecutiveMaxRunOpenAtEOFIsReported covers the run the file
// ends on: nothing closes it but Finish, so without the flush the block would
// go unreported.
func TestCommentLineConsecutiveMaxRunOpenAtEOFIsReported(t *testing.T) {
	r := newConsecutiveMax(t, TypeGo, 2)
	var fromOnLine []Violation
	lines := []string{"code()", "// one", "// two", "// three"}
	for n, line := range lines {
		fromOnLine = append(fromOnLine, r.OnLine(n+1, line)...)
	}
	if len(fromOnLine) != 0 {
		t.Fatalf("OnLine returned %d violations, want 0: the run is still open", len(fromOnLine))
	}
	v := r.Finish()
	if len(v) != 1 {
		t.Fatalf("Finish returned %d violations, want 1", len(v))
	}
	if v[0].Line != 2 {
		t.Errorf("Line = %d, want 2 (the first line of the run)", v[0].Line)
	}
}

func TestCommentLineConsecutiveMaxTwoBadRunsReportTwice(t *testing.T) {
	r := newConsecutiveMax(t, TypeGo, 1)
	v := feed(r, []string{"// a", "// b", "code()", "// c", "// d"})
	if len(v) != 2 {
		t.Fatalf("got %d violations, want 2: two distinct over-long runs", len(v))
	}
	if v[0].Line != 1 {
		t.Errorf("first violation Line = %d, want 1", v[0].Line)
	}
	if v[1].Line != 4 {
		t.Errorf("second violation Line = %d, want 4", v[1].Line)
	}
}

func TestCommentLineConsecutiveMaxPerlAndYAMLUseHash(t *testing.T) {
	r := newConsecutiveMax(t, TypeYAML, 2)
	v := feed(r, []string{"# one", "# two", "# three", "key: value"})
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1", len(v))
	}
	if v[0].Line != 1 {
		t.Errorf("Line = %d, want 1", v[0].Line)
	}
}

func TestCommentLineConsecutiveMaxAppliesOnlyWhereCommentsExist(t *testing.T) {
	f, _ := Lookup("comment_line_consecutive_max")
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

func TestCommentLineConsecutiveMaxValidateRequiresPositiveMax(t *testing.T) {
	f, _ := Lookup("comment_line_consecutive_max")
	for _, limit := range []int{0, -1} {
		err := f.Validate(Spec{ID: "a", Check: "comment_line_consecutive_max", Max: limit})
		if !errors.Is(err, ErrMaxRequired) {
			t.Errorf("Validate(max=%d) = %v, want ErrMaxRequired", limit, err)
		}
	}
	if err := f.Validate(Spec{ID: "a", Check: "comment_line_consecutive_max", Max: 1}); err != nil {
		t.Errorf("Validate(max=1) = %v, want nil", err)
	}
}

func TestCommentLineConsecutiveMaxIsRegistered(t *testing.T) {
	found := false
	for _, n := range Names() {
		if n == "comment_line_consecutive_max" {
			found = true
		}
	}
	if !found {
		t.Errorf("Names() = %v, missing comment_line_consecutive_max", Names())
	}
}
