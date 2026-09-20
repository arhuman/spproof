package engine

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/arhuman/spproof/internal/policy"
	"github.com/arhuman/spproof/internal/rules"
)

func load(t *testing.T, yaml string) *policy.Policy {
	t.Helper()
	p, err := policy.Decode(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("policy.Decode: %v", err)
	}
	return p
}

func sources(t *testing.T, files fstest.MapFS, roots ...string) []Source {
	t.Helper()
	if len(roots) == 0 {
		roots = []string{"."}
	}
	s, err := Collect(files, roots)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return s
}

const todoPolicy = `version: 1
rules:
  - id: no-todo
    check: pattern_absent
    files: ["**/*.md", "**/*.go"]
    pattern: "TODO"
`

func TestRunClean(t *testing.T) {
	files := fstest.MapFS{
		"a.md":     {Data: []byte("all good\nstill good\n")},
		"b.go":     {Data: []byte("package main\n")},
		"skip.txt": {Data: []byte("TODO but not selected\n")},
	}
	r, err := Run(load(t, todoPolicy), sources(t, files))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !r.OK() {
		t.Fatalf("want clean, got %d violations: %+v", len(r.Violations), r.Violations)
	}
	if len(r.Coverage) != 1 || r.Coverage[0].EvaluatedFiles != 2 {
		t.Errorf("coverage = %+v, want no-todo evaluated on 2 files", r.Coverage)
	}
}

func TestRunLineNumbersAcrossMultiLineFile(t *testing.T) {
	files := fstest.MapFS{
		"a.md": {Data: []byte("one\ntwo TODO\nthree\nfour TODO\n")},
	}
	r, err := Run(load(t, todoPolicy), sources(t, files))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.Violations) != 2 {
		t.Fatalf("got %d violations, want 2", len(r.Violations))
	}
	if r.Violations[0].Line != 2 || r.Violations[1].Line != 4 {
		t.Errorf("lines = %d,%d want 2,4", r.Violations[0].Line, r.Violations[1].Line)
	}
}

func TestRunMultipleMatchesPerLine(t *testing.T) {
	files := fstest.MapFS{"a.md": {Data: []byte("TODO TODO TODO\n")}}
	r, err := Run(load(t, todoPolicy), sources(t, files))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.Violations) != 3 {
		t.Fatalf("got %d violations, want 3", len(r.Violations))
	}
}

// TestApplicabilityDropsRuleNotFile proves the per (file, rule) mechanism: a
// rule that cannot decide a file's type falls away for that file while the
// other rules still visit it. The file is never skipped as a whole.
func TestApplicabilityDropsRuleNotFile(t *testing.T) {
	goOnly := stubFactory{applies: map[rules.FileType]bool{rules.TypeGo: true}}
	rules.Register("test_go_only", goOnly)

	p := load(t, `version: 1
rules:
  - id: everywhere
    check: pattern_absent
    files: ["**/*"]
    pattern: "MARK"
  - id: go-only
    check: test_go_only
    files: ["**/*"]
    pattern: "MARK"
`)
	files := fstest.MapFS{
		"a.md": {Data: []byte("MARK\n")},
		"b.go": {Data: []byte("MARK\n")},
	}
	r, err := Run(p, sources(t, files))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	byRule := map[string][]string{}
	for _, v := range r.Violations {
		byRule[v.RuleID] = append(byRule[v.RuleID], v.Path)
	}
	if got := byRule["everywhere"]; len(got) != 2 {
		t.Errorf("everywhere ran on %v, want both files", got)
	}
	if got := byRule["go-only"]; len(got) != 1 || got[0] != "b.go" {
		t.Errorf("go-only ran on %v, want only b.go", got)
	}

	cov := map[string]int{}
	for _, c := range r.Coverage {
		cov[c.RuleID] = c.EvaluatedFiles
	}
	if cov["everywhere"] != 2 {
		t.Errorf("everywhere evaluated on %d files, want 2", cov["everywhere"])
	}
	if cov["go-only"] != 1 {
		t.Errorf("go-only evaluated on %d files, want 1", cov["go-only"])
	}
}

// TestCoverageDistinguishesHeldFromNeverRan is the reason coverage exists: a
// rule selected by no file reports zero, which must not read like a rule that
// ran and found nothing.
func TestCoverageDistinguishesHeldFromNeverRan(t *testing.T) {
	p := load(t, `version: 1
rules:
  - id: ran
    check: pattern_absent
    files: ["**/*.md"]
    pattern: "TODO"
  - id: never-ran
    check: pattern_absent
    files: ["**/*.rs"]
    pattern: "TODO"
`)
	files := fstest.MapFS{"a.md": {Data: []byte("clean\n")}}
	r, err := Run(p, sources(t, files))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !r.OK() {
		t.Fatal("want clean run")
	}
	cov := map[string]int{}
	for _, c := range r.Coverage {
		cov[c.RuleID] = c.EvaluatedFiles
	}
	if cov["ran"] != 1 {
		t.Errorf("ran = %d, want 1", cov["ran"])
	}
	if cov["never-ran"] != 0 {
		t.Errorf("never-ran = %d, want 0", cov["never-ran"])
	}
}

// TestSingleReadPerFile proves a file is opened once no matter how many rules
// apply to it, which is what keeps the policy size off the I/O path.
func TestSingleReadPerFile(t *testing.T) {
	p := load(t, `version: 1
rules:
  - id: r1
    check: pattern_absent
    files: ["**/*.md"]
    pattern: "A"
  - id: r2
    check: pattern_absent
    files: ["**/*.md"]
    pattern: "B"
  - id: r3
    check: pattern_absent
    files: ["**/*.md"]
    pattern: "C"
`)
	opens := 0
	src := Source{
		Path: "a.md",
		Open: func() (readCloser, error) {
			opens++
			return nopCloser{strings.NewReader("A B C\n")}, nil
		},
	}
	r, err := Run(p, []Source{{Path: src.Path, Open: src.Open}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if opens != 1 {
		t.Errorf("file opened %d times, want 1", opens)
	}
	if len(r.Violations) != 3 {
		t.Errorf("got %d violations, want 3 (one per rule)", len(r.Violations))
	}
}

func TestDeterministicOrdering(t *testing.T) {
	p := load(t, `version: 1
rules:
  - id: z-rule
    check: pattern_absent
    files: ["**/*.md"]
    pattern: "X"
  - id: a-rule
    check: pattern_absent
    files: ["**/*.md"]
    pattern: "X"
`)
	files := fstest.MapFS{
		"z.md":       {Data: []byte("X\nX\n")},
		"a.md":       {Data: []byte("X X\n")},
		"dir/m.md":   {Data: []byte("X\n")},
		"dir/a/n.md": {Data: []byte("X\n")},
	}

	first, err := Run(p, sources(t, files))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := Run(p, sources(t, files))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(again.Violations) != len(first.Violations) {
			t.Fatalf("violation count changed between runs")
		}
		for j := range first.Violations {
			if first.Violations[j] != again.Violations[j] {
				t.Fatalf("run %d differs at %d:\n got %+v\nwant %+v", i, j, again.Violations[j], first.Violations[j])
			}
		}
	}

	want := []struct {
		path   string
		line   int
		ruleID string
	}{
		{"a.md", 1, "a-rule"},
		{"a.md", 1, "a-rule"},
		{"a.md", 1, "z-rule"},
		{"a.md", 1, "z-rule"},
		{"dir/a/n.md", 1, "a-rule"},
		{"dir/a/n.md", 1, "z-rule"},
		{"dir/m.md", 1, "a-rule"},
		{"dir/m.md", 1, "z-rule"},
		{"z.md", 1, "a-rule"},
		{"z.md", 1, "z-rule"},
		{"z.md", 2, "a-rule"},
		{"z.md", 2, "z-rule"},
	}
	if len(first.Violations) != len(want) {
		t.Fatalf("got %d violations, want %d", len(first.Violations), len(want))
	}
	for i, w := range want {
		g := first.Violations[i]
		if g.Path != w.path || g.Line != w.line || g.RuleID != w.ruleID {
			t.Errorf("violation %d = %s:%d[%s], want %s:%d[%s]", i, g.Path, g.Line, g.RuleID, w.path, w.line, w.ruleID)
		}
	}
}

// TestFileScopedSortsBeforeLineScoped pins where a violation with no line lands
// among positioned ones in the same file: first, because it is about the file
// as a whole rather than about anything inside it.
func TestFileScopedSortsBeforeLineScoped(t *testing.T) {
	p := load(t, `version: 1
rules:
  - id: z-no-todo
    check: pattern_absent
    files: ["**/*.md"]
    pattern: "TODO"
  - id: a-license
    check: pattern_present
    files: ["**/*.md"]
    pattern: "SPDX-License-Identifier"
`)
	files := fstest.MapFS{"a.md": {Data: []byte("first\nTODO here\n")}}

	r, err := Run(p, sources(t, files))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.Violations) != 2 {
		t.Fatalf("got %d violations, want 2", len(r.Violations))
	}
	if !r.Violations[0].FileScoped || r.Violations[0].RuleID != "a-license" {
		t.Errorf("violations[0] = %+v, want the file-scoped a-license failure", r.Violations[0])
	}
	if r.Violations[1].FileScoped || r.Violations[1].Line != 2 {
		t.Errorf("violations[1] = %+v, want the line-scoped failure on line 2", r.Violations[1])
	}

	for i := 0; i < 5; i++ {
		again, err := Run(p, sources(t, files))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		for j := range r.Violations {
			if r.Violations[j] != again.Violations[j] {
				t.Fatalf("run %d differs at %d", i, j)
			}
		}
	}
}

// TestFileScopedSortsFirstDespiteLaterRuleID proves the ordering comes from the
// scope and not from the rule id winning the tie by luck.
func TestFileScopedSortsFirstDespiteLaterRuleID(t *testing.T) {
	p := load(t, `version: 1
rules:
  - id: a-no-todo
    check: pattern_absent
    files: ["**/*.md"]
    pattern: "TODO"
  - id: z-license
    check: pattern_present
    files: ["**/*.md"]
    pattern: "SPDX-License-Identifier"
`)
	files := fstest.MapFS{"a.md": {Data: []byte("TODO on line one\n")}}

	r, err := Run(p, sources(t, files))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.Violations) != 2 {
		t.Fatalf("got %d violations, want 2", len(r.Violations))
	}
	if r.Violations[0].RuleID != "z-license" || !r.Violations[0].FileScoped {
		t.Errorf("violations[0] = %+v, want the file-scoped z-license failure first", r.Violations[0])
	}
}

// TestSortPutsFileScopedFirstOnScopeAlone pins the ordering on the scope rather
// than on Line happening to be zero. The input gives the file-scoped violation
// the later line and the later rule id, so every other clause of the comparator
// would rank it last.
func TestSortPutsFileScopedFirstOnScopeAlone(t *testing.T) {
	v := []rules.Violation{
		{RuleID: "a-rule", Path: "a.md", Line: 1, Column: 1},
		{RuleID: "z-rule", Path: "a.md", Line: 99, Column: 9, FileScoped: true},
	}
	sortViolations(v)
	if !v[0].FileScoped {
		t.Errorf("violations[0] = %+v, want the file-scoped one first", v[0])
	}
	if v[1].RuleID != "a-rule" {
		t.Errorf("violations[1] = %+v, want a-rule", v[1])
	}
}

func TestCollectWalksDirectoriesRecursively(t *testing.T) {
	files := fstest.MapFS{
		"a.md":                  {Data: []byte("x")},
		"sub/b.md":              {Data: []byte("x")},
		"sub/deep/c.md":         {Data: []byte("x")},
		".git/objects/pack.idx": {Data: []byte("x")},
	}
	got := sources(t, files)
	var paths []string
	for _, s := range got {
		paths = append(paths, s.Path)
	}
	want := []string{"a.md", "sub/b.md", "sub/deep/c.md"}
	if len(paths) != len(want) {
		t.Fatalf("got %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

func TestCollectSingleFile(t *testing.T) {
	files := fstest.MapFS{"a.md": {Data: []byte("x")}, "b.md": {Data: []byte("x")}}
	got := sources(t, files, "a.md")
	if len(got) != 1 || got[0].Path != "a.md" {
		t.Fatalf("got %+v, want only a.md", got)
	}
}

func TestCollectMissingPath(t *testing.T) {
	if _, err := Collect(fstest.MapFS{}, []string{"nope.md"}); err == nil {
		t.Fatal("want error for unreadable path, got nil")
	}
}

// realTree builds a temp directory on the real filesystem and returns an fs.FS
// rooted at it. fstest.MapFS cannot represent a symlink or a FIFO, so the
// non-regular-file rules can only be exercised against a real tree.
func realTree(t *testing.T) (string, fs.FS) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink and FIFO semantics differ on Windows")
	}
	dir := t.TempDir()
	return dir, os.DirFS(dir)
}

// TestCollectSkipsSymlinkWhileWalking pins that a walk cannot leave the tree it
// was given. Following the link would read an outside file and report it under
// an inside path, which is both a disclosure and a false location.
func TestCollectSkipsSymlinkWhileWalking(t *testing.T) {
	dir, fsys := realTree(t)
	if err := os.WriteFile(filepath.Join(dir, "real.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.md")); err != nil {
		t.Fatal(err)
	}

	got, err := Collect(fsys, []string{"."})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	// The sibling regular file must still be collected: a skipped entry drops
	// itself, not the walk.
	if len(got) != 1 || got[0].Path != "real.md" {
		var paths []string
		for _, s := range got {
			paths = append(paths, s.Path)
		}
		t.Fatalf("got %v, want only real.md", paths)
	}
}

// TestCollectSkipsFIFOWhileWalking pins that a walk never opens a FIFO. Reading
// one with no writer blocks forever, and a hook that never returns is worse
// than one that fails.
func TestCollectSkipsFIFOWhileWalking(t *testing.T) {
	dir, fsys := realTree(t)
	if err := os.WriteFile(filepath.Join(dir, "real.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mkfifo(t, filepath.Join(dir, "pipe.md"))

	got, err := Collect(fsys, []string{"."})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(got) != 1 || got[0].Path != "real.md" {
		var paths []string
		for _, s := range got {
			paths = append(paths, s.Path)
		}
		t.Fatalf("got %v, want only real.md", paths)
	}
}

// TestCollectRefusesNamedNonRegularFile is the other half of the rule: a path
// named directly is a request to check it, so refusing is the only answer that
// is not a clean-looking verdict over content never read.
func TestCollectRefusesNamedNonRegularFile(t *testing.T) {
	dir, fsys := realTree(t)
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.md")); err != nil {
		t.Fatal(err)
	}
	mkfifo(t, filepath.Join(dir, "pipe.md"))

	for _, name := range []string{"link.md", "pipe.md"} {
		t.Run(name, func(t *testing.T) {
			if _, err := Collect(fsys, []string{name}); !errors.Is(err, ErrNotRegularFile) {
				t.Errorf("got %v, want ErrNotRegularFile", err)
			}
		})
	}
}

// TestCollectSymlinkedDirectoryNotWalked guards the recursion case: a link to a
// directory is reported by WalkDir as a non-directory, so it must drop like any
// other link rather than pulling an outside tree in.
func TestCollectSymlinkedDirectoryNotWalked(t *testing.T) {
	dir, fsys := realTree(t)
	if err := os.WriteFile(filepath.Join(dir, "real.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(dir, "linkdir")); err != nil {
		t.Fatal(err)
	}

	got, err := Collect(fsys, []string{"."})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, s := range got {
		if strings.Contains(s.Path, "secret") {
			t.Errorf("walked through a symlinked directory: got %q", s.Path)
		}
	}
	if len(got) != 1 {
		t.Errorf("got %d sources, want only real.md", len(got))
	}
}

func TestLineTooLong(t *testing.T) {
	files := fstest.MapFS{"a.md": {Data: append([]byte(strings.Repeat("x", MaxLineLength+10)), '\n')}}
	_, err := Run(load(t, todoPolicy), sources(t, files))
	if !errors.Is(err, ErrLineTooLong) {
		t.Errorf("got %v, want ErrLineTooLong", err)
	}
}

// TestFenceAwareLinkRuleIgnoresCode is the engine-level proof of the capability
// this rule was blocked on: a Go generic inside a fence has the shape of a
// markdown link and names no file, so following it reports a target the
// document never claimed. The real broken link on the next line still reports.
func TestFenceAwareLinkRuleIgnoresCode(t *testing.T) {
	files := fstest.MapFS{
		"doc.md": {Data: []byte("```go\n[T any](slice []T)\n```\n[gone](./missing.md)\n")},
	}
	p := load(t, "version: 1\nrules:\n  - id: links\n    check: resolvable_local_path\n    files: [\"**/*.md\"]\n")
	r, err := RunWith(p, sources(t, files), stubExistence{})
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	if len(r.Violations) != 1 {
		for _, v := range r.Violations {
			t.Logf("got %s:%d %s", v.Path, v.Line, v.Message)
		}
		t.Fatalf("got %d violations, want 1: only the real link is missing", len(r.Violations))
	}
	if r.Violations[0].Line != 4 {
		t.Errorf("Line = %d, want 4 (the prose link, not the fenced generic)", r.Violations[0].Line)
	}
}

// stubExistence reports every path as absent, so any candidate the rule emits
// becomes a violation and the test measures what was emitted.
type stubExistence struct{}

func (stubExistence) Exists(string) (bool, error) { return false, nil }
