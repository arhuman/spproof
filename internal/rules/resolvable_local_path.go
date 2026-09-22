package rules

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

func init() {
	Register("resolvable_local_path", resolvableLocalPathFactory{})
}

// Candidate is one link target awaiting an existence answer, plus everything
// needed to render the violation if the answer is not "it is there".
//
// It is the unit a contextual rule hands to the engine instead of a verdict:
// the rule knows where the link is and what it points at, the engine owns the
// filesystem and the concurrency.
type Candidate struct {
	RuleID string
	// Path is the file containing the link, as violations report it.
	Path string
	// Target is the resolved path to test, relative to the process working
	// directory and already stripped of any fragment or query suffix.
	Target string
	// Raw is the link target exactly as written, quoted back to the reader so
	// the message names what is in the file rather than what we derived.
	Raw    string
	Line   int
	Column int
	// Message is the policy's own wording, empty when it set none. It is copied
	// here rather than read from the Spec because a candidate outlives the rule
	// that produced it: the resolver answers it after the file is closed.
	Message string
}

// Violation renders the failure for a candidate whose existence could not be
// confirmed. A nil err means the target was demonstrably absent; a non-nil err
// means existence could not be established at all, which fails identically and
// is distinguished only here, in the message.
func (c Candidate) Violation(err error) Violation {
	msg := fmt.Sprintf("link target %q does not resolve to an existing file (%s)", c.Raw, c.Target)
	if err != nil {
		msg = fmt.Sprintf("link target %q could not be resolved (%s): %v", c.Raw, c.Target, err)
	}
	if c.Message != "" {
		msg = c.Message
	}
	return Violation{
		RuleID:  c.RuleID,
		Path:    c.Path,
		Line:    c.Line,
		Column:  c.Column,
		Message: msg,
		Match:   c.Raw,
	}
}

// Contextual is the optional half of the rule contract, implemented only by a
// check whose verdict is not computable from the file's bytes.
//
// The engine type-asserts for it after each line and drains whatever the rule
// accumulated, so a pure rule implements nothing extra and is unaffected. A
// rule implementing it must still satisfy Rule; it simply reports through
// candidates rather than through OnLine's return value.
//
// TakeCandidates returns the candidates gathered since the previous call and
// clears them, so draining twice does not report twice.
type Contextual interface {
	TakeCandidates() []Candidate
}

type resolvableLocalPathFactory struct{}

func (resolvableLocalPathFactory) New(s Spec) Rule { return &resolvableLocalPath{spec: s} }

// AppliesTo accepts markdown only: the extraction knows markdown link syntax
// and nothing else, so any other type would be scanned for links that its
// grammar does not define.
func (resolvableLocalPathFactory) AppliesTo(t FileType) bool { return t == TypeMarkdown }

// Validate refuses a pattern and a maximum, which this check never reads: the
// link grammar is fixed and there is nothing to bound. It rejects skip_code
// because the check already ignores code unconditionally, so accepting the field
// would let a policy believe it turned something on that was never optional.
func (resolvableLocalPathFactory) Validate(s Spec) error {
	if err := rejectPattern(s); err != nil {
		return err
	}
	if err := rejectMax(s); err != nil {
		return err
	}
	return rejectSkipCode(s)
}

// resolvableLocalPath proves that every local relative link target in a
// markdown file resolves to something that exists.
//
// This check is CONTEXTUAL: unlike every other check, its verdict is not a
// function of the file's bytes. It consults the filesystem, so the same file
// can pass in one working tree and fail in another, and a verdict is not
// reproducible from the file alone. Targets resolve relative to the directory
// of the file containing the link, not to the walk root, and are allowed to
// escape that root: "../sibling/doc.md" is a legitimate link.
//
// The rule never stats. It accumulates candidates and the engine resolves them
// off the read loop, so no line of input waits on a syscall.
type resolvableLocalPath struct {
	spec    Spec
	path    string
	dir     string
	class   LineClass
	pending []Candidate
}

func (r *resolvableLocalPath) Init(f FileMeta) {
	r.path = f.Path
	r.dir = path.Dir(f.Path)
}

// OnClass records the current line's prose/code split for the OnLine that
// follows it.
func (r *resolvableLocalPath) OnClass(c LineClass) { r.class = c }

// OnLine records a candidate per checkable link target and returns no
// violations: it cannot decide one without the filesystem, and consulting it
// here would block the reader.
//
// Links are read from a masked copy in which code is blanked, unconditionally
// rather than behind an option. A link inside a fence is an example, not a
// reference: a Go generic (`[T any](slice []T)`) has the shape of a link and
// names no file, so following it reports a target the document never claimed.
// Masking preserves offsets, so the column still points into the real line.
func (r *resolvableLocalPath) OnLine(n int, text string) []Violation {
	for _, l := range extractLinks(r.class.Masked(text)) {
		target, ok := resolvableTarget(l.target)
		if !ok {
			continue
		}
		r.pending = append(r.pending, Candidate{
			RuleID:  r.spec.ID,
			Path:    r.path,
			Target:  path.Join(r.dir, target),
			Raw:     l.target,
			Line:    n,
			Column:  utf8.RuneCountInString(text[:l.offset]) + 1,
			Message: r.spec.Message,
		})
	}
	return nil
}

func (r *resolvableLocalPath) Finish() []Violation { return nil }

// TakeCandidates hands over what OnLine gathered and clears the buffer.
func (r *resolvableLocalPath) TakeCandidates() []Candidate {
	out := r.pending
	r.pending = nil
	return out
}

type link struct {
	target string
	// offset is the byte index of the target within the line.
	offset int
}

// inlineLink matches a markdown inline link, [text](target). The target stops
// at whitespace so a link carrying a title, [t](path "Title"), yields the path
// alone.
var inlineLink = regexp.MustCompile(`\[[^\]]*\]\(\s*([^)\s]+)`)

// refDefinition matches a reference definition, [label]: target, which is only
// a definition when it starts the line.
var refDefinition = regexp.MustCompile(`^\s*\[[^\]]+\]:\s*(\S+)`)

// extractLinks returns every markdown link target on a line with its byte
// offset. It is a regex over one line, not a parser: a link split across lines
// is not seen. Fenced and code-span content is already blanked by the caller,
// so nothing here needs to know about code.
func extractLinks(text string) []link {
	var out []link
	if m := refDefinition.FindStringSubmatchIndex(text); m != nil {
		out = append(out, link{target: text[m[2]:m[3]], offset: m[2]})
	}
	for _, m := range inlineLink.FindAllStringSubmatchIndex(text, -1) {
		out = append(out, link{target: text[m[2]:m[3]], offset: m[2]})
	}
	return out
}

// resolvableTarget reduces a raw link target to the relative path to test, and
// reports false for a target this check makes no claim about.
//
// Skipped: anything with a URL scheme, a protocol-relative "//host" target, an
// absolute filesystem path, and a pure fragment. None of them names a file in
// the working tree, so a verdict on them would be invented rather than proved.
// A fragment or query suffix is stripped before resolving, since "doc.md#name"
// asserts that doc.md exists, not that a file with a "#" in its name does.
func resolvableTarget(raw string) (string, bool) {
	if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "/") {
		return "", false
	}
	if hasScheme(raw) {
		return "", false
	}
	target := raw
	if i := strings.IndexAny(target, "#?"); i >= 0 {
		target = target[:i]
	}
	if target == "" {
		return "", false
	}
	return target, true
}

// hasScheme reports whether raw starts with a URL scheme per RFC 3986: a
// letter followed by letters, digits, "+", "-" or "." up to a colon. A colon
// reached any other way, such as one inside a path segment, is not a scheme.
func hasScheme(raw string) bool {
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == ':':
			return i > 0
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			continue
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
			continue
		default:
			return false
		}
	}
	return false
}
