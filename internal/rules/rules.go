// Package rules defines the rule contract and the registry of available checks.
//
// A rule is a stateful visitor instantiated per (file, rule) pair and never
// shared between files, so an implementation may keep unsynchronized state for
// the file it is visiting. The engine knows no rule by name: adding a check is
// one Rule implementation plus one Register call.
package rules

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
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

// Refusals for a field set on a check that never reads it. Ignoring one
// silently would run a different rule than the policy asked for and report that
// it held, which is the one outcome this tool exists to prevent. Strict loading
// already rejects a field no check knows; these reject a known field on the
// wrong check, which is the same failure in a shape the decoder cannot see.
var (
	ErrSkipCodeUnsupported = errors.New("rules: check does not support skip_code")
	ErrPatternUnsupported  = errors.New("rules: check does not take a pattern")
	ErrMaxUnsupported      = errors.New("rules: check does not take a max")
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

// Spec is the validated, compiled configuration of one policy rule. Pattern is
// nil for checks that take no regex. It is compiled once at policy load and
// shared across every file, so a rule implementation must treat it as read-only.
type Spec struct {
	ID      string
	Check   string
	Pattern *regexp.Regexp
	Max     int

	// Message replaces the generated violation text when set. A generated
	// message can say what matched but never why it is banned or what to write
	// instead, which is the half a reader acting on the violation needs.
	Message string

	// SkipCode confines the rule to prose, ignoring fenced blocks and inline
	// code spans. A rule about writing is wrong about code: a document that
	// bans a word has to name that word, and it names it in a code span.
	SkipCode bool
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

// rejectSkipCode refuses skip_code for a check that does not honor it.
//
// Silently ignoring the field would run a narrower rule than the policy
// declared and then report that it held, which is exactly the outcome strict
// loading exists to prevent.
func rejectSkipCode(s Spec) error {
	if s.SkipCode {
		return fmt.Errorf("%w: %q", ErrSkipCodeUnsupported, s.Check)
	}
	return nil
}

// rejectPattern refuses a pattern for a check that never reads one.
func rejectPattern(s Spec) error {
	if s.Pattern != nil {
		return fmt.Errorf("%w: %q", ErrPatternUnsupported, s.Check)
	}
	return nil
}

// requireMaxOnly validates a check whose whole configuration is a positive max:
// the bound is mandatory, and a pattern or skip_code alongside it is a field the
// check will never read. The three max-taking checks share it so a new refusal
// cannot be added to one and forgotten on the others.
func requireMaxOnly(s Spec) error {
	if s.Max <= 0 {
		return fmt.Errorf("%w: %q got max %d", ErrMaxRequired, s.Check, s.Max)
	}
	if err := rejectPattern(s); err != nil {
		return err
	}
	return rejectSkipCode(s)
}

// rejectMax refuses a max for a check that never reads one.
//
// It can only refuse a positive value: Max is a plain int, so a policy that
// omits the field and one that writes "max: 0" arrive here identically. Every
// check that does read Max requires it to be positive, so no policy can mean
// anything by a zero today, and the ambiguity stays invisible. A check wanting a
// meaningful zero has to make presence explicit first.
func rejectMax(s Spec) error {
	if s.Max != 0 {
		return fmt.Errorf("%w: %q", ErrMaxUnsupported, s.Check)
	}
	return nil
}
