package engine

import (
	"testing"
	"testing/fstest"
)

// mixedTreePolicy pairs a comment check, which no Markdown file can decide,
// with a pattern check, which every file can. Both select the same two files, so
// the only thing that can separate them is applicability.
const mixedTreePolicy = `version: 1
rules:
  - id: comment-run
    check: comment_line_consecutive_max
    files: ["**/*.go", "**/*.md"]
    with:
      max: 2
  - id: no-todo
    check: pattern_absent
    files: ["**/*.go", "**/*.md"]
    with:
      pattern: "TODO"
`

// mixedTree holds one .go and one .md file that would both fail the comment rule
// if it ran on them: each has a run of three lines starting with "//" and each
// carries a TODO for the pattern rule.
var mixedTree = fstest.MapFS{
	"a.go":    {Data: []byte("// one\n// two\n// three\nfunc main() {} // TODO\n")},
	"docs.md": {Data: []byte("// one\n// two\n// three\nprose with a TODO\n")},
}

// TestApplicabilityDropsRuleForFileNotFile proves R12 end to end: the comment
// rule falls away on the Markdown file while the pattern rule still runs on it.
// The file is not skipped, and nothing errors or warns.
func TestApplicabilityDropsRuleForFileNotFile(t *testing.T) {
	r, err := Run(load(t, mixedTreePolicy), sources(t, mixedTree))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	byRule := map[string][]string{}
	for _, v := range r.Violations {
		byRule[v.RuleID] = append(byRule[v.RuleID], v.Path)
	}

	if got := byRule["comment-run"]; len(got) != 1 || got[0] != "a.go" {
		t.Errorf("comment-run fired on %v, want only [a.go]: it is inapplicable to markdown", got)
	}
	if got := byRule["no-todo"]; len(got) != 2 || got[0] != "a.go" || got[1] != "docs.md" {
		t.Errorf("no-todo fired on %v, want [a.go docs.md]: it applies to every type", got)
	}
}

// TestApplicabilityCoverageReflectsTheDrop asserts the drop on the field that
// makes it legible: evaluatedFiles is what separates "the rule held" from "the
// rule never ran", so a dropped rule must not silently inflate it.
func TestApplicabilityCoverageReflectsTheDrop(t *testing.T) {
	r, err := Run(load(t, mixedTreePolicy), sources(t, mixedTree))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := map[string]int{"comment-run": 1, "no-todo": 2}
	got := map[string]int{}
	for _, c := range r.Coverage {
		got[c.RuleID] = c.EvaluatedFiles
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("EvaluatedFiles[%s] = %d, want %d", id, got[id], w)
		}
	}
}

// TestApplicabilityMarkdownOnlyTreeRunsNothingAndSaysSo covers the vacuous case:
// a tree where the only rule is inapplicable everywhere produces a clean result
// with zero evaluated files, which must not read as a proof that it held.
func TestApplicabilityMarkdownOnlyTreeRunsNothingAndSaysSo(t *testing.T) {
	const p = `version: 1
rules:
  - id: comment-run
    check: comment_line_consecutive_max
    files: ["**/*.md"]
    with:
      max: 1
`
	files := fstest.MapFS{"docs.md": {Data: []byte("// one\n// two\n// three\n")}}
	r, err := Run(load(t, p), sources(t, files))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !r.OK() {
		t.Errorf("want no violations, got %+v", r.Violations)
	}
	if len(r.Coverage) != 1 || r.Coverage[0].EvaluatedFiles != 0 {
		t.Errorf("coverage = %+v, want comment-run evaluated on 0 files", r.Coverage)
	}
}
