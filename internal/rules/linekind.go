package rules

import "strings"

// LineClass says which parts of one line are code rather than prose.
//
// It exists because a rule about prose is wrong about code: a Go generic
// written in a fenced block (`[T any](slice []T)`) has the shape of a markdown
// link, and a document that bans a character has to name that character in a
// code span. A rule that cannot tell the two apart reports on its own
// documentation, and a checker that cries wolf gets turned off.
//
// The zero value classifies the whole line as prose, so a rule that ignores
// classification behaves exactly as it did before.
type LineClass struct {
	// Fenced marks a line inside a fenced block, including the fence markers
	// themselves. Its content is code in full.
	Fenced bool
	// Marker distinguishes the opening and closing delimiter lines from the
	// block's content while Fenced is set. Both carry the block's Info, so a
	// rule selecting a block by language needs this to avoid scanning the
	// delimiters as if they were part of what they delimit.
	Marker bool
	// Info is the fence's info string ("go", "makefile", "") while Fenced is
	// set, taken from the opening marker.
	Info string
	// spans holds the byte ranges of inline code spans, in order, on a line
	// that is not itself fenced.
	spans [][2]int
}

// Masked returns text with every code region replaced by spaces, preserving
// length and byte offsets so a column computed against the result still points
// at the right rune in the original.
//
// Blanking rather than deleting is what keeps the offsets honest: a rule
// reports a column into the line the reader sees, not into a shortened copy.
func (c LineClass) Masked(text string) string {
	if c.Fenced {
		return strings.Repeat(" ", len(text))
	}
	if len(c.spans) == 0 {
		return text
	}
	b := []byte(text)
	for _, s := range c.spans {
		for i := s[0]; i < s[1] && i < len(b); i++ {
			b[i] = ' '
		}
	}
	return string(b)
}

// InFenceLang reports whether this line is content inside a fenced block whose
// language is lang, matched case-insensitively on the first word of the info
// string so "```Makefile title=x" still counts as makefile.
//
// The fence markers themselves are excluded: they carry the block's info string
// but delimit the content rather than being it, and a rule about what a block
// contains has nothing to say about the backticks around it.
func (c LineClass) InFenceLang(lang string) bool {
	if !c.Fenced || c.Marker {
		return false
	}
	return strings.EqualFold(fenceLanguage(c.Info), lang)
}

// KeptTo returns text with everything outside a fenced block of the given
// language blanked, which is the inverse of Masked: one keeps prose and drops
// code, the other drops everything but one block's code. Offsets survive either
// way, so a column computed against the result still points into the real line.
func (c LineClass) KeptTo(lang, text string) string {
	if c.InFenceLang(lang) {
		return text
	}
	return strings.Repeat(" ", len(text))
}

// fenceLanguage reduces an info string to the language it names, which is its
// first whitespace-separated word. Anything after that is the renderer's
// business (a title, a highlight range) and says nothing about the language.
func fenceLanguage(info string) string {
	if i := strings.IndexAny(info, " \t"); i >= 0 {
		return info[:i]
	}
	return info
}

// InCode reports whether the byte at off falls inside a code region.
func (c LineClass) InCode(off int) bool {
	if c.Fenced {
		return true
	}
	for _, s := range c.spans {
		if off >= s[0] && off < s[1] {
			return true
		}
	}
	return false
}

// Classifier tracks fence state across the lines of one file. A fence is a
// left-to-right toggle, so no lookahead is needed.
//
// An unclosed fence leaves the rest of the file classified as code. That is the
// conservative direction: it can only suppress a finding, never invent one, and
// it matches what the shell checkers this replaces already do.
type Classifier struct {
	inFence bool
	// marker is the run of backticks or tildes that opened the current fence.
	// A fence closes only on a marker of the same kind and at least the same
	// length, so a ``` inside a ~~~~ block does not end it.
	marker string
	info   string
}

// Classify advances the fence state by one line and returns that line's class.
// Lines must be fed in order: the state machine is the file's fence nesting.
func (c *Classifier) Classify(text string) LineClass {
	trimmed := strings.TrimLeft(text, " \t")
	if mark, info, ok := fenceMarker(trimmed); ok {
		if !c.inFence {
			c.inFence, c.marker, c.info = true, mark, info
			return LineClass{Fenced: true, Marker: true, Info: info}
		}
		if mark[0] == c.marker[0] && len(mark) >= len(c.marker) && info == "" {
			was := c.info
			c.inFence, c.marker, c.info = false, "", ""
			return LineClass{Fenced: true, Marker: true, Info: was}
		}
	}
	if c.inFence {
		return LineClass{Fenced: true, Info: c.info}
	}
	return LineClass{spans: codeSpans(text)}
}

// fenceMarker reports the run of fence characters opening or closing a fence,
// with whatever info string follows it. The line must already be left-trimmed.
func fenceMarker(s string) (mark, info string, ok bool) {
	if len(s) < 3 || (s[0] != '`' && s[0] != '~') {
		return "", "", false
	}
	ch := s[0]
	n := 0
	for n < len(s) && s[n] == ch {
		n++
	}
	if n < 3 {
		return "", "", false
	}
	info = strings.TrimSpace(s[n:])
	// An info string on a backtick fence may not itself contain a backtick,
	// which is how `` `a` `` on its own line stays an inline span.
	if ch == '`' && strings.Contains(info, "`") {
		return "", "", false
	}
	return s[:n], info, true
}

// codeSpans returns the byte ranges of inline code spans in a line, marker
// backticks included.
//
// A span opens on a run of backticks and closes on the next run of exactly the
// same length, which is what lets ``a `b` c`` hold a backtick. An unclosed run
// is not a span: the rest of the line stays prose.
func codeSpans(text string) [][2]int {
	var out [][2]int
	for i := 0; i < len(text); {
		if text[i] != '`' {
			i++
			continue
		}
		open, after := backtickRun(text, i)
		end, ok := closingRun(text, after, after-open)
		if !ok {
			// No closing run of the same length: the rest of the line is prose.
			break
		}
		out = append(out, [2]int{open, end})
		i = end
	}
	return out
}

// backtickRun returns the start of the run at i and the index just past it.
func backtickRun(text string, i int) (start, after int) {
	start = i
	for i < len(text) && text[i] == '`' {
		i++
	}
	return start, i
}

// closingRun finds the end of the next backtick run of exactly n characters at
// or after from, which is what closes a span opened by a run of the same width.
func closingRun(text string, from, n int) (end int, ok bool) {
	for j := from; j < len(text); {
		if text[j] != '`' {
			j++
			continue
		}
		start, after := backtickRun(text, j)
		if after-start == n {
			return after, true
		}
		j = after
	}
	return 0, false
}
