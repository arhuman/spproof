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
//
// The wording says "path" rather than "link": a policy may extract targets with
// its own pattern, and a //go:embed argument or a bare filename in prose is not
// a link. Naming it one would describe the file back to the reader wrongly.
func (c Candidate) Violation(err error) Violation {
	msg := fmt.Sprintf("path %q does not resolve to an existing file (%s)", c.Raw, c.Target)
	if err != nil {
		msg = fmt.Sprintf("path %q could not be resolved (%s): %v", c.Raw, c.Target, err)
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

// resolvableConfig is this check's `with:` block. Pattern replaces the built-in
// markdown link grammar with the policy's own extractor.
type resolvableConfig struct {
	// Pattern extracts link targets. Its first capture group names the target
	// when it has one, and the whole match does when it does not, so a policy
	// can either bracket the path or match it exactly.
	Pattern string `yaml:"pattern"`
}

// extractor is what the rule reads targets with: either the built-in markdown
// link grammar or the policy's own pattern, resolved once at load.
type extractor struct {
	re *regexp.Regexp
}

// custom reports whether the policy supplied the pattern, which decides both
// applicability and whether markdown masking is meaningful.
func (e extractor) custom() bool { return e.re != nil }

type resolvableLocalPathFactory struct{}

func (resolvableLocalPathFactory) New(s Spec) Rule {
	ex, _ := resolvableCfg(s)
	return &resolvableLocalPath{spec: s, ex: ex}
}

// AppliesTo answers for a check with no configuration, which is markdown only:
// the built-in extraction knows markdown link syntax and nothing else, so any
// other type would be scanned for links that its grammar does not define.
//
// A policy supplying its own pattern widens this; see AppliesToSpec.
func (resolvableLocalPathFactory) AppliesTo(t FileType) bool { return t == TypeMarkdown }

// AppliesToSpec widens the check to every file type once the policy supplies a
// pattern. The markdown-only restriction exists to protect the built-in grammar
// from files it does not describe; a policy bringing its own extractor has made
// no claim about markdown, and a Go file naming a path is a legitimate subject.
func (f resolvableLocalPathFactory) AppliesToSpec(s Spec, t FileType) bool {
	ex, err := resolvableCfg(s)
	if err != nil || !ex.custom() {
		return f.AppliesTo(t)
	}
	return true
}

// Validate accepts a pattern and nothing else. A `with:` block naming no
// pattern configures nothing, and refusing it keeps a policy from believing it
// turned on something that does not exist.
func (resolvableLocalPathFactory) Validate(s Spec) error {
	if s.With.IsZero() {
		return nil
	}
	_, err := resolvableCfg(s)
	return err
}

// Prepare caches the compiled extractor so New does not re-compile per file.
func (resolvableLocalPathFactory) Prepare(s Spec) (any, error) { return resolvableCfg(s) }

func resolvableCfg(s Spec) (extractor, error) {
	if ex, ok := s.Config.(extractor); ok {
		return ex, nil
	}
	if s.With.IsZero() {
		return extractor{}, nil
	}
	var cfg resolvableConfig
	if err := s.DecodeWith(&cfg); err != nil {
		return extractor{}, err
	}
	if cfg.Pattern == "" {
		return extractor{}, fmt.Errorf("%w: %q", ErrPatternRequired, s.Check)
	}
	re, err := regexp.Compile(cfg.Pattern)
	if err != nil {
		return extractor{}, fmt.Errorf("%w: %q: %v", ErrBadPattern, s.Check, err)
	}
	return extractor{re: re}, nil
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
	spec Spec
	ex   extractor
	path string
	dir  string
	// markdown records whether masking applies to this file, since fences and
	// code spans are a markdown concept and the engine classifies every type.
	markdown bool
	class    LineClass
	pending  []Candidate
}

func (r *resolvableLocalPath) Init(f FileMeta) {
	r.path = f.Path
	r.dir = path.Dir(f.Path)
	r.markdown = f.Type == TypeMarkdown
}

// OnClass records the current line's prose/code split for the OnLine that
// follows it.
func (r *resolvableLocalPath) OnClass(c LineClass) { r.class = c }

// OnLine records a candidate per checkable link target and returns no
// violations: it cannot decide one without the filesystem, and consulting it
// here would block the reader.
//
// In markdown, links are read from a masked copy in which code is blanked,
// unconditionally rather than behind an option. A link inside a fence is an
// example, not a reference: a Go generic (`[T any](slice []T)`) has the shape of
// a link and names no file, so following it reports a target the document never
// claimed. Masking preserves offsets, so the column still points into the real
// line.
//
// Masking is confined to markdown because fences and code spans are markdown's
// concepts. The engine classifies every file type, so a Go file containing
// backticks would otherwise have real content blanked out of it, and the rule
// would silently stop looking at the lines that matter.
func (r *resolvableLocalPath) OnLine(n int, text string) []Violation {
	subject := text
	if r.markdown {
		subject = r.class.Masked(text)
	}
	for _, l := range r.extract(subject) {
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

// extract returns the link targets on a line, through the policy's pattern when
// it supplied one and the built-in markdown grammar otherwise.
func (r *resolvableLocalPath) extract(text string) []link {
	if !r.ex.custom() {
		return extractLinks(text)
	}
	return extractPattern(r.ex.re, text)
}

// extractPattern reads targets with a caller-supplied regex.
//
// The first capture group names the target when the pattern has one, so a
// policy can bracket the path inside a larger match; the whole match names it
// otherwise. Taking the group rather than the match is also what keeps the
// reported column on the path itself rather than on whatever preceded it.
func extractPattern(re *regexp.Regexp, text string) []link {
	var out []link
	for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
		start, end := m[0], m[1]
		if len(m) >= 4 && m[2] >= 0 {
			start, end = m[2], m[3]
		}
		out = append(out, link{target: text[start:end], offset: start})
	}
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
