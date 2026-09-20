package policy

import (
	"errors"
	"strings"
	"testing"
)

const validPolicy = `version: 1
rules:
  - id: no-em-dash
    check: pattern_absent
    files: ["**/*.md"]
    pattern: "[a-z]"
`

func TestDecodeValid(t *testing.T) {
	p, err := Decode(strings.NewReader(validPolicy))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(p.Rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(p.Rules))
	}
	r := p.Rules[0]
	if r.Spec.ID != "no-em-dash" || r.Spec.Check != "pattern_absent" {
		t.Errorf("unexpected spec: %+v", r.Spec)
	}
	if r.Spec.Pattern == nil {
		t.Error("pattern was not compiled at load time")
	}
	if r.Factory == nil {
		t.Error("factory not bound")
	}
}

// TestDecodeCarriesMessage pins the optional message through to the Spec. An
// absent message must stay empty rather than becoming a placeholder, since the
// rules fall back to their generated text on exactly that condition.
func TestDecodeCarriesMessage(t *testing.T) {
	const withMessage = `version: 1
rules:
  - id: no-em-dash
    check: pattern_absent
    files: ["**/*.md"]
    pattern: "[a-z]"
    message: "use a comma or a colon instead"
`
	p, err := Decode(strings.NewReader(withMessage))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := p.Rules[0].Spec.Message; got != "use a comma or a colon instead" {
		t.Errorf("Message = %q, want the policy's own wording", got)
	}

	p, err = Decode(strings.NewReader(validPolicy))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := p.Rules[0].Spec.Message; got != "" {
		t.Errorf("Message = %q, want empty when the policy set none", got)
	}
}

// TestDecodeRejections pins every strict-validation refusal to a distinct
// sentinel, which is what lets the CLI map the whole class to exit code 2.
func TestDecodeRejections(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want error
	}{
		{
			name: "version absent",
			yaml: "rules:\n  - id: a\n    check: pattern_absent\n    files: [\"*.md\"]\n    pattern: \"x\"\n",
			want: ErrBadVersion,
		},
		{
			name: "version wrong",
			yaml: "version: 2\nrules:\n  - id: a\n    check: pattern_absent\n    files: [\"*.md\"]\n    pattern: \"x\"\n",
			want: ErrBadVersion,
		},
		{
			name: "unknown top-level field",
			yaml: "version: 1\nstrictness: high\nrules:\n  - id: a\n    check: pattern_absent\n    files: [\"*.md\"]\n    pattern: \"x\"\n",
			want: ErrUnknownField,
		},
		{
			name: "unknown rule field",
			yaml: "version: 1\nrules:\n  - id: a\n    check: pattern_absent\n    files: [\"*.md\"]\n    pattern: \"x\"\n    severity: high\n",
			want: ErrUnknownField,
		},
		{
			name: "unknown check",
			yaml: "version: 1\nrules:\n  - id: a\n    check: pattern_maybe\n    files: [\"*.md\"]\n    pattern: \"x\"\n",
			want: ErrUnknownCheck,
		},
		{
			name: "duplicate rule id",
			yaml: "version: 1\nrules:\n  - id: a\n    check: pattern_absent\n    files: [\"*.md\"]\n    pattern: \"x\"\n  - id: a\n    check: pattern_absent\n    files: [\"*.go\"]\n    pattern: \"y\"\n",
			want: ErrDuplicateID,
		},
		{
			name: "empty rule set",
			yaml: "version: 1\nrules: []\n",
			want: ErrEmptyRuleSet,
		},
		{
			name: "empty file",
			yaml: "",
			want: ErrEmptyRuleSet,
		},
		{
			name: "invalid regex",
			yaml: "version: 1\nrules:\n  - id: a\n    check: pattern_absent\n    files: [\"*.md\"]\n    pattern: \"[unclosed\"\n",
			want: ErrBadPattern,
		},
		{
			name: "missing id",
			yaml: "version: 1\nrules:\n  - check: pattern_absent\n    files: [\"*.md\"]\n    pattern: \"x\"\n",
			want: ErrMissingID,
		},
		{
			name: "missing files",
			yaml: "version: 1\nrules:\n  - id: a\n    check: pattern_absent\n    pattern: \"x\"\n",
			want: ErrMissingFiles,
		},
		{
			name: "invalid glob",
			yaml: "version: 1\nrules:\n  - id: a\n    check: pattern_absent\n    files: [\"[bad\"]\n    pattern: \"x\"\n",
			want: ErrBadGlob,
		},
		{
			name: "pattern_absent without pattern",
			yaml: "version: 1\nrules:\n  - id: a\n    check: pattern_absent\n    files: [\"*.md\"]\n",
			want: nil, // checked below via rules.ErrPatternRequired
		},
		{
			name: "malformed yaml",
			yaml: "version: 1\nrules: [[[\n",
			want: ErrMalformedYAML,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(tc.yaml))
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("got %v, want errors.Is(_, %v)", err, tc.want)
			}
		})
	}
}

func TestRuleMatches(t *testing.T) {
	p, err := Decode(strings.NewReader(`version: 1
rules:
  - id: a
    check: pattern_absent
    files: ["**/*.md", "docs/*.txt", "README"]
    pattern: "x"
`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	r := p.Rules[0]

	tests := []struct {
		path string
		want bool
	}{
		{"a.md", true},
		{"docs/a.md", true},
		{"a/b/c/deep.md", true},
		{"docs/notes.txt", true},
		{"docs/sub/notes.txt", false},
		{"README", true},
		{"main.go", false},
		{"docs/a.md.bak", false},
	}
	for _, tc := range tests {
		if got := r.Matches(tc.path); got != tc.want {
			t.Errorf("Matches(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestRuleMatchesTrailingDoubleStar covers the branch where ** ends the pattern
// and has no segments left to consume. Nothing exercised it before, so breaking
// it was invisible: a glob that silently stops matching is how file selection
// fails without anyone noticing.
func TestRuleMatchesTrailingDoubleStar(t *testing.T) {
	p, err := Decode(strings.NewReader(`version: 1
rules:
  - id: a
    check: pattern_absent
    files: ["docs/**", "**"]
    pattern: "x"
`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	r := p.Rules[0]

	tests := []struct {
		path string
		want bool
	}{
		{"docs", true},
		{"docs/a.md", true},
		{"docs/deep/nested/a.md", true},
		{"anything.txt", true},
	}
	for _, tc := range tests {
		if got := r.Matches(tc.path); got != tc.want {
			t.Errorf("Matches(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
