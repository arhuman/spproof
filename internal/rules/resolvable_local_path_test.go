package rules

import (
	"testing"
)

func newResolvable(t *testing.T, filePath string) Rule {
	t.Helper()
	f, ok := Lookup("resolvable_local_path")
	if !ok {
		t.Fatal("resolvable_local_path is not registered")
	}
	spec := Spec{ID: "test-rule", Check: "resolvable_local_path"}
	if err := f.Validate(spec); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	r := f.New(spec)
	r.Init(FileMeta{Path: filePath, Type: TypeMarkdown})
	return r
}

// candidates runs the rule over a whole file and returns what it accumulated.
func candidates(t *testing.T, r Rule, lines []string) []Candidate {
	t.Helper()
	c, ok := r.(Contextual)
	if !ok {
		t.Fatal("rule does not implement Contextual")
	}
	var out []Candidate
	for n, line := range lines {
		if v := r.OnLine(n+1, line); len(v) != 0 {
			t.Fatalf("OnLine returned %d violations, want 0: the verdict needs the filesystem", len(v))
		}
		out = append(out, c.TakeCandidates()...)
	}
	if v := r.Finish(); len(v) != 0 {
		t.Fatalf("Finish returned %d violations, want 0", len(v))
	}
	return append(out, c.TakeCandidates()...)
}

func TestResolvableLocalPathIsRegistered(t *testing.T) {
	found := false
	for _, n := range Names() {
		if n == "resolvable_local_path" {
			found = true
		}
	}
	if !found {
		t.Errorf("Names() = %v, missing resolvable_local_path", Names())
	}
}

func TestResolvableLocalPathAppliesToMarkdownOnly(t *testing.T) {
	f, _ := Lookup("resolvable_local_path")
	want := map[FileType]bool{
		TypeMarkdown: true,
		TypeGo:       false,
		TypePerl:     false,
		TypeYAML:     false,
		TypeUnknown:  false,
	}
	for ty, w := range want {
		if got := f.AppliesTo(ty); got != w {
			t.Errorf("AppliesTo(%q) = %v, want %v", ty, got, w)
		}
	}
}

// TestResolvableLocalPathSkipsNonLocalTargets pins the targets the check makes
// no claim about. A verdict on any of them would be invented, not proved.
func TestResolvableLocalPathSkipsNonLocalTargets(t *testing.T) {
	skipped := []string{
		"http://example.com/a.md",
		"https://example.com/a.md",
		"mailto:someone@example.com",
		"ftp://example.com/a.md",
		"custom+scheme-1.0://host/a.md",
		"//example.com/a.md",
		"/etc/passwd",
		"#section",
	}
	for _, target := range skipped {
		t.Run(target, func(t *testing.T) {
			r := newResolvable(t, "doc.md")
			got := candidates(t, r, []string{"see [x](" + target + ") here"})
			if len(got) != 0 {
				t.Errorf("got %d candidates for %q, want 0: %+v", len(got), target, got)
			}
		})
	}
}

func TestResolvableLocalPathStripsFragmentAndQuery(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"../docs/adr.md#rationale", "docs/adr.md"},
		{"../docs/adr.md?v=2", "docs/adr.md"},
		{"../docs/adr.md?v=2#rationale", "docs/adr.md"},
		{"../docs/adr.md", "docs/adr.md"},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			r := newResolvable(t, "internal/notes/a.md")
			got := candidates(t, r, []string{"see [x](" + c.raw + ")"})
			if len(got) != 1 {
				t.Fatalf("got %d candidates, want 1", len(got))
			}
			if got[0].Target != "internal/"+c.want {
				t.Errorf("Target = %q, want %q", got[0].Target, "internal/"+c.want)
			}
			if got[0].Raw != c.raw {
				t.Errorf("Raw = %q, want %q: the message names what is written", got[0].Raw, c.raw)
			}
		})
	}
}

// TestResolvableLocalPathResolvesRelativeToLinkingFile pins the resolution
// base: the directory of the file holding the link, not the walk root. The
// second case escapes the root, which is legitimate and must not be clamped.
func TestResolvableLocalPathResolvesRelativeToLinkingFile(t *testing.T) {
	cases := []struct {
		file   string
		target string
		want   string
	}{
		{"internal/notes/a.md", "../docs/x.md", "internal/docs/x.md"},
		{"notes/a.md", "../../outside/x.md", "../outside/x.md"},
		{"a.md", "docs/x.md", "docs/x.md"},
		{"deep/nested/a.md", "./sibling.md", "deep/nested/sibling.md"},
	}
	for _, c := range cases {
		t.Run(c.file+" "+c.target, func(t *testing.T) {
			r := newResolvable(t, c.file)
			got := candidates(t, r, []string{"[x](" + c.target + ")"})
			if len(got) != 1 {
				t.Fatalf("got %d candidates, want 1", len(got))
			}
			if got[0].Target != c.want {
				t.Errorf("Target = %q, want %q", got[0].Target, c.want)
			}
		})
	}
}

func TestResolvableLocalPathBothLinkSyntaxes(t *testing.T) {
	r := newResolvable(t, "doc.md")
	got := candidates(t, r, []string{
		"an inline [one](inline.md) link",
		"[label]: definition.md",
	})
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2: %+v", len(got), got)
	}
	if got[0].Target != "inline.md" || got[0].Line != 1 {
		t.Errorf("candidate 0 = %+v, want inline.md on line 1", got[0])
	}
	if got[1].Target != "definition.md" || got[1].Line != 2 {
		t.Errorf("candidate 1 = %+v, want definition.md on line 2", got[1])
	}
}

// TestResolvableLocalPathColumnPointsAtTarget pins the column on the first rune
// of the target, which is what a reader must edit.
func TestResolvableLocalPathColumnPointsAtTarget(t *testing.T) {
	r := newResolvable(t, "doc.md")
	line := "see [text](missing.md)"
	got := candidates(t, r, []string{line})
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1", len(got))
	}
	want := len("see [text](") + 1
	if got[0].Column != want {
		t.Errorf("Column = %d, want %d", got[0].Column, want)
	}
}

// TestResolvableLocalPathColumnCountsRunes pins that the column is a rune
// offset, so an editor cursor lands on the target in a UTF-8 file.
func TestResolvableLocalPathColumnCountsRunes(t *testing.T) {
	r := newResolvable(t, "doc.md")
	got := candidates(t, r, []string{"voir [été](missing.md)"})
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1", len(got))
	}
	if want := len("voir [xxx](") + 1; got[0].Column != want {
		t.Errorf("Column = %d, want %d", got[0].Column, want)
	}
}

func TestResolvableLocalPathTwoLinksOnOneLine(t *testing.T) {
	r := newResolvable(t, "doc.md")
	got := candidates(t, r, []string{"[a](one.md) and [b](two.md)"})
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2", len(got))
	}
	if got[0].Column >= got[1].Column {
		t.Errorf("columns %d and %d are not increasing, so the two share a sort key", got[0].Column, got[1].Column)
	}
}

// TestResolvableLocalPathIgnoresTitleSuffix keeps the title out of the target:
// [t](path "Title") points at path.
func TestResolvableLocalPathIgnoresTitleSuffix(t *testing.T) {
	r := newResolvable(t, "doc.md")
	got := candidates(t, r, []string{`[t](target.md "A Title")`})
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1", len(got))
	}
	if got[0].Target != "target.md" {
		t.Errorf("Target = %q, want target.md", got[0].Target)
	}
}

// TestResolvableLocalPathTakeCandidatesClears proves draining twice does not
// report the same link twice, which is the engine's per-line contract.
func TestResolvableLocalPathTakeCandidatesClears(t *testing.T) {
	r := newResolvable(t, "doc.md")
	c := r.(Contextual)
	r.OnLine(1, "[a](one.md)")
	if got := c.TakeCandidates(); len(got) != 1 {
		t.Fatalf("first take = %d candidates, want 1", len(got))
	}
	if got := c.TakeCandidates(); len(got) != 0 {
		t.Errorf("second take = %d candidates, want 0", len(got))
	}
}

// TestResolvableLocalPathViolationDistinguishesMissFromError pins the settled
// semantics: both fail, and the payload is the only place they differ.
func TestResolvableLocalPathViolationDistinguishesMissFromError(t *testing.T) {
	c := Candidate{RuleID: "r", Path: "doc.md", Target: "x.md", Raw: "x.md", Line: 3, Column: 5}

	miss := c.Violation(nil)
	denied := c.Violation(errDenied)

	if miss.Message == denied.Message {
		t.Error("miss and access error render the same message")
	}
	if miss.FileScoped || denied.FileScoped {
		t.Error("a link violation is line-scoped: it has a position to point at")
	}
	for _, v := range []Violation{miss, denied} {
		if v.Line != 3 || v.Column != 5 || v.Path != "doc.md" || v.RuleID != "r" {
			t.Errorf("violation lost its location: %+v", v)
		}
	}
}
