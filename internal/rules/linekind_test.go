package rules

import "testing"

// classifyAll runs a whole document through one Classifier, as the engine does.
func classifyAll(lines []string) []LineClass {
	var c Classifier
	out := make([]LineClass, len(lines))
	for i, l := range lines {
		out[i] = c.Classify(l)
	}
	return out
}

func TestClassifierFencedBlock(t *testing.T) {
	got := classifyAll([]string{
		"prose before",
		"```go",
		"[T any](slice []T)",
		"```",
		"prose after",
	})
	want := []bool{false, true, true, true, false}
	for i, w := range want {
		if got[i].Fenced != w {
			t.Errorf("line %d Fenced = %v, want %v", i+1, got[i].Fenced, w)
		}
	}
	if got[1].Info != "go" {
		t.Errorf("Info = %q, want go", got[1].Info)
	}
}

// TestClassifierUnclosedFence pins the conservative direction: an unclosed
// fence classifies the remainder as code, which can suppress a finding but
// never invent one. This matches the shell checkers being replaced.
func TestClassifierUnclosedFence(t *testing.T) {
	got := classifyAll([]string{"prose", "```", "still code", "to the end"})
	for i := 1; i < 4; i++ {
		if !got[i].Fenced {
			t.Errorf("line %d Fenced = false, want true after an unclosed fence", i+1)
		}
	}
}

// TestClassifierTildeFence covers the other fence spelling, and pins that a
// backtick run does not close a tilde fence.
func TestClassifierTildeFence(t *testing.T) {
	got := classifyAll([]string{"~~~", "```", "still inside", "~~~", "prose"})
	if !got[1].Fenced || !got[2].Fenced {
		t.Error("a backtick run closed a tilde fence")
	}
	if got[4].Fenced {
		t.Error("the tilde fence did not close on its own marker")
	}
}

// TestClassifierLongerFenceNotClosedByShorter pins that a ``` inside a ````
// block does not end it, which is how a document shows fenced syntax.
func TestClassifierLongerFenceNotClosedByShorter(t *testing.T) {
	got := classifyAll([]string{"````md", "```", "inner", "````", "prose"})
	if !got[2].Fenced {
		t.Error("a shorter run closed a longer fence")
	}
	if got[4].Fenced {
		t.Error("the fence did not close on an equal-length run")
	}
}

func TestClassifierIndentedFence(t *testing.T) {
	got := classifyAll([]string{"  ```", "code", "  ```", "prose"})
	if !got[1].Fenced {
		t.Error("an indented fence was not recognized")
	}
	if got[3].Fenced {
		t.Error("an indented fence did not close")
	}
}

func TestCodeSpans(t *testing.T) {
	tests := []struct {
		name string
		text string
		off  int
		code bool
	}{
		{"inside a span", "a `code` b", 4, true},
		{"the marker itself", "a `code` b", 2, true},
		{"before a span", "a `code` b", 0, false},
		{"after a span", "a `code` b", 9, false},
		{"double-backtick span holds a backtick", "x ``a `b` c`` y", 8, true},
		{"unclosed run is not a span", "a ` b c", 4, false},
		{"second span on one line", "`a` and `b`", 9, true},
		{"between two spans", "`a` and `b`", 5, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := LineClass{spans: codeSpans(tc.text)}
			if got := c.InCode(tc.off); got != tc.code {
				t.Errorf("InCode(%d) on %q = %v, want %v", tc.off, tc.text, got, tc.code)
			}
		})
	}
}

// TestMaskedPreservesOffsets is the property columns depend on: masking blanks
// code without moving any byte that follows it.
func TestMaskedPreservesOffsets(t *testing.T) {
	text := "see `[a](b)` and [real](target.md)"
	c := LineClass{spans: codeSpans(text)}
	got := c.Masked(text)
	if len(got) != len(text) {
		t.Fatalf("Masked changed length: %d, want %d", len(got), len(text))
	}
	if idx := indexOf(got, "[real](target.md)"); idx != indexOf(text, "[real](target.md)") {
		t.Errorf("the prose link moved to %d, want %d", idx, indexOf(text, "[real](target.md)"))
	}
	if indexOf(got, "[a](b)") != -1 {
		t.Errorf("the code span survived masking: %q", got)
	}
}

func TestMaskedFencedLineIsAllCode(t *testing.T) {
	c := LineClass{Fenced: true}
	text := "[T any](slice []T)"
	got := c.Masked(text)
	if len(got) != len(text) {
		t.Fatalf("Masked changed length: %d, want %d", len(got), len(text))
	}
	for i := range got {
		if got[i] != ' ' {
			t.Fatalf("Masked left %q at %d, want all spaces on a fenced line", got[i], i)
		}
	}
}

// TestZeroLineClassIsProse pins the default: a rule that ignores classification
// sees exactly what it saw before this existed.
func TestZeroLineClassIsProse(t *testing.T) {
	var c LineClass
	text := "anything at all"
	if c.Masked(text) != text {
		t.Error("the zero LineClass masked prose")
	}
	if c.InCode(0) {
		t.Error("the zero LineClass reported code")
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
