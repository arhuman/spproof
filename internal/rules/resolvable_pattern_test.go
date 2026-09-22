package rules

import (
	"strings"
	"testing"
)

// gather drives the rule over a document and returns the candidates it handed
// the engine, which is where a contextual rule's whole output lives.
func gather(t *testing.T, with, path string, ft FileType, doc string) []Candidate {
	t.Helper()
	f, ok := Lookup("resolvable_local_path")
	if !ok {
		t.Fatal("resolvable_local_path is not registered")
	}
	spec := Spec{ID: "links", Check: "resolvable_local_path"}
	if with != "" {
		spec.With = WithYAML(with)
	}
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
	r.Init(FileMeta{Path: path, Type: ft})

	aware, _ := r.(ClassAware)
	var cls Classifier
	for i, line := range strings.Split(doc, "\n") {
		if aware != nil {
			aware.OnClass(cls.Classify(line))
		}
		r.OnLine(i+1, line)
	}
	r.Finish()
	return r.(Contextual).TakeCandidates()
}

func targets(cs []Candidate) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Target)
	}
	return out
}

// TestPatternExtractsBareReferences is the case the whole phase exists for: a
// path named in prose, with no markdown link syntax around it, which the
// built-in extractor cannot see.
func TestPatternExtractsBareReferences(t *testing.T) {
	const with = `pattern: 'references/[^\s)]+\.md'`
	doc := "See references/setup.md for details.\nAlso references/api.md.\n"

	got := targets(gather(t, with, "skills/a/SKILL.md", TypeMarkdown, doc))
	want := []string{"skills/a/references/setup.md", "skills/a/references/api.md"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("target %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestPatternCaptureGroupSelectsTheTarget pins that a capture group, when
// present, is what names the file. Without it a policy could only match
// patterns whose entire text is the path.
func TestPatternCaptureGroupSelectsTheTarget(t *testing.T) {
	const with = `pattern: 'see:\s*(\S+\.md)'`
	got := targets(gather(t, with, "doc.md", TypeMarkdown, "see: guide.md\n"))
	if len(got) != 1 || got[0] != "guide.md" {
		t.Fatalf("got %v, want [guide.md]: the group selects the target, not the whole match", got)
	}
}

// TestPatternColumnPointsAtTheTarget pins that the reported column lands on the
// extracted path rather than the start of the whole match, so an editor jumping
// to it puts the cursor on the thing that does not resolve.
func TestPatternColumnPointsAtTheTarget(t *testing.T) {
	const with = `pattern: 'see:\s*(\S+\.md)'`
	cs := gather(t, with, "doc.md", TypeMarkdown, "see: guide.md\n")
	if len(cs) != 1 {
		t.Fatalf("got %d candidates, want 1", len(cs))
	}
	if want := len("see: ") + 1; cs[0].Column != want {
		t.Errorf("Column = %d, want %d: the column points at the target", cs[0].Column, want)
	}
	if cs[0].Raw != "guide.md" {
		t.Errorf("Raw = %q, want the extracted target quoted back", cs[0].Raw)
	}
}

// TestPatternDefaultIsUnchanged is the regression guard: a policy that supplies
// no pattern must behave exactly as before, since every existing policy relies
// on the markdown link grammar.
func TestPatternDefaultIsUnchanged(t *testing.T) {
	doc := "A [link](target.md) and a bare references/x.md mention.\n"
	got := targets(gather(t, "", "doc.md", TypeMarkdown, doc))
	if len(got) != 1 || got[0] != "target.md" {
		t.Fatalf("got %v, want [target.md]: without a pattern only markdown links are seen", got)
	}
}

// TestPatternSkipsTheSameNonTargets pins that a custom pattern does not turn the
// check into a URL validator. A match that names no file in the tree is not a
// claim this check can prove, with or without a pattern.
func TestPatternSkipsTheSameNonTargets(t *testing.T) {
	const with = `pattern: '\S+\.md'`
	doc := "https://example.com/page.md\n/absolute/path.md\nrelative.md\n"
	got := targets(gather(t, with, "doc.md", TypeMarkdown, doc))
	if len(got) != 1 || got[0] != "relative.md" {
		t.Errorf("got %v, want [relative.md]: a URL and an absolute path name no file in the tree", got)
	}
}

// TestPatternAppliesToAnyTypeWhenGiven covers the widening. The markdown-only
// restriction exists because the built-in grammar is markdown's; a policy
// supplying its own extractor has made no such claim.
func TestPatternAppliesToAnyTypeWhenGiven(t *testing.T) {
	f, _ := Lookup("resolvable_local_path")
	if !f.AppliesTo(TypeMarkdown) {
		t.Error("AppliesTo(markdown) = false, want true")
	}

	// Without a pattern the check stays markdown-only, since its grammar is.
	plain := Spec{Check: "resolvable_local_path"}
	for _, ty := range []FileType{TypeGo, TypeYAML, TypeUnknown} {
		if appliesToType(t, f, plain, ty) {
			t.Errorf("AppliesTo(%q) = true without a pattern, want false", ty)
		}
	}

	withPattern := Spec{Check: "resolvable_local_path", With: WithYAML(`pattern: '\S+\.md'`)}
	for _, ty := range []FileType{TypeGo, TypeYAML, TypeMarkdown, TypeUnknown} {
		if !appliesToType(t, f, withPattern, ty) {
			t.Errorf("AppliesTo(%q) = false with a pattern, want true", ty)
		}
	}
}

// appliesToType asks the factory about a type for a specific spec, which is how
// applicability must be decided once it depends on configuration.
func appliesToType(t *testing.T, f Factory, s Spec, ty FileType) bool {
	t.Helper()
	if sa, ok := f.(SpecAware); ok {
		return sa.AppliesToSpec(s, ty)
	}
	return f.AppliesTo(ty)
}

// TestPatternExtractsFromGoFile is the widening end to end: the rule reads a
// path out of a Go file, which it could never do while restricted to markdown.
func TestPatternExtractsFromGoFile(t *testing.T) {
	const with = `pattern: '//go:embed (\S+)'`
	doc := "package main\n\n//go:embed templates/index.html\nvar tmpl string\n"
	got := targets(gather(t, with, "cmd/main.go", TypeGo, doc))
	if len(got) != 1 || got[0] != "cmd/templates/index.html" {
		t.Fatalf("got %v, want [cmd/templates/index.html]", got)
	}
}

// TestPatternMasksCodeInMarkdownOnly pins where masking applies. In markdown a
// fenced example is not a reference, so it stays masked. In a Go file the whole
// document is code, and masking it would make the rule find nothing at all.
func TestPatternMasksCodeInMarkdownOnly(t *testing.T) {
	const with = `pattern: 'refs/\S+\.md'`

	md := "Real refs/a.md here.\n\n```\nrefs/example.md in a fence\n```\n"
	if got := targets(gather(t, with, "doc.md", TypeMarkdown, md)); len(got) != 1 || got[0] != "refs/a.md" {
		t.Errorf("markdown: got %v, want [refs/a.md]: a fenced path is an example", got)
	}

	// The same content in a Go file: the classifier sees a fence, but a Go file
	// has no markdown fences, so nothing may be masked away.
	gо := "// refs/a.md\n\n```\nrefs/example.md\n```\n"
	if got := targets(gather(t, with, "a.go", TypeGo, gо)); len(got) != 2 {
		t.Errorf("go: got %v, want both paths: markdown masking must not reach a Go file", got)
	}
}

// TestPatternSkipCodeIsRefused pins that the shared field stays out. Masking
// here is decided by file type, not by a policy switch, so accepting skip_code
// would suggest a control that does not exist.
func TestPatternSkipCodeIsRefused(t *testing.T) {
	f, _ := Lookup("resolvable_local_path")
	with := "pattern: '\\S+\\.md'\nskip_code: true"
	if err := f.Validate(Spec{Check: "resolvable_local_path", With: WithYAML(with)}); err == nil {
		t.Error("Validate = nil, want an error")
	}
}

// TestPatternValidateRejectsBadConfig keeps the widened check inside strict
// loading: a pattern that cannot compile, or a field from another check, must
// refuse the run.
func TestPatternValidateRejectsBadConfig(t *testing.T) {
	f, _ := Lookup("resolvable_local_path")
	bad := []struct{ name, with string }{
		{"uncompilable pattern", `pattern: '[unclosed'`},
		{"unknown key", "pattern: 'x'\nseverity: high"},
		{"max from another check", "pattern: 'x'\nmax: 10"},
		{"empty pattern", `pattern: ''`},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if err := f.Validate(Spec{Check: "resolvable_local_path", With: WithYAML(tc.with)}); err == nil {
				t.Error("Validate = nil, want an error")
			}
		})
	}
}

// TestPatternStillRejectsEmptyWith pins that a with block naming nothing is
// still a refusal: the check has no optional configuration other than pattern.
func TestPatternStillRejectsEmptyWith(t *testing.T) {
	f, _ := Lookup("resolvable_local_path")
	if err := f.Validate(Spec{Check: "resolvable_local_path", With: WithYAML("{}")}); err == nil {
		t.Error("Validate = nil, want an error for a with block that configures nothing")
	}
}
