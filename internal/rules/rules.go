// Package rules defines the rule contract and the registry of available checks.
//
// A rule is a stateful visitor instantiated per (file, rule) pair and never
// shared between files, so an implementation may keep unsynchronized state for
// the file it is visiting. The engine knows no rule by name: adding a check is
// one Rule implementation plus one Register call.
package rules

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// FileType is a file's kind, derived from its extension alone. v1 has no lexer
// and no syntax tree, so this is the entirety of what a rule knows about the
// shape of the file it visits.
type FileType string

// File types recognized by extension. TypeUnknown is used for any extension not
// in the table, including files with no extension.
const (
	TypeUnknown  FileType = ""
	TypeGo       FileType = "go"
	TypeMarkdown FileType = "markdown"
	TypePerl     FileType = "perl"
	TypeYAML     FileType = "yaml"
)

// FileMeta describes the file a rule is about to visit. Path is slash-separated
// and relative to the walk root, or the value of --as when the content came
// from stdin.
type FileMeta struct {
	Path string
	Type FileType
}

// Violation is a single proof failure.
//
// Line and Column are 1-based; Column counts runes, not bytes, so a caller can
// position a cursor in a UTF-8 editor. They are meaningful only while
// FileScoped is false.
//
// FileScoped marks a failure that is about the file as a whole and has no
// position in it, such as a required pattern that appears on no line. Line and
// Column are then both zero and carry no meaning: a renderer must branch on
// FileScoped rather than print them, since line 0 does not exist and a caller
// parsing it as a location gets nonsense.
type Violation struct {
	RuleID     string
	Path       string
	Line       int
	Column     int
	FileScoped bool
	Message    string
	Match      string
}

// Rule is a stateful visitor over one file.
//
// The engine calls Init once, then OnLine for each line in order starting at 1,
// then Finish once. A rule that can verdict per line returns violations from
// OnLine; a rule whose verdict is only decidable once the file is exhausted
// returns them from Finish. Both may return nil. A Rule is never called
// concurrently and is never reused for a second file.
type Rule interface {
	Init(f FileMeta)
	OnLine(n int, text string) []Violation
	Finish() []Violation
}

// Configuration failures, all raised at policy load so an unusable rule refuses
// the run rather than rendering as a rule that held.
var (
	ErrMaxRequired     = errors.New("rules: check requires a positive max")
	ErrPatternRequired = errors.New("rules: check requires a pattern")
	ErrBadPattern      = errors.New("rules: invalid pattern")
)

// ClassAware is the optional half of the rule contract, implemented by a check
// whose verdict depends on whether a line is prose or code.
//
// The engine calls OnClass immediately before OnLine for the same line, so a
// rule reads the class from its own field. A pure rule implements nothing extra
// and is unaffected. The class is computed once per line by the engine rather
// than by each rule, so two rules cannot disagree about where a fence ends,
// which is what keeps output deterministic across rule sets.
//
// The class of a line is meaningful only for a file type with a fence concept;
// for every other type the engine passes the zero LineClass, which is prose.
type ClassAware interface {
	OnClass(c LineClass)
}

// Spec is the validated configuration of one policy rule. It carries only what
// every check shares; everything check-specific arrives through With. It is
// built once at policy load and shared across every file, so a rule
// implementation must treat it as read-only.
type Spec struct {
	ID    string
	Check string

	// Message replaces the generated violation text when set. A generated
	// message can say what matched but never why it is banned or what to write
	// instead, which is the half a reader acting on the violation needs.
	Message string

	// With is the rule's `with:` block, still undecoded. A check reads it
	// through DecodeWith rather than directly, which is what keeps an unknown
	// key inside the block a refusal rather than a silently dropped field.
	//
	// It is a zero Node when the policy wrote no `with:` block at all, and
	// DecodeWith treats that as an empty mapping so a check with no required
	// config needs no special case.
	With yaml.Node

	// Config is what the check's own Validate decoded out of With, kept so New
	// does not parse YAML again for every file. The engine builds a Rule per
	// (file, rule) pair, so decoding in New would put the policy parser inside
	// the walk: measurable as allocations per file, and work that cannot vary
	// by file since the policy is the same for all of them.
	//
	// It is whatever type the check returned; only that check reads it back.
	Config any
}

// ErrBadWith is returned by DecodeWith when the `with:` block does not fit the
// check's configuration: an unknown key, a value of the wrong type, or a
// mapping where the check expects none.
var ErrBadWith = errors.New("rules: invalid with block")

// DecodeWith decodes the rule's `with:` block into a check's own config struct,
// rejecting any key the struct does not declare.
//
// Strictness here is the whole point of the block. A check-specific field that
// decoded into nothing would let a policy declare a constraint the engine never
// applies and then report that the rule held, which is the one outcome this
// tool exists to prevent. Because each check owns its config type, a field
// belonging to another check is simply an unknown key, so the wrong-check case
// needs no separate guard.
//
// The caller passes a pointer to a struct whose fields carry yaml tags. A rule
// with no configuration at all can skip the call: an unexpected `with:` block
// is caught at load by RejectWith.
//
// It routes through a yaml.Decoder rather than calling Node.Decode directly,
// because only the Decoder honors KnownFields: Node.Decode silently drops a key
// the target does not declare, which would reinstate the exact failure this
// block removes.
func (s Spec) DecodeWith(target any) error {
	if s.With.IsZero() {
		return nil
	}
	raw, err := yaml.Marshal(&s.With)
	if err != nil {
		return fmt.Errorf("%w: %q: %v", ErrBadWith, s.Check, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(target); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %q: %v", ErrBadWith, s.Check, err)
	}
	return nil
}

// WithYAML builds the `with:` block of a Spec from YAML source, for callers
// constructing a Spec directly instead of loading a policy file. It panics on
// malformed input, which in a test or a fixture is a bug in the caller rather
// than a condition to handle.
func WithYAML(src string) yaml.Node {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		panic(fmt.Sprintf("rules: WithYAML: %v", err))
	}
	// Unmarshal yields a document node wrapping the mapping; the policy loader
	// hands a rule the mapping itself, so unwrap to match.
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		return *n.Content[0]
	}
	return n
}

// RejectWith refuses a `with:` block for a check that takes no configuration.
// Accepting and ignoring one would repeat, one level down, the failure the
// block exists to remove.
func (s Spec) RejectWith() error {
	if s.With.IsZero() {
		return nil
	}
	return fmt.Errorf("%w: %q takes no configuration", ErrBadWith, s.Check)
}

// Msg returns the policy's own wording when it set one, and the generated text
// otherwise. A rule builds its generated message either way, since the argument
// is already formatted by the time it gets here.
func (s Spec) Msg(generated string) string {
	if s.Message != "" {
		return s.Message
	}
	return generated
}

// Factory builds a Rule for one file and reports which file types the check can
// decide. A factory is registered under the check name used in the policy file.
type Factory interface {
	// New returns a Rule instance for a single file.
	New(s Spec) Rule

	// AppliesTo reports whether the check can decide a file of this type. A
	// false result drops the rule for that file only; the file is still
	// visited by every other applicable rule.
	AppliesTo(t FileType) bool

	// Validate reports whether the spec carries the fields this check needs.
	// It runs at policy load so an unusable rule refuses the run rather than
	// rendering as a rule that held.
	Validate(s Spec) error
}

// SpecAware is the optional half of the factory contract, implemented by a
// check whose applicability depends on how the policy configured it rather than
// on the check alone.
//
// The engine prefers AppliesToSpec over AppliesTo when a factory implements it.
// A factory implementing neither half differently is unaffected: AppliesTo stays
// the answer for every check whose file types are fixed.
type SpecAware interface {
	// AppliesToSpec reports whether this configuration of the check can decide a
	// file of this type. It must agree with AppliesTo for a spec carrying no
	// configuration, so a policy that configures nothing sees no change.
	AppliesToSpec(s Spec, t FileType) bool
}

// Preparer is the optional half of the factory contract, implemented by a check
// whose configuration is worth decoding once instead of per file.
//
// The loader calls Prepare after Validate and stores the result on Spec.Config,
// which New then reads back. A factory that does not implement it pays a decode
// per file, which is correct but wasteful; the engine builds a Rule for every
// (file, rule) pair.
type Preparer interface {
	// Prepare returns the value to cache on Spec.Config. It runs only on a spec
	// Validate already accepted, so it may assume the configuration is sound.
	Prepare(s Spec) (any, error)
}

var registry = map[string]Factory{}

// Register adds a check under name. It panics on a duplicate name, which can
// only be a programming error in an init function.
func Register(name string, f Factory) {
	if _, dup := registry[name]; dup {
		panic(fmt.Sprintf("rules: check %q registered twice", name))
	}
	registry[name] = f
}

// Lookup returns the factory registered for a check name.
func Lookup(name string) (Factory, bool) {
	f, ok := registry[name]
	return f, ok
}

// Names returns every registered check name, sorted, for error messages that
// tell a caller what it could have written instead.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// maxConfig is the `with:` block of a check configured by a single bound. Max
// is a pointer so an omitted field and an explicit "max: 0" stay distinct: the
// first is a missing requirement, the second a value the check can reject on
// its own terms.
type maxConfig struct {
	Max *int `yaml:"max"`
}

// requireMax decodes and validates a bound-only `with:` block, returning the
// limit. The three max-taking checks share it so a rule to the bound cannot be
// tightened on one and forgotten on the others.
//
// It returns the cached limit when Validate already decoded one, so New costs a
// type assertion rather than a YAML parse.
func requireMax(s Spec) (int, error) {
	if limit, ok := s.Config.(int); ok {
		return limit, nil
	}
	var cfg maxConfig
	if err := s.DecodeWith(&cfg); err != nil {
		return 0, err
	}
	if cfg.Max == nil {
		return 0, fmt.Errorf("%w: %q", ErrMaxRequired, s.Check)
	}
	if *cfg.Max <= 0 {
		return 0, fmt.Errorf("%w: %q got max %d", ErrMaxRequired, s.Check, *cfg.Max)
	}
	return *cfg.Max, nil
}

// patternConfig is the `with:` block of the two regex checks. Pattern is
// compiled at load and shared across every file, so a rule must treat the
// compiled value as read-only.
type patternConfig struct {
	Pattern string `yaml:"pattern"`
	// SkipCode confines the rule to prose, ignoring fenced blocks and inline
	// code spans. A rule about writing is wrong about code: a document that
	// bans a word has to name that word, and it names it in a code span.
	SkipCode bool `yaml:"skip_code"`
}

// compiledPattern is what requirePattern caches on the Spec: the regex compiled
// once at load, rather than per file.
type compiledPattern struct {
	re       *regexp.Regexp
	skipCode bool
}

// requirePattern decodes a regex `with:` block and compiles the pattern once.
// Compiling at load rather than per file keeps both the walk and the line loop
// free of work that cannot vary by file.
func requirePattern(s Spec) (*regexp.Regexp, bool, error) {
	if c, ok := s.Config.(compiledPattern); ok {
		return c.re, c.skipCode, nil
	}
	var cfg patternConfig
	if err := s.DecodeWith(&cfg); err != nil {
		return nil, false, err
	}
	if cfg.Pattern == "" {
		return nil, false, fmt.Errorf("%w: %q", ErrPatternRequired, s.Check)
	}
	re, err := regexp.Compile(cfg.Pattern)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %q: %v", ErrBadPattern, s.Check, err)
	}
	return re, cfg.SkipCode, nil
}
