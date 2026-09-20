package rules

import (
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
