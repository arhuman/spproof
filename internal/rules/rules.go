// Package rules defines the rule contract and the registry of available checks.
//
// A rule is a stateful visitor instantiated per (file, rule) pair and never
// shared between files, so an implementation may keep unsynchronized state for
// the file it is visiting. The engine knows no rule by name: adding a check is
// one Rule implementation plus one Register call.
package rules

import (
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
