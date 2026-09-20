// Package policy loads and validates the declarative policy file.
//
// Loading is strict by design: a proof engine must never render "I do not
// understand this rule" as "this rule holds". Every validation failure is a
// distinct sentinel error so the caller can map the whole class to exit code 2.
package policy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/arhuman/spproof/internal/rules"
	"gopkg.in/yaml.v3"
)

// Version is the only policy schema version this build accepts.
const Version = 1

// Validation failures. Every one of these means the engine could not run, which
// the CLI reports as exit code 2.
var (
	ErrBadVersion    = errors.New("policy: version must be 1")
	ErrUnknownField  = errors.New("policy: unknown field")
	ErrUnknownCheck  = errors.New("policy: unknown check")
	ErrDuplicateID   = errors.New("policy: duplicate rule id")
	ErrEmptyRuleSet  = errors.New("policy: rule set is empty")
	ErrBadPattern    = errors.New("policy: invalid pattern")
	ErrMissingID     = errors.New("policy: rule id is required")
	ErrMissingFiles  = errors.New("policy: rule files is required")
	ErrBadGlob       = errors.New("policy: invalid file glob")
	ErrMalformedYAML = errors.New("policy: malformed YAML")
	ErrBadBaseline   = errors.New("policy: baseline must not be negative")
)

// Policy is a loaded, validated and compiled rule set. Its regexes are compiled
// once here and shared across every file of a run.
type Policy struct {
	Rules []Rule
}

// Rule is one validated policy entry bound to its registered factory.
type Rule struct {
	Spec    rules.Spec
	Files   []string
	Factory rules.Factory
	Ratchet Ratchet
}

// Ratchet tolerates a declared number of existing violations so a rule can be
// adopted on a tree that does not yet satisfy it. Only an increase fails, which
// is what lets a policy gate new work without a cleanup landing first.
//
// A count is the whole verdict: the engine knows how many violations exist, not
// which of them are new. When the count is over the limit every violation is
// reported, not just the excess, so the author chooses which to remove rather
// than being handed an arbitrary subset.
//
// Both limits are declared in the policy, never discovered from a state file.
// The engine reads no ambient state and writes nothing, so a verdict stays a
// function of the tree and the policy alone.
type Ratchet struct {
	// Run is the tolerated total across every file, when set.
	Run *int
	// PerFile is the tolerated count within any single file, when set. It is
	// applied per file, so it bounds the worst file rather than the total.
	PerFile *int
}

// Zero reports whether no ratchet was declared, in which case every violation
// stands.
func (r Ratchet) Zero() bool { return r.Run == nil && r.PerFile == nil }

// Matches reports whether the rule's file globs select this path. It answers
// only the path question; whether the check can decide the file's type is a
// separate question the engine asks the factory.
func (r Rule) Matches(p string) bool {
	for _, g := range r.Files {
		if matchGlob(g, p) {
			return true
		}
	}
	return false
}

type file struct {
	Version int        `yaml:"version"`
	Rules   []ruleNode `yaml:"rules"`
}

type ruleNode struct {
	ID       string   `yaml:"id"`
	Check    string   `yaml:"check"`
	Files    []string `yaml:"files"`
	Pattern  string   `yaml:"pattern"`
	Max      int      `yaml:"max"`
	Message  string   `yaml:"message"`
	SkipCode bool     `yaml:"skip_code"`
	Baseline *int     `yaml:"baseline"`
	PerFile  *int     `yaml:"baseline_per_file"`
}

// Load reads and validates a policy file from disk.
//
// The caller names the path, deliberately: --policy is explicit and required so
// a verdict depends on nothing ambient. Opening a caller-supplied path is the
// contract, not a traversal risk, and the file is parsed as data rather than
// executed.
func Load(path string) (*Policy, error) {
	f, err := os.Open(path) // #nosec G304 -- the policy path is the caller's explicit argument

	if err != nil {
		return nil, fmt.Errorf("policy: open: %w", err)
	}
	defer f.Close()
	return Decode(f)
}

// Decode reads and validates a policy from r. Unknown fields are rejected, so a
// typo in a policy file is a refusal rather than a silently ignored rule.
func Decode(r io.Reader) (*Policy, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)

	var raw file
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: file is empty", ErrEmptyRuleSet)
		}
		return nil, classifyYAMLError(err)
	}

	if raw.Version != Version {
		return nil, fmt.Errorf("%w: got %d", ErrBadVersion, raw.Version)
	}
	if len(raw.Rules) == 0 {
		return nil, ErrEmptyRuleSet
	}

	p := &Policy{Rules: make([]Rule, 0, len(raw.Rules))}
	seen := make(map[string]struct{}, len(raw.Rules))
	for i, n := range raw.Rules {
		r, err := compile(n, i)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[r.Spec.ID]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateID, r.Spec.ID)
		}
		seen[r.Spec.ID] = struct{}{}
		p.Rules = append(p.Rules, r)
	}
	return p, nil
}

func compile(n ruleNode, idx int) (Rule, error) {
	if n.ID == "" {
		return Rule{}, fmt.Errorf("%w: rule %d", ErrMissingID, idx)
	}
	factory, ok := rules.Lookup(n.Check)
	if !ok {
		return Rule{}, fmt.Errorf("%w: %q in rule %q (known checks: %v)", ErrUnknownCheck, n.Check, n.ID, rules.Names())
	}
	if len(n.Files) == 0 {
		return Rule{}, fmt.Errorf("%w: rule %q", ErrMissingFiles, n.ID)
	}
	for _, g := range n.Files {
		if err := validateGlob(g); err != nil {
			return Rule{}, fmt.Errorf("%w: %q in rule %q: %v", ErrBadGlob, g, n.ID, err)
		}
	}

	spec := rules.Spec{ID: n.ID, Check: n.Check, Max: n.Max, Message: n.Message, SkipCode: n.SkipCode}
	if n.Pattern != "" {
		re, err := regexp.Compile(n.Pattern)
		if err != nil {
			return Rule{}, fmt.Errorf("%w: rule %q: %v", ErrBadPattern, n.ID, err)
		}
		spec.Pattern = re
	}
	if err := factory.Validate(spec); err != nil {
		return Rule{}, fmt.Errorf("policy: rule %q: %w", n.ID, err)
	}
	if err := validateBaselines(n); err != nil {
		return Rule{}, err
	}
	return Rule{
		Spec:    spec,
		Files:   n.Files,
		Factory: factory,
		Ratchet: Ratchet{Run: n.Baseline, PerFile: n.PerFile},
	}, nil
}

// classifyYAMLError maps the decoder's unknown-field failure onto a sentinel.
// yaml.v3 reports it as a plain *yaml.TypeError with a message, so the text is
// the only available discriminator.
func classifyYAMLError(err error) error {
	var te *yaml.TypeError
	if errors.As(err, &te) {
		for _, e := range te.Errors {
			if isUnknownFieldMessage(e) {
				return fmt.Errorf("%w: %s", ErrUnknownField, e)
			}
		}
	}
	return fmt.Errorf("%w: %v", ErrMalformedYAML, err)
}

// isUnknownFieldMessage matches the message yaml.v3 emits under
// KnownFields(true), which is the only signal it gives for an unknown field.
func isUnknownFieldMessage(msg string) bool {
	return strings.Contains(msg, "not found in type")
}

// validateBaselines refuses a negative tolerance. Clamping it to zero would run
// a stricter rule than the policy declared without saying so.
func validateBaselines(n ruleNode) error {
	for _, b := range []struct {
		field string
		val   *int
	}{{"baseline", n.Baseline}, {"baseline_per_file", n.PerFile}} {
		if b.val != nil && *b.val < 0 {
			return fmt.Errorf("%w: rule %q: %s is %d", ErrBadBaseline, n.ID, b.field, *b.val)
		}
	}
	return nil
}
