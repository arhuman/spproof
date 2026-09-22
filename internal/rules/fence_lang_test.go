package rules

import (
	"errors"
	"strings"
	"testing"
)

// runFenced drives pattern_absent over a document, feeding the classifier the
// same way the engine does, and returns everything reported.
func runFenced(t *testing.T, with, doc string) []Violation {
	t.Helper()
	f, ok := Lookup("pattern_absent")
	if !ok {
		t.Fatal("pattern_absent is not registered")
	}
	spec := Spec{ID: "r", Check: "pattern_absent", With: WithYAML(with)}
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

	aware := r.(ClassAware)
	var cls Classifier
	var out []Violation
	for i, line := range strings.Split(doc, "\n") {
		aware.OnClass(cls.Classify(line))
		out = append(out, r.OnLine(i+1, line)...)
	}
	return append(out, r.Finish()...)
}

// The motivating document: a makefile example that must use tabs, beside a
// shell example and prose that both legitimately contain leading spaces.
const fenceDoc = "Prose with  leading spaces is fine.\n" +
	"\n" +
	"```makefile\n" +
	"build:\n" +
	"    go build ./...\n" +
	"```\n" +
	"\n" +
	"```sh\n" +
	"    indented shell is fine\n" +
	"```\n" +
	"\n" +
	"    an indented prose block is fine too\n"

// TestFenceLangConfinesToOneLanguage is the whole point: a rule about makefile
// examples must not report the shell block or the prose around it.
func TestFenceLangConfinesToOneLanguage(t *testing.T) {
	const with = "pattern: '^ +[^ ]'\nfence_lang: makefile"
	v := runFenced(t, with, fenceDoc)
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1: %+v", len(v), v)
	}
	if v[0].Line != 5 {
		t.Errorf("Line = %d, want 5: the space-indented makefile recipe", v[0].Line)
	}
}

// TestFenceLangIgnoresTheFenceMarkers pins that the ``` lines delimiting the
// block are not themselves scanned. They carry the block's info string, so a
// rule reading it naively would report on the delimiter rather than the content.
func TestFenceLangIgnoresTheFenceMarkers(t *testing.T) {
	// A pattern matching a backtick: the only backticks in the document are the
	// fence markers themselves, so a correct implementation reports nothing.
	const with = "pattern: '`'\nfence_lang: makefile"
	if v := runFenced(t, with, fenceDoc); len(v) != 0 {
		t.Fatalf("got %d violations, want 0: the ``` markers delimit the block, they are not in it: %+v", len(v), v)
	}

	// And the block's content is still reached, so the exclusion above is not
	// just the rule failing to look anywhere at all.
	const anyWord = "pattern: '\\w+'\nfence_lang: makefile"
	v := runFenced(t, anyWord, fenceDoc)
	if len(v) == 0 {
		t.Fatal("got 0 violations for the block's content: the rule looked nowhere")
	}
	for _, got := range v {
		if got.Line == 3 || got.Line == 6 {
			t.Errorf("line %d is a fence marker and must not be scanned", got.Line)
		}
	}
}

// TestFenceLangWithoutMatchHolds covers the document that has no block of the
// named language at all: nothing to scan means nothing to report.
func TestFenceLangWithoutMatchHolds(t *testing.T) {
	const with = "pattern: '\\S'\nfence_lang: python"
	if v := runFenced(t, with, fenceDoc); len(v) != 0 {
		t.Errorf("got %d violations, want 0: no python block exists: %+v", len(v), v)
	}
}

// TestFenceLangIsCaseInsensitive covers the info string written in any case,
// since markdown authors write ```Makefile and ```MAKEFILE interchangeably.
func TestFenceLangIsCaseInsensitive(t *testing.T) {
	doc := "```Makefile\n    spaces\n```\n"
	const with = "pattern: '^ +[^ ]'\nfence_lang: makefile"
	if v := runFenced(t, with, doc); len(v) != 1 {
		t.Errorf("got %d violations, want 1: the info string case must not matter", len(v))
	}
}

// TestFenceLangIgnoresInfoStringArguments covers a fence whose info string
// carries more than the language, as in ```go title="x". The language is the
// first word, and requiring an exact match would silently skip such a block.
func TestFenceLangIgnoresInfoStringArguments(t *testing.T) {
	doc := "```makefile title=\"Makefile\"\n    spaces\n```\n"
	const with = "pattern: '^ +[^ ]'\nfence_lang: makefile"
	if v := runFenced(t, with, doc); len(v) != 1 {
		t.Errorf("got %d violations, want 1: the language is the first word of the info string", len(v))
	}
}

// TestFenceLangAndSkipCodeAreExclusive pins that the two cannot be combined.
// They are opposite ends of one axis: skip_code blanks all code, fence_lang
// keeps only one block's code, so a policy setting both has contradicted itself
// and must be told rather than silently given one of the two.
func TestFenceLangAndSkipCodeAreExclusive(t *testing.T) {
	f, _ := Lookup("pattern_absent")
	with := "pattern: 'x'\nskip_code: true\nfence_lang: makefile"
	err := f.Validate(Spec{Check: "pattern_absent", With: WithYAML(with)})
	if err == nil {
		t.Fatal("Validate = nil, want a refusal: skip_code and fence_lang contradict")
	}
	if !errors.Is(err, ErrFenceLangConflict) {
		t.Errorf("Validate = %v, want ErrFenceLangConflict", err)
	}
}

// TestFenceLangUnchangedWithoutTheField is the regression guard: a policy that
// sets neither field sees every line, exactly as before.
func TestFenceLangUnchangedWithoutTheField(t *testing.T) {
	const with = "pattern: '^ +[^ ]'"
	v := runFenced(t, with, fenceDoc)
	// The space-indented makefile recipe, the shell line, and the prose block.
	if len(v) != 3 {
		t.Errorf("got %d violations, want 3: without the field every line is scanned: %+v", len(v), v)
	}
}

// TestFenceLangAppliesToMarkdownOnly pins the narrowing. Fences are a markdown
// concept, so a rule naming one cannot decide a Go file: silently scanning every
// line of it would run a broader rule than the policy declared.
func TestFenceLangAppliesToMarkdownOnly(t *testing.T) {
	f, _ := Lookup("pattern_absent")
	plain := Spec{Check: "pattern_absent", With: WithYAML("pattern: 'x'")}
	fenced := Spec{Check: "pattern_absent", With: WithYAML("pattern: 'x'\nfence_lang: makefile")}

	sa, ok := f.(SpecAware)
	if !ok {
		t.Fatal("pattern_absent does not implement SpecAware")
	}
	for _, ty := range []FileType{TypeGo, TypeYAML, TypeMarkdown, TypeUnknown} {
		if !sa.AppliesToSpec(plain, ty) {
			t.Errorf("AppliesToSpec(plain, %q) = false, want true", ty)
		}
	}
	if !sa.AppliesToSpec(fenced, TypeMarkdown) {
		t.Error("AppliesToSpec(fence_lang, markdown) = false, want true")
	}
	for _, ty := range []FileType{TypeGo, TypeYAML, TypeUnknown} {
		if sa.AppliesToSpec(fenced, ty) {
			t.Errorf("AppliesToSpec(fence_lang, %q) = true, want false: fences are markdown's", ty)
		}
	}
}

// TestFenceLangColumnPointsIntoTheRealLine pins that masking outside the block
// does not shift the reported column, the same guarantee skip_code already gives.
func TestFenceLangColumnPointsIntoTheRealLine(t *testing.T) {
	doc := "```makefile\nbuild:\n\techo    spaced\n```\n"
	const with = "pattern: 'spaced'\nfence_lang: makefile"
	v := runFenced(t, with, doc)
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1: %+v", len(v), v)
	}
	if want := len([]rune("\techo    ")) + 1; v[0].Column != want {
		t.Errorf("Column = %d, want %d: the column points into the original line", v[0].Column, want)
	}
	if v[0].Match != "spaced" {
		t.Errorf("Match = %q, want the text from the real line", v[0].Match)
	}
}

// TestFenceLangUnclosedFenceStaysConservative covers the fence that never
// closes. The classifier treats the rest of the file as that block's code, which
// can only suppress a finding or report inside a block the author opened, never
// invent one in prose the author wrote outside any block.
func TestFenceLangUnclosedFenceStaysConservative(t *testing.T) {
	doc := "prose before\n```makefile\n    indented\nstill inside the unclosed block\n"
	const with = "pattern: '^\\S'\nfence_lang: makefile"
	v := runFenced(t, with, doc)
	if len(v) != 1 {
		t.Fatalf("got %d violations, want 1: %+v", len(v), v)
	}
	if v[0].Line != 4 {
		t.Errorf("Line = %d, want 4: prose before the fence is not in the block", v[0].Line)
	}
}
