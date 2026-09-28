package rules

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func init() {
	Register("yaml_frontmatter", yamlFrontmatterFactory{})
}

// ErrFrontmatterConfig is returned by Validate when a yaml_frontmatter rule
// asks nothing of the file. A rule with no required keys, no closed schema and
// no key constraints holds over every document, which reads as a check that
// passed rather than one that was never written.
var ErrFrontmatterConfig = errors.New("rules: yaml_frontmatter requires at least one of required_keys, forbid_extra_keys or key_constraints")

// maxFrontmatterLines bounds what the rule will buffer before giving up on the
// closing fence.
//
// The project's peak memory is bounded by the longest line, and this check is
// the only one that holds more than one line at a time. Without a cap, a file
// opening with "---" and never closing it would be buffered whole. The cap is
// generous next to real frontmatter, so reaching it means the fence is missing
// rather than the block being large.
const maxFrontmatterLines = 200

// keyConstraint restricts the values of one frontmatter key. It applies to a
// scalar and to every element of a sequence alike, so a policy does not have to
// know which shape the document used.
//
// Split exists because the same list is written two ways. A YAML sequence makes
// each item a value of its own; a comma-joined scalar is one value that happens
// to contain the list. Without it, a policy written against the sequence form
// holds vacuously over the scalar form, which is the failure this field closes:
// an unevaluated constraint rendering as one that held.
type keyConstraint struct {
	Forbidden []string `yaml:"forbidden"`
	// Split tokenizes every value this constraint looks at before comparing it,
	// on the given separator. Tokens are trimmed of surrounding whitespace and
	// empty ones dropped, so "Read, Edit" and "Read,Edit," read alike. An
	// unset Split leaves values whole, which is what a policy constraining a
	// free-form string expects.
	Split string `yaml:"split"`
}

// comparable returns the values a constraint compares against Forbidden: the
// node's own values, or their tokens when the constraint names a separator.
// Comparison stays equality either way, so a token is forbidden only when the
// whole token is.
func (c keyConstraint) comparable(node *yaml.Node) []string {
	values := scalarValues(node)
	if c.Split == "" {
		return values
	}
	var out []string
	for _, v := range values {
		for _, token := range strings.Split(v, c.Split) {
			if token = strings.TrimSpace(token); token != "" {
				out = append(out, token)
			}
		}
	}
	return out
}

type frontmatterConfig struct {
	// RequiredKeys must each be present. A key present with an empty value
	// counts as present: the policy asked for the key, not for a value.
	RequiredKeys []string `yaml:"required_keys"`
	// ForbidExtraKeys closes the schema to RequiredKeys plus the constrained
	// keys. It is off by default, since naming the keys a document must have is
	// not the same as saying those are the only ones allowed.
	ForbidExtraKeys bool `yaml:"forbid_extra_keys"`
	// KeyConstraints restricts the values of individual keys, which is what
	// makes a separate sequence-checking rule unnecessary.
	KeyConstraints map[string]keyConstraint `yaml:"key_constraints"`
	// RequirePresent makes a document with no frontmatter a violation. It
	// defaults to true, so the field exists only to opt out: a policy that
	// checks the shape of frontmatter wherever it appears, without mandating it.
	RequirePresent *bool `yaml:"require_present"`
}

// requires reports whether the config asks anything of a document at all.
func (c frontmatterConfig) requires() bool {
	return len(c.RequiredKeys) > 0 || c.ForbidExtraKeys || len(c.KeyConstraints) > 0
}

// requirePresent resolves the tri-state field to its default of true.
func (c frontmatterConfig) requirePresent() bool {
	return c.RequirePresent == nil || *c.RequirePresent
}

type yamlFrontmatterFactory struct{}

func (yamlFrontmatterFactory) New(s Spec) Rule {
	cfg, _ := frontmatterCfg(s)
	return &yamlFrontmatter{spec: s, cfg: cfg}
}

// AppliesTo accepts markdown only. A .yaml file is a whole document with no
// fence to find: asking it for frontmatter would either report every such file
// as missing a block it never had, or silently treat the document as its own
// frontmatter. "This YAML document has key X" is a different question and would
// be a different check.
func (yamlFrontmatterFactory) AppliesTo(t FileType) bool { return t == TypeMarkdown }

func (yamlFrontmatterFactory) Validate(s Spec) error {
	_, err := frontmatterCfg(s)
	return err
}

// Prepare caches the decoded config so New does not re-parse per file.
func (yamlFrontmatterFactory) Prepare(s Spec) (any, error) { return frontmatterCfg(s) }

func frontmatterCfg(s Spec) (frontmatterConfig, error) {
	if cfg, ok := s.Config.(frontmatterConfig); ok {
		return cfg, nil
	}
	var cfg frontmatterConfig
	if err := s.DecodeWith(&cfg); err != nil {
		return cfg, err
	}
	if !cfg.requires() {
		return cfg, fmt.Errorf("%w: %q", ErrFrontmatterConfig, s.Check)
	}
	return cfg, nil
}

// yamlFrontmatter proves that a markdown file's YAML frontmatter block exists
// and has the shape the policy declared.
//
// The block is located line by line rather than by splitting the file on "---":
// a horizontal rule in the body has the same three characters, and only position
// distinguishes them. The opener must be the very first line.
//
// The rule buffers the block, which every other check avoids, because YAML is
// not decidable one line at a time. maxFrontmatterLines bounds that buffer, so
// the file-scale memory guarantee survives a fence that never closes.
type yamlFrontmatter struct {
	spec Spec
	cfg  frontmatterConfig
	path string

	// state machine over the file's first lines
	seenAny bool
	inBlock bool
	closed  bool
	lines   []string
	// endLine is the line number of the closing fence, which is where a finding
	// about the block as a whole points.
	endLine int
	// overrun records that the block passed the cap, so Finish reports the
	// missing fence rather than parsing a truncated block.
	overrun bool
}

func (r *yamlFrontmatter) Init(f FileMeta) { r.path = f.Path }

// OnLine drives the fence state machine and never reports: the block's verdict
// is only decidable once it is closed, and a document with no frontmatter is
// only known to have none once the file ends.
func (r *yamlFrontmatter) OnLine(n int, text string) []Violation {
	if r.closed || r.overrun {
		return nil
	}
	if !r.seenAny {
		r.seenAny = true
		// Only the first line can open the block. Anything else means this
		// document has no frontmatter, which Finish decides.
		if isFrontmatterFence(text) {
			r.inBlock = true
		}
		return nil
	}
	if !r.inBlock {
		return nil
	}
	if isFrontmatterFence(text) {
		r.inBlock, r.closed, r.endLine = false, true, n
		return nil
	}
	if len(r.lines) >= maxFrontmatterLines {
		r.overrun = true
		r.lines = nil
		return nil
	}
	r.lines = append(r.lines, text)
	return nil
}

// Finish reports the block's verdict, which is only decidable here.
//
// The four outcomes are deliberately distinct: a missing block is file-scoped
// because it has no position, while an unclosed, oversized or malformed block
// all have one and say which line to look at.
func (r *yamlFrontmatter) Finish() []Violation {
	switch {
	case !r.seenAny || (!r.inBlock && !r.closed && !r.overrun):
		if !r.cfg.requirePresent() {
			return nil
		}
		return []Violation{{
			RuleID:     r.spec.ID,
			Path:       r.path,
			FileScoped: true,
			Message:    r.spec.Msg("file has no YAML frontmatter block"),
		}}
	case r.overrun:
		return []Violation{r.at(1, fmt.Sprintf(
			"YAML frontmatter is not closed within %d lines of the opening fence", maxFrontmatterLines))}
	case r.inBlock:
		return []Violation{r.at(1, "YAML frontmatter opened but never closed")}
	}
	return r.check()
}

// check parses the collected block and applies the policy to it.
func (r *yamlFrontmatter) check() []Violation {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(r.lines, "\n")), &doc); err != nil {
		// A broken block is a finding about this file, never an error that
		// aborts the run: the policy file's own parse failure is exit 2, and
		// this must stay exit 1.
		return []Violation{r.at(r.endLine, fmt.Sprintf("YAML frontmatter does not parse: %s", blockError(err)))}
	}

	keys, ok := mappingKeys(&doc)
	if !ok {
		return []Violation{r.at(r.endLine, "YAML frontmatter is not a mapping of keys to values")}
	}

	var out []Violation
	// Ranging over the declared slice rather than a map keeps the order of two
	// missing keys stable across runs, which the output ordering invariant needs.
	for _, want := range r.cfg.RequiredKeys {
		if _, found := keys[want]; !found {
			out = append(out, r.at(r.endLine, fmt.Sprintf("YAML frontmatter is missing the required key %q", want)))
		}
	}
	out = append(out, r.constraintViolations(keys)...)
	if r.cfg.ForbidExtraKeys {
		out = append(out, r.extraKeyViolations(keys)...)
	}
	return out
}

// constraintViolations applies every declared key constraint. Keys are visited
// through the document's own order rather than the map's, so two findings in one
// file keep a stable relative order.
func (r *yamlFrontmatter) constraintViolations(keys map[string]*yaml.Node) []Violation {
	var out []Violation
	for _, name := range sortedKeys(r.cfg.KeyConstraints) {
		node, found := keys[name]
		if !found {
			continue
		}
		constraint := r.cfg.KeyConstraints[name]
		comparable := constraint.comparable(node)
		for _, bad := range constraint.Forbidden {
			for _, got := range comparable {
				if got == bad {
					out = append(out, r.at(node.Line, fmt.Sprintf(
						"YAML frontmatter key %q carries the forbidden value %q", name, bad)))
				}
			}
		}
	}
	return out
}

// extraKeyViolations reports every key the policy did not account for. The
// allowed set is the required keys plus the constrained ones: naming a
// constraint for a key is how a policy says that key may appear.
func (r *yamlFrontmatter) extraKeyViolations(keys map[string]*yaml.Node) []Violation {
	allowed := make(map[string]struct{}, len(r.cfg.RequiredKeys)+len(r.cfg.KeyConstraints))
	for _, k := range r.cfg.RequiredKeys {
		allowed[k] = struct{}{}
	}
	for k := range r.cfg.KeyConstraints {
		allowed[k] = struct{}{}
	}
	var out []Violation
	for _, name := range sortedKeys(keys) {
		if _, ok := allowed[name]; !ok {
			out = append(out, r.at(keys[name].Line, fmt.Sprintf(
				"YAML frontmatter carries the extra key %q", name)))
		}
	}
	return out
}

// blockError reduces a YAML error to its text, dropping the line number the
// library prefixes.
//
// That number is deliberately discarded rather than mapped into the file. For
// this error class yaml.v3 reports the line zero-indexed and floored at 1, and
// it names where the broken construct opened rather than where parsing failed,
// so no fixed offset makes it correct for every input. The violation points at
// the block's closing fence instead: a location that is always right and merely
// coarse beats one that is precise on some documents and silently wrong on
// others.
func blockError(err error) string {
	msg := err.Error()
	var te *yaml.TypeError
	if errors.As(err, &te) && len(te.Errors) > 0 {
		msg = te.Errors[0]
	}
	if rest, ok := strings.CutPrefix(msg, "yaml: line "); ok {
		if _, tail, found := strings.Cut(rest, ": "); found {
			return tail
		}
	}
	return strings.TrimPrefix(msg, "yaml: ")
}

// at builds a line-scoped violation. The line is relative to the block, so it is
// offset by the opening fence the buffer does not contain.
func (r *yamlFrontmatter) at(line int, generated string) Violation {
	if line < 1 {
		line = 1
	}
	return Violation{
		RuleID:  r.spec.ID,
		Path:    r.path,
		Line:    line,
		Column:  1,
		Message: r.spec.Msg(generated),
	}
}

// isFrontmatterFence reports whether a line is a bare "---" delimiter. Trailing
// whitespace is tolerated; anything else on the line is not a fence.
func isFrontmatterFence(text string) bool {
	return strings.TrimRight(text, " \t\r") == "---"
}

// mappingKeys indexes a parsed block by key name, reporting false when the block
// is not a mapping. An empty document is an empty mapping rather than an error:
// "---\n---" declares no keys, which the required-key check then reports.
func mappingKeys(doc *yaml.Node) (map[string]*yaml.Node, bool) {
	out := map[string]*yaml.Node{}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return out, true
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, false
	}
	// A mapping's Content alternates key, value.
	for i := 0; i+1 < len(root.Content); i += 2 {
		out[root.Content[i].Value] = root.Content[i+1]
	}
	return out, true
}

// sortedKeys returns a map's keys in a stable order. Every violation slice this
// rule builds is walked through it rather than by ranging a map, since Go
// randomizes map order and the tool guarantees byte-identical output across runs.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// scalarValues returns the values a node carries: one for a scalar, each element
// for a sequence. A policy constrains what a key may hold without having to know
// which of the two shapes the document used.
func scalarValues(n *yaml.Node) []string {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.SequenceNode {
		out := make([]string, 0, len(n.Content))
		for _, el := range n.Content {
			if el.Kind == yaml.ScalarNode {
				out = append(out, el.Value)
			}
		}
		return out
	}
	if n.Kind == yaml.ScalarNode {
		return []string{n.Value}
	}
	return nil
}
