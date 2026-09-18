package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arhuman/spproof/internal/engine"
	"github.com/arhuman/spproof/internal/rules"
)

func sample() engine.Result {
	return engine.Result{
		Violations: []rules.Violation{
			{RuleID: "no-todo", Path: "a.md", Line: 2, Column: 5, Message: "forbidden pattern matched", Match: "TODO"},
			{RuleID: "no-todo", Path: "b/c.go", Line: 10, Column: 1, Message: "forbidden pattern matched", Match: "TODO"},
		},
		Coverage: []engine.RuleCoverage{
			{RuleID: "never-ran", Check: "pattern_absent", EvaluatedFiles: 0},
			{RuleID: "no-todo", Check: "pattern_absent", EvaluatedFiles: 2},
		},
	}
}

func TestTextFormat(t *testing.T) {
	var b strings.Builder
	if err := Text(&b, sample()); err != nil {
		t.Fatalf("Text: %v", err)
	}
	want := "a.md:2:5: [no-todo] forbidden pattern matched\n" +
		"b/c.go:10:1: [no-todo] forbidden pattern matched\n"
	if b.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", b.String(), want)
	}
}

func TestTextCleanResultWritesNothing(t *testing.T) {
	var b strings.Builder
	if err := Text(&b, engine.Result{}); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if b.String() != "" {
		t.Errorf("got %q, want empty output", b.String())
	}
}

// TestJSONIsSARIFShaped checks the field names SARIF uses, so a later SARIF
// emitter is a projection of this structure rather than a rewrite.
func TestJSONIsSARIFShaped(t *testing.T) {
	var b strings.Builder
	if err := JSON(&b, sample()); err != nil {
		t.Fatalf("JSON: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(b.String()), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	catalogue, ok := got["rules"].([]any)
	if !ok || len(catalogue) != 2 {
		t.Fatalf("rules catalogue = %v, want 2 entries", got["rules"])
	}
	first := catalogue[0].(map[string]any)
	if first["id"] != "never-ran" {
		t.Errorf("catalogue[0].id = %v, want never-ran", first["id"])
	}
	if first["evaluatedFiles"].(float64) != 0 {
		t.Errorf("never-ran evaluatedFiles = %v, want 0", first["evaluatedFiles"])
	}

	results, ok := got["results"].([]any)
	if !ok || len(results) != 2 {
		t.Fatalf("results = %v, want 2 entries", got["results"])
	}
	r0 := results[0].(map[string]any)
	if r0["ruleId"] != "no-todo" {
		t.Errorf("ruleId = %v, want no-todo", r0["ruleId"])
	}
	loc := r0["locations"].([]any)[0].(map[string]any)["physicalLocation"].(map[string]any)
	if uri := loc["artifactLocation"].(map[string]any)["uri"]; uri != "a.md" {
		t.Errorf("uri = %v, want a.md", uri)
	}
	region := loc["region"].(map[string]any)
	if region["startLine"].(float64) != 2 {
		t.Errorf("startLine = %v, want 2", region["startLine"])
	}
	if region["startColumn"].(float64) != 5 {
		t.Errorf("startColumn = %v, want 5", region["startColumn"])
	}
}

// TestJSONCleanResultHasEmptyArrays pins that a clean run emits [] rather than
// null, so a consumer can index the output without a nil check.
func TestJSONCleanResultHasEmptyArrays(t *testing.T) {
	var b strings.Builder
	if err := JSON(&b, engine.Result{}); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(b.String(), `"results": []`) {
		t.Errorf("got %s, want empty results array", b.String())
	}
}

func TestJSONIsByteIdenticalAcrossRuns(t *testing.T) {
	var a, b strings.Builder
	if err := JSON(&a, sample()); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if err := JSON(&b, sample()); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if a.String() != b.String() {
		t.Error("two renderings of the same result differ")
	}
}
