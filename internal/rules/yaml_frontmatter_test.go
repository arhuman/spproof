package rules

import (
	"errors"
	"strings"
	"testing"
)

// run drives the rule over a whole document and returns everything it reported,
// in the order the engine would collect it: OnLine first, then Finish.
func runFrontmatter(t *testing.T, with, doc string) []Violation {
	t.Helper()
	f, ok := Lookup("yaml_frontmatter")
	if !ok {
		t.Fatal("yaml_frontmatter is not registered")
	}
	spec := Spec{ID: "fm", Check: "yaml_frontmatter", With: WithYAML(with)}
	if err := f.Validate(spec); err != nil {
		t.Fatalf("Validate(%q): %v", with, err)
	}
	if p, ok := f.(Preparer); ok {
		cfg, err := p.Prepare(spec)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		spec.Config = cfg
	}
	r := f.New(spec)
	r.Init(FileMeta{Path: "a.md", Type: TypeMarkdown})

	var got []Violation
	if doc != "" {
		for i, line := range strings.Split(doc, "\n") {
			got = append(got, r.OnLine(i+1, line)...)
		}
	}
	return append(got, r.Finish()...)
}

const goodDoc = `---
name: thing
description: does a thing
---

# Body

Text.
`

// TestFrontmatterHolds is the control: a document satisfying the policy reports
// nothing, so every failing case below is about the policy and not the parser.
func TestFrontmatterHolds(t *testing.T) {
	if v := runFrontmatter(t, `required_keys: ["description"]`, goodDoc); len(v) != 0 {
		t.Errorf("got %d violations, want 0: %+v", len(v), v)
	}
}

// TestFrontmatterMissingIsFileScoped pins the scoping rule. A document with no
// frontmatter has no line to point at, so the violation must carry Line 0 and
// FileScoped, which is what the renderer and the sort both branch on.
func TestFrontmatterMissingIsFileScoped(t *testing.T) {
	v := runFrontmatter(t, `required_keys: ["description"]`, "# Just a heading\n\nText.\n")
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1: %+v", len(v), v)
	}
	if !v[0].FileScoped {
		t.Error("missing frontmatter must be file-scoped: it has no position")
	}
	if v[0].Line != 0 || v[0].Column != 0 {
		t.Errorf("Line = %d, Column = %d, want 0 and 0 on a file-scoped violation", v[0].Line, v[0].Column)
	}
}

// TestFrontmatterEmptyFile covers the file that ends before the rule sees
// anything at all. It is the same verdict as a file whose first line is prose.
func TestFrontmatterEmptyFile(t *testing.T) {
	v := runFrontmatter(t, `required_keys: ["description"]`, "")
	if len(v) != 1 || !v[0].FileScoped {
		t.Errorf("got %+v, want one file-scoped violation", v)
	}
}

// TestFrontmatterOpenerMustBeFirstLine pins that `---` only opens frontmatter at
// the top of the file. A horizontal rule further down is body content, and
// treating it as an opener would invent a frontmatter block the author never
// wrote.
func TestFrontmatterOpenerMustBeFirstLine(t *testing.T) {
	doc := "# Heading\n\n---\n\nname: not-frontmatter\n"
	v := runFrontmatter(t, `required_keys: ["name"]`, doc)
	if len(v) != 1 || !v[0].FileScoped {
		t.Errorf("got %+v, want one file-scoped violation: a rule mid-body is not an opener", v)
	}
}

// TestFrontmatterUnclosedIsLineScoped covers the fence that never closes. The
// rule must report rather than buffer to the end of the file, and the finding
// points at the opener, which is the line the author has to fix.
func TestFrontmatterUnclosedIsLineScoped(t *testing.T) {
	doc := "---\nname: thing\ndescription: text\n\n# Body never closes the fence\n"
	v := runFrontmatter(t, `required_keys: ["name"]`, doc)
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1: %+v", len(v), v)
	}
	if v[0].FileScoped {
		t.Error("an unclosed fence has a position: the opener")
	}
	if v[0].Line != 1 {
		t.Errorf("Line = %d, want 1: the violation points at the opener", v[0].Line)
	}
	if !strings.Contains(v[0].Message, "closed") {
		t.Errorf("Message = %q, want it to name the unclosed fence", v[0].Message)
	}
}

// TestFrontmatterBoundIsAViolationNotATruncation is the memory invariant made
// observable. The rule must stop buffering at the cap and report, never hold a
// whole file because a fence never closed.
func TestFrontmatterBoundIsAViolationNotATruncation(t *testing.T) {
	var b strings.Builder
	b.WriteString("---\n")
	for i := 0; i < maxFrontmatterLines+50; i++ {
		b.WriteString("key")
		b.WriteString(strings.Repeat("x", 3))
		b.WriteString(": value\n")
	}
	b.WriteString("---\n")

	v := runFrontmatter(t, `required_keys: ["name"]`, b.String())
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1: %+v", len(v), v)
	}
	if !strings.Contains(v[0].Message, "closed") && !strings.Contains(v[0].Message, "lines") {
		t.Errorf("Message = %q, want it to explain the overrun", v[0].Message)
	}
}

// TestFrontmatterMalformedYAMLIsAFinding is the exit-code contract. A broken
// frontmatter block is a violation about the file (exit 1), never an error that
// aborts the run (exit 2), which is what the policy file's own parse failure is.
func TestFrontmatterMalformedYAMLIsAFinding(t *testing.T) {
	doc := "---\nname: [unclosed\n---\n\n# Body\n"
	v := runFrontmatter(t, `required_keys: ["name"]`, doc)
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1: %+v", len(v), v)
	}
	if v[0].FileScoped {
		t.Error("malformed YAML has a position: the block it is in")
	}
	if v[0].Line < 1 {
		t.Errorf("Line = %d, want a line inside the block", v[0].Line)
	}
}

// TestFrontmatterParseErrorPointsAtTheBlock pins that a parse failure reports a
// line inside the file and carries no block-relative number in its text.
//
// It deliberately does not assert the exact offending line. yaml.v3 reports this
// error class zero-indexed, floored at 1, and names where the broken construct
// opened rather than where parsing failed, so no offset maps it correctly for
// every document. Pointing at the closing fence is coarse but never wrong, and a
// test demanding more would be pinning a library quirk rather than a contract.
func TestFrontmatterParseErrorPointsAtTheBlock(t *testing.T) {
	doc := "---\nname: fine\nbroken: [unclosed\n---\n\n# Body\n"
	v := runFrontmatter(t, `required_keys: ["name"]`, doc)
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1: %+v", len(v), v)
	}
	if v[0].FileScoped {
		t.Error("a parse failure has a position: the block it is in")
	}
	if v[0].Line != 4 {
		t.Errorf("Line = %d, want 4: the block's closing fence", v[0].Line)
	}
	// The library's own line number is block-relative, so quoting it verbatim
	// would name a line of the file that is not the one it means.
	if strings.Contains(v[0].Message, "line ") {
		t.Errorf("Message = %q, must not carry a block-relative line number", v[0].Message)
	}
}

// TestFrontmatterNotAMapping covers a block that parses but is not key/value.
// A sequence satisfies no required key, so reporting it as "missing key" would
// name the wrong problem.
func TestFrontmatterNotAMapping(t *testing.T) {
	doc := "---\n- one\n- two\n---\n\n# Body\n"
	v := runFrontmatter(t, `required_keys: ["name"]`, doc)
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1: %+v", len(v), v)
	}
	if !strings.Contains(v[0].Message, "mapping") {
		t.Errorf("Message = %q, want it to say the block is not a mapping", v[0].Message)
	}
}

// TestFrontmatterRequiredKeys covers the core predicate, including that a key
// present but empty still counts as present: the policy asked for the key, not
// for a value, and inventing the stronger rule would fail documents it never
// meant to.
func TestFrontmatterRequiredKeys(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want int
	}{
		{"all present", "---\nname: a\ndescription: b\n---\n", 0},
		{"one missing", "---\nname: a\n---\n", 1},
		{"both missing", "---\nother: a\n---\n", 2},
		{"present but empty", "---\nname: a\ndescription:\n---\n", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := runFrontmatter(t, `required_keys: ["name", "description"]`, tc.doc)
			if len(v) != tc.want {
				t.Errorf("got %d violations, want %d: %+v", len(v), tc.want, v)
			}
		})
	}
}

// TestFrontmatterMissingKeyOrderIsDeterministic pins that two missing keys are
// reported in the policy's declared order rather than a map's iteration order.
// Output ordering is a load-bearing invariant, and a range over a map would
// break it intermittently.
func TestFrontmatterMissingKeyOrderIsDeterministic(t *testing.T) {
	const with = `required_keys: ["alpha", "beta", "gamma"]`
	for i := 0; i < 20; i++ {
		v := runFrontmatter(t, with, "---\nother: x\n---\n")
		if len(v) != 3 {
			t.Fatalf("got %d violations, want 3", len(v))
		}
		for j, want := range []string{"alpha", "beta", "gamma"} {
			if !strings.Contains(v[j].Message, want) {
				t.Fatalf("violation %d = %q, want it to name %q", j, v[j].Message, want)
			}
		}
	}
}

// TestFrontmatterForbidExtraKeys covers the opt-in closed schema. It is off by
// default: a policy naming required keys has not thereby said the list is
// exhaustive.
func TestFrontmatterForbidExtraKeys(t *testing.T) {
	const doc = "---\nname: a\nextra: b\n---\n"
	if v := runFrontmatter(t, `required_keys: ["name"]`, doc); len(v) != 0 {
		t.Errorf("got %d violations by default, want 0: extra keys are allowed unless forbidden", len(v))
	}
	v := runFrontmatter(t, "required_keys: [\"name\"]\nforbid_extra_keys: true", doc)
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1: %+v", len(v), v)
	}
	if !strings.Contains(v[0].Message, "extra") {
		t.Errorf("Message = %q, want it to name the extra key", v[0].Message)
	}
}

// TestFrontmatterKeyConstraints is what folds yaml_sequence_contains into this
// check: a constraint on one key's value, over a scalar or a sequence.
func TestFrontmatterKeyConstraints(t *testing.T) {
	const with = `key_constraints:
  tools:
    forbidden: ["Edit", "NotebookEdit"]`
	tests := []struct {
		name string
		doc  string
		want int
	}{
		{"clean sequence", "---\ntools: [Read, Grep]\n---\n", 0},
		{"one forbidden", "---\ntools: [Read, Edit]\n---\n", 1},
		{"both forbidden", "---\ntools: [Edit, NotebookEdit]\n---\n", 2},
		{"forbidden as scalar", "---\ntools: Edit\n---\n", 1},
		{"key absent entirely", "---\nname: a\n---\n", 0},
		{"block sequence", "---\ntools:\n  - Read\n  - Edit\n---\n", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := runFrontmatter(t, with, tc.doc)
			if len(v) != tc.want {
				t.Errorf("got %d violations, want %d: %+v", len(v), tc.want, v)
			}
		})
	}
}

// TestFrontmatterRequirePresentOptOut covers the one case where a missing block
// is acceptable: a policy checking the shape of frontmatter where it exists,
// without mandating it. It defaults to on, so the common case needs no field.
func TestFrontmatterRequirePresentOptOut(t *testing.T) {
	const doc = "# No frontmatter here\n"
	if v := runFrontmatter(t, `required_keys: ["name"]`, doc); len(v) != 1 {
		t.Errorf("got %d violations by default, want 1: frontmatter is required unless opted out", len(v))
	}
	with := "required_keys: [\"name\"]\nrequire_present: false"
	if v := runFrontmatter(t, with, doc); len(v) != 0 {
		t.Errorf("got %d violations with require_present: false, want 0: %+v", len(v), v)
	}
}

// TestFrontmatterCustomMessage pins that this check honors message: like every
// other, through Spec.Msg rather than by assigning the text directly.
func TestFrontmatterCustomMessage(t *testing.T) {
	f, _ := Lookup("yaml_frontmatter")
	spec := Spec{
		ID:      "fm",
		Check:   "yaml_frontmatter",
		With:    WithYAML(`required_keys: ["description"]`),
		Message: "every skill needs a description",
	}
	r := f.New(spec)
	r.Init(FileMeta{Path: "a.md", Type: TypeMarkdown})
	for i, line := range strings.Split("---\nname: a\n---\n", "\n") {
		r.OnLine(i+1, line)
	}
	v := r.Finish()
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1", len(v))
	}
	if v[0].Message != "every skill needs a description" {
		t.Errorf("Message = %q, want the policy's wording", v[0].Message)
	}
}

// TestFrontmatterAppliesToMarkdownOnly pins the narrowing. A .yaml file is a
// whole document with no fence to find, so running this check on one would ask
// a question the file's shape cannot answer.
func TestFrontmatterAppliesToMarkdownOnly(t *testing.T) {
	f, _ := Lookup("yaml_frontmatter")
	if !f.AppliesTo(TypeMarkdown) {
		t.Error("AppliesTo(markdown) = false, want true")
	}
	for _, ty := range []FileType{TypeYAML, TypeGo, TypePerl, TypeUnknown} {
		if f.AppliesTo(ty) {
			t.Errorf("AppliesTo(%q) = true, want false", ty)
		}
	}
}

// TestFrontmatterValidateRejectsBadConfig keeps the check inside the strict
// loading contract: an unusable rule refuses the run rather than rendering as a
// rule that held.
func TestFrontmatterValidateRejectsBadConfig(t *testing.T) {
	f, _ := Lookup("yaml_frontmatter")
	bad := []struct {
		name string
		with string
	}{
		{"unknown key", `required_keys: ["a"]` + "\nseverity: high"},
		{"wrong type", `required_keys: "not a list"`},
		{"pattern from another check", `required_keys: ["a"]` + "\npattern: \"x\""},
		{"no configuration at all", "{}"},
		{"empty required_keys with nothing else", `required_keys: []`},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if err := f.Validate(Spec{Check: "yaml_frontmatter", With: WithYAML(tc.with)}); err == nil {
				t.Error("Validate = nil, want an error")
			}
		})
	}
}

// TestFrontmatterValidateAcceptsConstraintOnlyPolicy covers a policy that names
// no required key but constrains a value, which is a complete rule on its own.
func TestFrontmatterValidateAcceptsConstraintOnlyPolicy(t *testing.T) {
	f, _ := Lookup("yaml_frontmatter")
	with := "key_constraints:\n  tools:\n    forbidden: [\"Edit\"]"
	if err := f.Validate(Spec{Check: "yaml_frontmatter", With: WithYAML(with)}); err != nil {
		t.Errorf("Validate: %v, want nil", err)
	}
}

// TestFrontmatterSkipCodeIsRefused pins that the shared field does not leak in.
// The check reads no line class, so accepting skip_code would let a policy
// believe it turned something on that this rule never consults.
func TestFrontmatterSkipCodeIsRefused(t *testing.T) {
	f, _ := Lookup("yaml_frontmatter")
	with := "required_keys: [\"a\"]\nskip_code: true"
	err := f.Validate(Spec{Check: "yaml_frontmatter", With: WithYAML(with)})
	if !errors.Is(err, ErrBadWith) {
		t.Errorf("Validate = %v, want ErrBadWith", err)
	}
}
