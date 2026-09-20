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

// mixedScoped is one file carrying both a file-scoped and a line-scoped
// violation, already ordered as the engine orders them: file-scoped first.
func mixedScoped() engine.Result {
	return engine.Result{
		Violations: []rules.Violation{
			{RuleID: "license-header", Path: "a.go", FileScoped: true, Message: "required pattern matched no line"},
			{RuleID: "no-todo", Path: "a.go", Line: 4, Column: 2, Message: "forbidden pattern matched", Match: "TODO"},
		},
		Coverage: []engine.RuleCoverage{
			{RuleID: "license-header", Check: "pattern_present", EvaluatedFiles: 1},
			{RuleID: "no-todo", Check: "pattern_absent", EvaluatedFiles: 1},
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

// TestTextFileScopedOmitsPosition pins that a file-scoped violation never
// renders a 0:0 location, which no editor could jump to and no caller could
// parse as a position.
func TestTextFileScopedOmitsPosition(t *testing.T) {
	var b strings.Builder
	if err := Text(&b, mixedScoped()); err != nil {
		t.Fatalf("Text: %v", err)
	}
	want := "a.go: [license-header] required pattern matched no line\n" +
		"a.go:4:2: [no-todo] forbidden pattern matched\n"
	if b.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", b.String(), want)
	}
	if strings.Contains(b.String(), ":0:0") {
		t.Errorf("output contains a 0:0 location: %q", b.String())
	}
}

// TestJSONFileScopedOmitsRegion pins the SARIF-correct encoding: a location
// with an artifactLocation and no region means "the whole artifact". A zero
// region would instead claim a position that does not exist.
func TestJSONFileScopedOmitsRegion(t *testing.T) {
	var b strings.Builder
	r := engine.Result{Violations: mixedScoped().Violations[:1]}
	if err := JSON(&b, r); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if strings.Contains(b.String(), `"region"`) {
		t.Errorf("file-scoped result emitted a region:\n%s", b.String())
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(b.String()), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	loc := got["results"].([]any)[0].(map[string]any)["locations"].([]any)[0].(map[string]any)["physicalLocation"].(map[string]any)
	if uri := loc["artifactLocation"].(map[string]any)["uri"]; uri != "a.go" {
		t.Errorf("uri = %v, want a.go", uri)
	}
	if _, present := loc["region"]; present {
		t.Error("physicalLocation has a region key, want it absent")
	}
}

// TestJSONMixedScopeKeepsLineScopedRegion guards the omission from becoming a
// blanket one: the line-scoped violation alongside it must still carry a region.
func TestJSONMixedScopeKeepsLineScopedRegion(t *testing.T) {
	var b strings.Builder
	if err := JSON(&b, mixedScoped()); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(b.String()), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	results := got["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("results = %d entries, want 2", len(results))
	}

	physical := func(i int) map[string]any {
		return results[i].(map[string]any)["locations"].([]any)[0].(map[string]any)["physicalLocation"].(map[string]any)
	}
	if _, present := physical(0)["region"]; present {
		t.Error("file-scoped result has a region, want it absent")
	}
	region, present := physical(1)["region"].(map[string]any)
	if !present {
		t.Fatal("line-scoped result lost its region")
	}
	if region["startLine"].(float64) != 4 {
		t.Errorf("startLine = %v, want 4", region["startLine"])
	}
	if region["startColumn"].(float64) != 2 {
		t.Errorf("startColumn = %v, want 2", region["startColumn"])
	}
}

func TestMixedScopeRenderingIsByteIdenticalAcrossRuns(t *testing.T) {
	var jsonA, jsonB, textA, textB strings.Builder
	for _, p := range []struct {
		j, t *strings.Builder
	}{{&jsonA, &textA}, {&jsonB, &textB}} {
		if err := JSON(p.j, mixedScoped()); err != nil {
			t.Fatalf("JSON: %v", err)
		}
		if err := Text(p.t, mixedScoped()); err != nil {
			t.Fatalf("Text: %v", err)
		}
	}
	if jsonA.String() != jsonB.String() {
		t.Error("two JSON renderings of the same result differ")
	}
	if textA.String() != textB.String() {
		t.Error("two text renderings of the same result differ")
	}
}

// TestTextToleratedRunIsNotSilent is the invariant this feature could most
// easily break: a run whose findings were all absorbed by a baseline must not
// render identically to a run that found nothing. Silence has to mean nothing
// was found.
func TestTextToleratedRunIsNotSilent(t *testing.T) {
	var clean strings.Builder
	if err := Text(&clean, engine.Result{}); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if clean.String() != "" {
		t.Fatalf("a clean result wrote %q, want nothing", clean.String())
	}

	absorbed := engine.Result{
		Tolerated: []rules.Violation{{RuleID: "no-todo", Path: "a.md", Line: 1, Column: 1, Message: "m"}},
		Ratchets:  []engine.RatchetStatus{{RuleID: "no-todo", Found: 1, Limit: 3, Scope: "run"}},
	}
	var got strings.Builder
	if err := Text(&got, absorbed); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if got.String() == "" {
		t.Fatal("a fully tolerated run wrote nothing, so it reads as clean")
	}
	for _, want := range []string{"no-todo", "1", "baseline 3"} {
		if !strings.Contains(got.String(), want) {
			t.Errorf("output %q is missing %q", got.String(), want)
		}
	}
}

// TestTextExceededRatchetPrintsNoSummary: once a rule fails, its violations are
// the message. A summary line saying "tolerated" alongside them would be wrong.
func TestTextExceededRatchetPrintsNoSummary(t *testing.T) {
	r := engine.Result{
		Violations: []rules.Violation{{RuleID: "no-todo", Path: "a.md", Line: 1, Column: 1, Message: "m"}},
		Ratchets:   []engine.RatchetStatus{{RuleID: "no-todo", Found: 4, Limit: 3, Scope: "run", Exceeded: true}},
	}
	var got strings.Builder
	if err := Text(&got, r); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if strings.Contains(got.String(), "tolerated") {
		t.Errorf("output %q calls a failing rule tolerated", got.String())
	}
}

// TestJSONCarriesToleratedSeparately pins the split: results is the set that
// failed, tolerated is what the baseline absorbed, and both are present.
func TestJSONCarriesToleratedSeparately(t *testing.T) {
	r := engine.Result{
		Coverage:  []engine.RuleCoverage{{RuleID: "no-todo", Check: "pattern_absent", EvaluatedFiles: 1}},
		Tolerated: []rules.Violation{{RuleID: "no-todo", Path: "a.md", Line: 2, Column: 3, Message: "m", Match: "TODO"}},
		Ratchets:  []engine.RatchetStatus{{RuleID: "no-todo", Found: 1, Limit: 3, Scope: "run"}},
	}
	var buf strings.Builder
	if err := JSON(&buf, r); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var doc struct {
		Rules []struct {
			ID      string `json:"id"`
			Ratchet *struct {
				Found    int    `json:"found"`
				Limit    int    `json:"limit"`
				Scope    string `json:"scope"`
				Exceeded bool   `json:"exceeded"`
			} `json:"ratchet"`
		} `json:"rules"`
		Results   []json.RawMessage `json:"results"`
		Tolerated []json.RawMessage `json:"tolerated"`
	}
	if err := json.Unmarshal([]byte(buf.String()), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Results) != 0 {
		t.Errorf("results = %d, want 0: nothing failed", len(doc.Results))
	}
	if len(doc.Tolerated) != 1 {
		t.Errorf("tolerated = %d, want 1", len(doc.Tolerated))
	}
	if doc.Rules[0].Ratchet == nil {
		t.Fatal("the rule catalogue carries no ratchet status")
	}
	if got := doc.Rules[0].Ratchet; got.Found != 1 || got.Limit != 3 || got.Scope != "run" || got.Exceeded {
		t.Errorf("ratchet = %+v, want found 1 limit 3 scope run not exceeded", got)
	}
}

// TestJSONOmitsRatchetWhenUnused keeps the document unchanged for a policy that
// declares no baseline.
func TestJSONOmitsRatchetWhenUnused(t *testing.T) {
	r := engine.Result{Coverage: []engine.RuleCoverage{{RuleID: "r", Check: "pattern_absent"}}}
	var buf strings.Builder
	if err := JSON(&buf, r); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if strings.Contains(buf.String(), "ratchet") || strings.Contains(buf.String(), "tolerated") {
		t.Errorf("output carries ratchet fields for a policy with none: %s", buf.String())
	}
}
