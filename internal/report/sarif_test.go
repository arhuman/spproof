package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arhuman/spproof/internal/engine"
	"github.com/arhuman/spproof/internal/rules"
)

// decodeSARIF renders a result and decodes it back into a generic document, so
// the assertions read the JSON a consumer actually receives rather than the Go
// structs that produced it.
func decodeSARIF(t *testing.T, r engine.Result) map[string]any {
	t.Helper()
	var b strings.Builder
	if err := SARIF(&b, r); err != nil {
		t.Fatalf("SARIF: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(b.String()), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return doc
}

// run returns the single run every spproof document carries.
func run(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	runs, ok := doc["runs"].([]any)
	if !ok || len(runs) != 1 {
		t.Fatalf("runs = %v, want exactly one", doc["runs"])
	}
	return runs[0].(map[string]any)
}

func TestSARIFCarriesSchemaAndVersion(t *testing.T) {
	doc := decodeSARIF(t, sample())
	if got := doc["version"]; got != "2.1.0" {
		t.Errorf("version = %v, want 2.1.0", got)
	}
	if got, ok := doc["$schema"].(string); !ok || !strings.Contains(got, "sarif-schema-2.1.0.json") {
		t.Errorf("$schema = %v, want the 2.1.0 schema URI", doc["$schema"])
	}
}

func TestSARIFDriverNamesTheTool(t *testing.T) {
	driver := run(t, decodeSARIF(t, sample()))["tool"].(map[string]any)["driver"].(map[string]any)
	if got := driver["name"]; got != "spproof" {
		t.Errorf("driver.name = %v, want spproof", got)
	}
	if got, ok := driver["informationUri"].(string); !ok || got == "" {
		t.Errorf("driver.informationUri = %v, want a URI", driver["informationUri"])
	}
	if got, ok := driver["version"].(string); !ok || got == "" {
		t.Errorf("driver.version = %v, want a version", driver["version"])
	}
}

// TestSARIFCatalogueHoldsEveryRule pins the invariant the format cannot express
// on its own: a rule that evaluated no file must stay visible and distinguishable
// from one that ran and held.
func TestSARIFCatalogueHoldsEveryRule(t *testing.T) {
	driver := run(t, decodeSARIF(t, sample()))["tool"].(map[string]any)["driver"].(map[string]any)
	descs, ok := driver["rules"].([]any)
	if !ok || len(descs) != 2 {
		t.Fatalf("rules = %v, want 2 descriptors", driver["rules"])
	}

	first := descs[0].(map[string]any)
	if got := first["id"]; got != "never-ran" {
		t.Fatalf("rules[0].id = %v, want never-ran", got)
	}
	if got := first["name"]; got != "pattern_absent" {
		t.Errorf("rules[0].name = %v, want the check name", got)
	}
	props := first["properties"].(map[string]any)
	if got := props["evaluatedFiles"]; got != float64(0) {
		t.Errorf("never-ran evaluatedFiles = %v, want 0", got)
	}

	ran := descs[1].(map[string]any)["properties"].(map[string]any)
	if got := ran["evaluatedFiles"]; got != float64(2) {
		t.Errorf("no-todo evaluatedFiles = %v, want 2", got)
	}
}

func TestSARIFResultsCarryLocationAndRuleIndex(t *testing.T) {
	results := run(t, decodeSARIF(t, sample()))["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}

	first := results[0].(map[string]any)
	if got := first["ruleId"]; got != "no-todo" {
		t.Errorf("ruleId = %v, want no-todo", got)
	}
	// no-todo is second in the catalogue, so a ruleIndex of 0 would point at
	// never-ran: the exact misattribution the index exists to prevent.
	if got := first["ruleIndex"]; got != float64(1) {
		t.Errorf("ruleIndex = %v, want 1", got)
	}
	if got := first["level"]; got != "error" {
		t.Errorf("level = %v, want error", got)
	}
	if got := first["message"].(map[string]any)["text"]; got != "forbidden pattern matched" {
		t.Errorf("message.text = %v", got)
	}

	phys := first["locations"].([]any)[0].(map[string]any)["physicalLocation"].(map[string]any)
	if got := phys["artifactLocation"].(map[string]any)["uri"]; got != "a.md" {
		t.Errorf("uri = %v, want a.md", got)
	}
	region := phys["region"].(map[string]any)
	if region["startLine"] != float64(2) || region["startColumn"] != float64(5) {
		t.Errorf("region = %v, want line 2 column 5", region)
	}
	if got := region["snippet"].(map[string]any)["text"]; got != "TODO" {
		t.Errorf("snippet.text = %v, want TODO", got)
	}
}

// TestSARIFFileScopedOmitsRegion pins that a whole-file finding never claims a
// position: an emitted zero region would read as line 0, which does not exist.
func TestSARIFFileScopedOmitsRegion(t *testing.T) {
	results := run(t, decodeSARIF(t, mixedScoped()))["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}

	scoped := results[0].(map[string]any)
	phys := scoped["locations"].([]any)[0].(map[string]any)["physicalLocation"].(map[string]any)
	if _, ok := phys["region"]; ok {
		t.Errorf("file-scoped result carries a region: %v", phys["region"])
	}
	if got := phys["artifactLocation"].(map[string]any)["uri"]; got != "a.go" {
		t.Errorf("uri = %v, want a.go", got)
	}

	lineScoped := results[1].(map[string]any)
	lphys := lineScoped["locations"].([]any)[0].(map[string]any)["physicalLocation"].(map[string]any)
	region, ok := lphys["region"].(map[string]any)
	if !ok {
		t.Fatalf("line-scoped result lost its region")
	}
	if region["startLine"] != float64(4) {
		t.Errorf("startLine = %v, want 4", region["startLine"])
	}
}

// TestSARIFToleratedIsSuppressedNotDropped pins the invariant that a run which
// absorbed a violation never renders like a run that found nothing.
func TestSARIFToleratedIsSuppressedNotDropped(t *testing.T) {
	r := engine.Result{
		Coverage: []engine.RuleCoverage{
			{RuleID: "no-todo", Check: "pattern_absent", EvaluatedFiles: 1},
		},
		Tolerated: []rules.Violation{
			{RuleID: "no-todo", Path: "a.md", Line: 3, Column: 1, Message: "forbidden pattern matched", Match: "TODO"},
		},
		Ratchets: []engine.RatchetStatus{
			{RuleID: "no-todo", Found: 1, Limit: 2, Scope: "run"},
		},
	}

	got := run(t, decodeSARIF(t, r))
	results := got["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("results = %d, want the tolerated finding present", len(results))
	}

	sups, ok := results[0].(map[string]any)["suppressions"].([]any)
	if !ok || len(sups) != 1 {
		t.Fatalf("suppressions = %v, want one", results[0].(map[string]any)["suppressions"])
	}
	sup := sups[0].(map[string]any)
	if got := sup["kind"]; got != "external" {
		t.Errorf("suppression kind = %v, want external", got)
	}
	if just, _ := sup["justification"].(string); !strings.Contains(just, "baseline of 2") {
		t.Errorf("justification = %q, want the declared baseline named", just)
	}

	ratchets := got["properties"].(map[string]any)["ratchets"].([]any)
	if len(ratchets) != 1 {
		t.Fatalf("properties.ratchets = %v, want one", ratchets)
	}
	entry := ratchets[0].(map[string]any)
	if entry["found"] != float64(1) || entry["limit"] != float64(2) || entry["exceeded"] != false {
		t.Errorf("ratchet entry = %v", entry)
	}
}

// TestSARIFFailingResultHasNoSuppression separates the two verdicts: a finding
// that failed the run must not carry the marker a consumer reads as dismissed.
func TestSARIFFailingResultHasNoSuppression(t *testing.T) {
	results := run(t, decodeSARIF(t, sample()))["results"].([]any)
	for i, res := range results {
		if _, ok := res.(map[string]any)["suppressions"]; ok {
			t.Errorf("results[%d] carries a suppression", i)
		}
	}
}

func TestSARIFCleanResultHasEmptyResults(t *testing.T) {
	doc := run(t, decodeSARIF(t, engine.Result{}))
	results, ok := doc["results"].([]any)
	if !ok {
		t.Fatalf("results = %v, want an empty array rather than null", doc["results"])
	}
	if len(results) != 0 {
		t.Errorf("results = %v, want empty", results)
	}
	descs, ok := doc["tool"].(map[string]any)["driver"].(map[string]any)["rules"].([]any)
	if !ok || len(descs) != 0 {
		t.Errorf("rules = %v, want an empty array", descs)
	}
}

func TestSARIFIsByteIdenticalAcrossRuns(t *testing.T) {
	r := engine.Result{
		Violations: sample().Violations,
		Coverage:   sample().Coverage,
		Tolerated: []rules.Violation{
			{RuleID: "no-todo", Path: "d.md", Line: 1, Column: 1, Message: "forbidden pattern matched", Match: "TODO"},
		},
		Ratchets: []engine.RatchetStatus{
			{RuleID: "no-todo", Found: 3, Limit: 3, Scope: "run"},
			{RuleID: "never-ran", Found: 0, Limit: 1, Scope: "file"},
		},
	}
	var first, second strings.Builder
	if err := SARIF(&first, r); err != nil {
		t.Fatalf("SARIF: %v", err)
	}
	if err := SARIF(&second, r); err != nil {
		t.Fatalf("SARIF: %v", err)
	}
	if first.String() != second.String() {
		t.Errorf("output differs across runs:\n%s\n%s", first.String(), second.String())
	}
}

// TestSARIFUnknownRuleOmitsIndex pins that a violation whose rule is absent from
// the catalogue does not silently borrow descriptor 0.
func TestSARIFUnknownRuleOmitsIndex(t *testing.T) {
	r := engine.Result{
		Violations: []rules.Violation{
			{RuleID: "absent", Path: "a.md", Line: 1, Column: 1, Message: "forbidden pattern matched"},
		},
		Coverage: []engine.RuleCoverage{
			{RuleID: "declared", Check: "pattern_absent", EvaluatedFiles: 1},
		},
	}
	results := run(t, decodeSARIF(t, r))["results"].([]any)
	if _, ok := results[0].(map[string]any)["ruleIndex"]; ok {
		t.Errorf("unknown rule carries a ruleIndex: %v", results[0])
	}
}

// TestSARIFPerFileRatchetJustification covers the other scope wording: a
// per-file baseline bounds the worst file, not the total, and a justification
// that said otherwise would misstate what absorbed the finding.
func TestSARIFPerFileRatchetJustification(t *testing.T) {
	r := engine.Result{
		Coverage:  []engine.RuleCoverage{{RuleID: "no-todo", Check: "pattern_absent", EvaluatedFiles: 2}},
		Tolerated: []rules.Violation{{RuleID: "no-todo", Path: "a.md", Line: 1, Column: 1, Message: "forbidden pattern matched"}},
		Ratchets:  []engine.RatchetStatus{{RuleID: "no-todo", Found: 1, Limit: 3, Scope: "file"}},
	}
	results := run(t, decodeSARIF(t, r))["results"].([]any)
	sup := results[0].(map[string]any)["suppressions"].([]any)[0].(map[string]any)
	just, _ := sup["justification"].(string)
	if !strings.Contains(just, "in the worst file") {
		t.Errorf("justification = %q, want the per-file scope named", just)
	}
}

// TestSARIFToleratedWithoutRatchetOmitsJustification pins that an absent
// ratchet yields no justification rather than one naming a limit of zero: a
// stated baseline that was never declared would be a fabricated reason.
func TestSARIFToleratedWithoutRatchetOmitsJustification(t *testing.T) {
	r := engine.Result{
		Coverage:  []engine.RuleCoverage{{RuleID: "no-todo", Check: "pattern_absent", EvaluatedFiles: 1}},
		Tolerated: []rules.Violation{{RuleID: "no-todo", Path: "a.md", Line: 1, Column: 1, Message: "forbidden pattern matched"}},
	}
	results := run(t, decodeSARIF(t, r))["results"].([]any)
	sup := results[0].(map[string]any)["suppressions"].([]any)[0].(map[string]any)
	if _, ok := sup["justification"]; ok {
		t.Errorf("suppression carries a justification with no ratchet: %v", sup)
	}
	if got := sup["kind"]; got != "external" {
		t.Errorf("kind = %v, want external", got)
	}
}
