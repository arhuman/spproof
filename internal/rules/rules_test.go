package rules

import (
	"errors"
	"regexp"
	"testing"
)

// TestSpecMsgFallsBackToGenerated pins both halves of the contract: a policy
// that set no message keeps today's generated text byte for byte, and one that
// set a message replaces it rather than appending to it.
func TestSpecMsgFallsBackToGenerated(t *testing.T) {
	var none Spec
	if got := none.Msg("generated text"); got != "generated text" {
		t.Errorf("Msg = %q, want the generated text when the policy set none", got)
	}

	custom := Spec{Message: "say it this way"}
	if got := custom.Msg("generated text"); got != "say it this way" {
		t.Errorf("Msg = %q, want the policy's wording to replace the generated text", got)
	}
}

// TestCustomMessageReachesTheViolation walks a real rule end to end, since a
// Spec helper that works in isolation is worth nothing if a rule bypasses it.
func TestCustomMessageReachesTheViolation(t *testing.T) {
	f, ok := Lookup("pattern_absent")
	if !ok {
		t.Fatal("pattern_absent is not registered")
	}
	spec := Spec{
		ID:      "no-todo",
		Check:   "pattern_absent",
		Pattern: regexp.MustCompile("TODO"),
		Message: "file a ticket instead of leaving a marker",
	}
	r := f.New(spec)
	r.Init(FileMeta{Path: "a.md", Type: TypeMarkdown})

	v := r.OnLine(1, "TODO here")
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1", len(v))
	}
	if v[0].Message != "file a ticket instead of leaving a marker" {
		t.Errorf("Message = %q, want the policy's wording", v[0].Message)
	}
	// The matched text stays available separately, so a custom message costs no
	// machine-readable information.
	if v[0].Match != "TODO" {
		t.Errorf("Match = %q, want TODO to survive the custom message", v[0].Match)
	}
}

// TestCandidateCustomMessage covers the contextual path, where the message
// travels on the Candidate because the resolver answers it after the rule that
// produced it is gone.
func TestCandidateCustomMessage(t *testing.T) {
	c := Candidate{
		RuleID:  "links",
		Path:    "a.md",
		Target:  "missing.md",
		Raw:     "./missing.md",
		Line:    3,
		Column:  5,
		Message: "point at a file that exists, or drop the link",
	}
	if got := c.Violation(nil).Message; got != "point at a file that exists, or drop the link" {
		t.Errorf("Message = %q, want the policy's wording", got)
	}

	c.Message = ""
	if got := c.Violation(nil).Message; got == "" {
		t.Error("empty Message produced no generated text")
	}
}

// TestSkipCodeRejectedByChecksThatIgnoreIt pins the refusal. Accepting the
// field and then ignoring it would run a narrower rule than the policy
// declared and report that it held, which strict loading exists to prevent.
func TestSkipCodeRejectedByChecksThatIgnoreIt(t *testing.T) {
	specs := map[string]Spec{
		"file_line_max":                {Check: "file_line_max", Max: 10},
		"pattern_present":              {Check: "pattern_present", Pattern: regexp.MustCompile("x")},
		"comment_line_char_max":        {Check: "comment_line_char_max", Max: 10},
		"comment_line_consecutive_max": {Check: "comment_line_consecutive_max", Max: 10},
		"resolvable_local_path":        {Check: "resolvable_local_path"},
	}
	for name, base := range specs {
		t.Run(name, func(t *testing.T) {
			f, ok := Lookup(name)
			if !ok {
				t.Fatalf("%s is not registered", name)
			}
			if err := f.Validate(base); err != nil {
				t.Fatalf("Validate without skip_code: %v", err)
			}
			base.SkipCode = true
			if err := f.Validate(base); !errors.Is(err, ErrSkipCodeUnsupported) {
				t.Errorf("Validate with skip_code = %v, want ErrSkipCodeUnsupported", err)
			}
		})
	}
}

// TestSkipCodeAcceptedByPatternAbsent is the other half: the one check that
// honors the field must not reject it.
func TestSkipCodeAcceptedByPatternAbsent(t *testing.T) {
	f, ok := Lookup("pattern_absent")
	if !ok {
		t.Fatal("pattern_absent is not registered")
	}
	spec := Spec{Check: "pattern_absent", Pattern: regexp.MustCompile("x"), SkipCode: true}
	if err := f.Validate(spec); err != nil {
		t.Errorf("Validate: %v, want nil", err)
	}
}

// TestPatternRejectedByChecksThatIgnoreIt pins the same refusal for pattern.
// A check that never reads Spec.Pattern must refuse one: honoring the field
// nowhere and accepting it here would let a policy declare a constraint the
// engine never applies, then report that the rule held.
func TestPatternRejectedByChecksThatIgnoreIt(t *testing.T) {
	specs := map[string]Spec{
		"file_line_max":                {Check: "file_line_max", Max: 10},
		"comment_line_char_max":        {Check: "comment_line_char_max", Max: 10},
		"comment_line_consecutive_max": {Check: "comment_line_consecutive_max", Max: 10},
		"resolvable_local_path":        {Check: "resolvable_local_path"},
	}
	for name, base := range specs {
		t.Run(name, func(t *testing.T) {
			f, ok := Lookup(name)
			if !ok {
				t.Fatalf("%s is not registered", name)
			}
			if err := f.Validate(base); err != nil {
				t.Fatalf("Validate without pattern: %v", err)
			}
			base.Pattern = regexp.MustCompile("ignored")
			if err := f.Validate(base); !errors.Is(err, ErrPatternUnsupported) {
				t.Errorf("Validate with pattern = %v, want ErrPatternUnsupported", err)
			}
		})
	}
}

// TestMaxRejectedByChecksThatIgnoreIt is the third field on the same footing.
// Max is the one field whose absence is indistinguishable from a zero, so a
// check that ignores it can only refuse a positive one.
func TestMaxRejectedByChecksThatIgnoreIt(t *testing.T) {
	specs := map[string]Spec{
		"pattern_absent":        {Check: "pattern_absent", Pattern: regexp.MustCompile("x")},
		"pattern_present":       {Check: "pattern_present", Pattern: regexp.MustCompile("x")},
		"resolvable_local_path": {Check: "resolvable_local_path"},
	}
	for name, base := range specs {
		t.Run(name, func(t *testing.T) {
			f, ok := Lookup(name)
			if !ok {
				t.Fatalf("%s is not registered", name)
			}
			if err := f.Validate(base); err != nil {
				t.Fatalf("Validate without max: %v", err)
			}
			base.Max = 42
			if err := f.Validate(base); !errors.Is(err, ErrMaxUnsupported) {
				t.Errorf("Validate with max = %v, want ErrMaxUnsupported", err)
			}
		})
	}
}

// TestFieldsAcceptedByTheChecksThatReadThem is the other half of both tables:
// the refusals must not spread to the checks the fields belong to.
func TestFieldsAcceptedByTheChecksThatReadThem(t *testing.T) {
	ok := []struct {
		name string
		spec Spec
	}{
		{"pattern_absent", Spec{Check: "pattern_absent", Pattern: regexp.MustCompile("x")}},
		{"pattern_present", Spec{Check: "pattern_present", Pattern: regexp.MustCompile("x")}},
		{"file_line_max", Spec{Check: "file_line_max", Max: 10}},
		{"comment_line_char_max", Spec{Check: "comment_line_char_max", Max: 10}},
		{"comment_line_consecutive_max", Spec{Check: "comment_line_consecutive_max", Max: 10}},
	}
	for _, c := range ok {
		t.Run(c.name, func(t *testing.T) {
			f, found := Lookup(c.name)
			if !found {
				t.Fatalf("%s is not registered", c.name)
			}
			if err := f.Validate(c.spec); err != nil {
				t.Errorf("Validate: %v, want nil", err)
			}
		})
	}
}

// TestPatternAbsentSkipsCode walks the rule end to end: a match inside a code
// span is ignored while one in prose on the same line still reports, and the
// reported column points into the original line rather than the masked copy.
func TestPatternAbsentSkipsCode(t *testing.T) {
	f, _ := Lookup("pattern_absent")
	spec := Spec{ID: "no-todo", Check: "pattern_absent", Pattern: regexp.MustCompile("TODO"), SkipCode: true}
	r := f.New(spec)
	r.Init(FileMeta{Path: "a.md", Type: TypeMarkdown})

	text := "`TODO` is quoted but TODO is not"
	aware, ok := r.(ClassAware)
	if !ok {
		t.Fatal("pattern_absent does not implement ClassAware")
	}
	var c Classifier
	aware.OnClass(c.Classify(text))

	v := r.OnLine(1, text)
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1: the code span must be ignored", len(v))
	}
	wantCol := len([]rune("`TODO` is quoted but ")) + 1
	if v[0].Column != wantCol {
		t.Errorf("Column = %d, want %d: the column must point into the real line", v[0].Column, wantCol)
	}
	if v[0].Match != "TODO" {
		t.Errorf("Match = %q, want TODO from the original text", v[0].Match)
	}
}

// TestPatternAbsentWithoutSkipCodeIsUnchanged pins the default: a policy that
// did not ask for prose-only sees exactly what it saw before.
func TestPatternAbsentWithoutSkipCodeIsUnchanged(t *testing.T) {
	f, _ := Lookup("pattern_absent")
	spec := Spec{ID: "no-todo", Check: "pattern_absent", Pattern: regexp.MustCompile("TODO")}
	r := f.New(spec)
	r.Init(FileMeta{Path: "a.md", Type: TypeMarkdown})

	text := "`TODO` is quoted but TODO is not"
	r.(ClassAware).OnClass(LineClass{Fenced: true})
	if v := r.OnLine(1, text); len(v) != 2 {
		t.Errorf("got %d violations, want 2: without skip_code the class is ignored", len(v))
	}
}
