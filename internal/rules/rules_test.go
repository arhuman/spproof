package rules

import (
	"errors"
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
		With:    WithYAML(`pattern: "TODO"`),
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

// TestWrongFieldForTheCheckIsRefused is the load-bearing test of the `with:`
// design: a field that belongs to another check is an unknown key, so it
// refuses the run instead of loading and reporting that the rule held.
//
// Before the block existed these fields were shared, and every one of these
// cases loaded silently. The table is deliberately exhaustive over the
// wrong-field pairs rather than sampling, since the whole class is what
// regressed last time.
func TestWrongFieldForTheCheckIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		check string
		with  string
	}{
		{"pattern on file_line_max", "file_line_max", "max: 10\npattern: \"x\""},
		{"pattern on comment_line_char_max", "comment_line_char_max", "max: 10\npattern: \"x\""},
		{"pattern on comment_line_consecutive_max", "comment_line_consecutive_max", "max: 10\npattern: \"x\""},
		{"max on pattern_absent", "pattern_absent", "pattern: \"x\"\nmax: 42"},
		{"max on pattern_present", "pattern_present", "pattern: \"x\"\nmax: 42"},
		{"skip_code on pattern_present", "pattern_present", "pattern: \"x\"\nskip_code: true"},
		{"skip_code on file_line_max", "file_line_max", "max: 10\nskip_code: true"},
		// pattern is NOT listed for resolvable_local_path: that check takes one,
		// as its own tests cover. Every other field is still foreign to it.
		{"max on resolvable_local_path", "resolvable_local_path", "max: 10"},
		{"skip_code on resolvable_local_path", "resolvable_local_path", "skip_code: true"},
		{"unknown key on pattern_absent", "pattern_absent", "pattern: \"x\"\nseverity: high"},
		{"unknown key on file_line_max", "file_line_max", "max: 10\nseverity: high"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, ok := Lookup(c.check)
			if !ok {
				t.Fatalf("%s is not registered", c.check)
			}
			err := f.Validate(Spec{Check: c.check, With: WithYAML(c.with)})
			if !errors.Is(err, ErrBadWith) {
				t.Errorf("Validate = %v, want ErrBadWith", err)
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
	spec := Spec{Check: "pattern_absent", With: WithYAML(`pattern: "x"
skip_code: true`)}
	if err := f.Validate(spec); err != nil {
		t.Errorf("Validate: %v, want nil", err)
	}
}

// TestMaxZeroIsDistinctFromAbsent pins what the pointer in maxConfig bought.
// Both are refusals today, but for different reasons, and a check wanting a
// meaningful zero can now tell them apart.
func TestMaxZeroIsDistinctFromAbsent(t *testing.T) {
	f, _ := Lookup("file_line_max")
	absent := f.Validate(Spec{Check: "file_line_max", With: WithYAML("{}")})
	zero := f.Validate(Spec{Check: "file_line_max", With: WithYAML("max: 0")})
	if !errors.Is(absent, ErrMaxRequired) || !errors.Is(zero, ErrMaxRequired) {
		t.Fatalf("absent = %v, zero = %v, want both ErrMaxRequired", absent, zero)
	}
	if absent.Error() == zero.Error() {
		t.Errorf("absent and explicit zero produced the same message %q", absent.Error())
	}
}

// TestPrepareCachesConfigForNew pins the optimization that keeps the policy
// parser out of the walk: every configured check caches its decoded config, and
// a Rule built from the cached Spec behaves identically to one built without it.
//
// The behavioural half matters more than the caching half. A cache that New
// silently ignored, or that returned a stale value, would be invisible to every
// other test here.
func TestPrepareCachesConfigForNew(t *testing.T) {
	cases := []struct {
		check string
		with  string
		line  string
		want  int
	}{
		{"pattern_absent", `pattern: "TODO"`, "a TODO here", 1},
		// pattern_present reports absence, so a line that does not match is the
		// violating case.
		{"pattern_present", `pattern: "TODO"`, "nothing", 1},
		{"file_line_max", "max: 1", "one line", 0},
		{"comment_line_char_max", "max: 4", "// far too long", 1},
		{"comment_line_consecutive_max", "max: 5", "// one", 0},
	}
	for _, c := range cases {
		t.Run(c.check, func(t *testing.T) {
			f, ok := Lookup(c.check)
			if !ok {
				t.Fatalf("%s is not registered", c.check)
			}
			p, ok := f.(Preparer)
			if !ok {
				t.Fatalf("%s does not implement Preparer", c.check)
			}
			spec := Spec{ID: "r", Check: c.check, With: WithYAML(c.with)}
			cfg, err := p.Prepare(spec)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if cfg == nil {
				t.Fatal("Prepare returned no config to cache")
			}

			// Same spec, once with the cache populated and once without: the
			// rule must not be able to tell the difference.
			cached := spec
			cached.Config = cfg
			got := runOneLine(f.New(cached), c.line)
			want := runOneLine(f.New(spec), c.line)
			if got != want {
				t.Errorf("cached spec gave %d violations, uncached gave %d", got, want)
			}
			if got != c.want {
				t.Errorf("got %d violations, want %d", got, c.want)
			}
		})
	}
}

// runOneLine drives a rule over a single line and returns the total violation
// count from both OnLine and Finish, since a check may report from either.
func runOneLine(r Rule, text string) int {
	r.Init(FileMeta{Path: "a.go", Type: TypeGo})
	n := len(r.OnLine(1, text))
	return n + len(r.Finish())
}

// TestFieldsAcceptedByTheChecksThatReadThem is the other half of the table:
// the refusals must not spread to the checks the fields belong to.
func TestFieldsAcceptedByTheChecksThatReadThem(t *testing.T) {
	ok := []struct {
		name string
		spec Spec
	}{
		{"pattern_absent", Spec{Check: "pattern_absent", With: WithYAML(`pattern: "x"`)}},
		{"pattern_present", Spec{Check: "pattern_present", With: WithYAML(`pattern: "x"`)}},
		{"file_line_max", Spec{Check: "file_line_max", With: WithYAML("max: 10")}},
		{"comment_line_char_max", Spec{Check: "comment_line_char_max", With: WithYAML("max: 10")}},
		{"comment_line_consecutive_max", Spec{Check: "comment_line_consecutive_max", With: WithYAML("max: 10")}},
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
	spec := Spec{ID: "no-todo", Check: "pattern_absent", With: WithYAML(`pattern: "TODO"
skip_code: true`)}
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
	spec := Spec{ID: "no-todo", Check: "pattern_absent", With: WithYAML(`pattern: "TODO"`)}
	r := f.New(spec)
	r.Init(FileMeta{Path: "a.md", Type: TypeMarkdown})

	text := "`TODO` is quoted but TODO is not"
	r.(ClassAware).OnClass(LineClass{Fenced: true})
	if v := r.OnLine(1, text); len(v) != 2 {
		t.Errorf("got %d violations, want 2: without skip_code the class is ignored", len(v))
	}
}
