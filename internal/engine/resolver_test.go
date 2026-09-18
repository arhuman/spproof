package engine

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/arhuman/spproof/internal/rules"
)

const linkPolicy = `version: 1
rules:
  - id: links
    check: resolvable_local_path
    files: ["**/*.md"]
`

var errDenied = errors.New("permission denied")

// mapExistence answers from a fixed set of paths and counts every ask, so a
// test can assert both the verdict and how many times the filesystem was
// consulted.
type mapExistence struct {
	present map[string]bool
	denied  map[string]bool
	delay   time.Duration

	calls atomic.Int64
	mu    sync.Mutex
	per   map[string]int
}

func newMapExistence(present ...string) *mapExistence {
	m := &mapExistence{
		present: make(map[string]bool, len(present)),
		denied:  map[string]bool{},
		per:     map[string]int{},
	}
	for _, p := range present {
		m.present[p] = true
	}
	return m
}

func (m *mapExistence) Exists(path string) (bool, error) {
	m.calls.Add(1)
	m.mu.Lock()
	m.per[path]++
	m.mu.Unlock()
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if m.denied[path] {
		return false, errDenied
	}
	return m.present[path], nil
}

func (m *mapExistence) countFor(path string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.per[path]
}

func runLinks(t *testing.T, files fstest.MapFS, ex rules.Existence) Result {
	t.Helper()
	r, err := RunWith(load(t, linkPolicy), sources(t, files), ex)
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	return r
}

func TestResolvableLocalPathResolvingLinkHolds(t *testing.T) {
	files := fstest.MapFS{"doc.md": {Data: []byte("see [x](target.md)\n")}}
	r := runLinks(t, files, newMapExistence("target.md"))
	if !r.OK() {
		t.Fatalf("want clean, got %+v", r.Violations)
	}
	if r.Coverage[0].EvaluatedFiles != 1 {
		t.Errorf("evaluatedFiles = %d, want 1", r.Coverage[0].EvaluatedFiles)
	}
}

func TestResolvableLocalPathMissingTargetFails(t *testing.T) {
	files := fstest.MapFS{"doc.md": {Data: []byte("intro\nsee [x](missing.md) here\n")}}
	r := runLinks(t, files, newMapExistence())

	if len(r.Violations) != 1 {
		t.Fatalf("got %d violations, want 1: %+v", len(r.Violations), r.Violations)
	}
	v := r.Violations[0]
	if v.FileScoped {
		t.Error("FileScoped = true, want false: the link has a position")
	}
	if v.Path != "doc.md" || v.Line != 2 {
		t.Errorf("location = %s:%d, want doc.md:2", v.Path, v.Line)
	}
	if want := len("see [x](") + 1; v.Column != want {
		t.Errorf("Column = %d, want %d", v.Column, want)
	}
	if v.RuleID != "links" {
		t.Errorf("RuleID = %q, want links", v.RuleID)
	}
	if !strings.Contains(v.Message, "missing.md") {
		t.Errorf("Message = %q, want it to name the target", v.Message)
	}
}

// TestResolvableLocalPathAccessErrorAlsoFails pins the settled verdict: an
// inability to confirm existence is a failure, exactly like a miss. The
// distinction survives only in the message.
func TestResolvableLocalPathAccessErrorAlsoFails(t *testing.T) {
	ex := newMapExistence()
	ex.denied["secret.md"] = true
	files := fstest.MapFS{"doc.md": {Data: []byte("see [x](secret.md)\n")}}

	r := runLinks(t, files, ex)
	if len(r.Violations) != 1 {
		t.Fatalf("got %d violations, want 1: an unconfirmable path fails", len(r.Violations))
	}
	got := r.Violations[0].Message
	if !strings.Contains(got, errDenied.Error()) {
		t.Errorf("Message = %q, want it to carry the access failure", got)
	}

	miss := runLinks(t, fstest.MapFS{"doc.md": {Data: []byte("see [x](gone.md)\n")}}, newMapExistence())
	if miss.Violations[0].Message == got {
		t.Error("a miss and an access error render the same message")
	}
}

func TestResolvableLocalPathSkipsNonLocalTargetsEndToEnd(t *testing.T) {
	ex := newMapExistence()
	files := fstest.MapFS{"doc.md": {Data: []byte(strings.Join([]string{
		"[a](https://example.com/x.md)",
		"[b](mailto:someone@example.com)",
		"[c](//example.com/x.md)",
		"[d](/etc/passwd)",
		"[e](#section)",
	}, "\n") + "\n")}}

	r := runLinks(t, files, ex)
	if !r.OK() {
		t.Fatalf("want clean, got %+v", r.Violations)
	}
	if n := ex.calls.Load(); n != 0 {
		t.Errorf("the filesystem was consulted %d times for targets that name no local file", n)
	}
}

// TestResolvableLocalPathResolvesRelativeToLinkingFileEndToEnd includes a "../"
// that escapes the walk root, which is legitimate and must reach the real path.
func TestResolvableLocalPathResolvesRelativeToLinkingFileEndToEnd(t *testing.T) {
	ex := newMapExistence("internal/docs/x.md", "../outside/y.md")
	files := fstest.MapFS{
		"internal/notes/a.md": {Data: []byte("[x](../docs/x.md)\n")},
		"b.md":                {Data: []byte("[y](../outside/y.md)\n")},
	}
	r := runLinks(t, files, ex)
	if !r.OK() {
		t.Fatalf("want clean, got %+v", r.Violations)
	}
	if ex.countFor("internal/docs/x.md") != 1 {
		t.Errorf("resolution base is wrong: asked for %v", ex.per)
	}
}

func TestResolvableLocalPathBothSyntaxesEndToEnd(t *testing.T) {
	files := fstest.MapFS{"doc.md": {Data: []byte("an [inline](one.md) link\n\n[label]: two.md\n")}}
	r := runLinks(t, files, newMapExistence())
	if len(r.Violations) != 2 {
		t.Fatalf("got %d violations, want 2 (inline and reference definition): %+v", len(r.Violations), r.Violations)
	}
	if r.Violations[0].Line != 1 || r.Violations[1].Line != 3 {
		t.Errorf("lines = %d,%d want 1,3", r.Violations[0].Line, r.Violations[1].Line)
	}
}

// TestResolvableLocalPathTwoBrokenLinksOnOneLine is the tiebreaker case: both
// violations share path, line and rule id, so only the column separates them.
// It runs repeatedly because their arrival order from the workers is not fixed.
func TestResolvableLocalPathTwoBrokenLinksOnOneLine(t *testing.T) {
	files := fstest.MapFS{"doc.md": {Data: []byte("[a](one.md) and [b](two.md)\n")}}
	for i := 0; i < 20; i++ {
		r := runLinks(t, files, newMapExistence())
		if len(r.Violations) != 2 {
			t.Fatalf("got %d violations, want 2", len(r.Violations))
		}
		if r.Violations[0].Column >= r.Violations[1].Column {
			t.Fatalf("run %d ordered columns %d then %d", i, r.Violations[0].Column, r.Violations[1].Column)
		}
		if !strings.Contains(r.Violations[0].Message, "one.md") {
			t.Fatalf("run %d put %q first, want the earlier column", i, r.Violations[0].Message)
		}
	}
}

// TestResolvableLocalPathStatsSharedTargetOnce proves the cache is per run and
// shared across documents: twenty-six files naming the same path collapse to at
// most one ask per worker, not one per document.
//
// The bound is resolveWorkers rather than exactly 1 because the cache does not
// hold its lock across the underlying call: workers racing on a path no one has
// answered yet all reach it, and only later asks read the memo. That costs a
// duplicate syscall, never a wrong answer. Asserting exactly 1 would be
// asserting a single-flight guarantee this cache deliberately does not make.
func TestResolvableLocalPathStatsSharedTargetOnce(t *testing.T) {
	files := fstest.MapFS{}
	for i := 'a'; i <= 'z'; i++ {
		files[string(i)+".md"] = &fstest.MapFile{Data: []byte("see [x](shared.md)\n")}
	}
	ex := newMapExistence("shared.md")

	r := runLinks(t, files, ex)
	if !r.OK() {
		t.Fatalf("want clean, got %+v", r.Violations)
	}
	got := ex.countFor("shared.md")
	if got < 1 {
		t.Errorf("shared.md was never resolved")
	}
	if got > resolveWorkers {
		t.Errorf("shared.md was resolved %d times across %d documents, want at most %d: the cache is not collapsing repeated targets", got, len(files), resolveWorkers)
	}
}

// TestResolvableLocalPathWaitsForOutstandingStats is the property that cannot
// be observed when it is broken: a run returning before its resolutions land
// reports clean for no better reason than not having waited. The backing store
// answers slowly, so a missing join loses the violation rather than reordering
// it.
func TestResolvableLocalPathWaitsForOutstandingStats(t *testing.T) {
	ex := newMapExistence()
	ex.delay = 20 * time.Millisecond
	files := fstest.MapFS{"doc.md": {Data: []byte("see [x](missing.md)\n")}}

	for i := 0; i < 5; i++ {
		r := runLinks(t, files, ex)
		if len(r.Violations) != 1 {
			t.Fatalf("run %d returned %d violations, want 1: the run reported a verdict while a resolution was outstanding", i, len(r.Violations))
		}
	}
}

// TestResolvableLocalPathDeterministicAcrossRuns pins R6 for violations that
// arrive from goroutines in no fixed order.
func TestResolvableLocalPathDeterministicAcrossRuns(t *testing.T) {
	files := fstest.MapFS{
		"z.md":       {Data: []byte("[a](gone-a.md) [b](gone-b.md)\n[c](gone-c.md)\n")},
		"a.md":       {Data: []byte("[d](gone-d.md)\n\n[label]: gone-e.md\n")},
		"dir/m.md":   {Data: []byte("[f](../gone-f.md)\n")},
		"dir/a/n.md": {Data: []byte("[g](gone-g.md) [h](gone-h.md)\n")},
	}

	first := runLinks(t, files, newMapExistence())
	if len(first.Violations) != 8 {
		t.Fatalf("got %d violations, want 8: %+v", len(first.Violations), first.Violations)
	}
	for i := 0; i < 20; i++ {
		again := runLinks(t, files, newMapExistence())
		if len(again.Violations) != len(first.Violations) {
			t.Fatalf("run %d produced %d violations, want %d", i, len(again.Violations), len(first.Violations))
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
		column int
	}{
		{"a.md", 1, 5},
		{"a.md", 3, 10},
		{"dir/a/n.md", 1, 5},
		{"dir/a/n.md", 1, 20},
		{"dir/m.md", 1, 5},
		{"z.md", 1, 5},
		{"z.md", 1, 20},
		{"z.md", 2, 5},
	}
	for i, w := range want {
		g := first.Violations[i]
		if g.Path != w.path || g.Line != w.line || g.Column != w.column {
			t.Errorf("violation %d = %s:%d:%d, want %s:%d:%d", i, g.Path, g.Line, g.Column, w.path, w.line, w.column)
		}
	}
}

// TestResolvableLocalPathMixesWithPureRules proves the optional interface left
// the pure rules alone: both kinds report from the same run, correctly ordered.
func TestResolvableLocalPathMixesWithPureRules(t *testing.T) {
	p := load(t, `version: 1
rules:
  - id: links
    check: resolvable_local_path
    files: ["**/*.md"]
  - id: no-todo
    check: pattern_absent
    files: ["**/*.md", "**/*.go"]
    pattern: "TODO"
`)
	files := fstest.MapFS{
		"doc.md": {Data: []byte("TODO and [x](missing.md)\n")},
		"a.go":   {Data: []byte("// TODO\n")},
	}

	r, err := RunWith(p, sources(t, files), newMapExistence())
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	if len(r.Violations) != 3 {
		t.Fatalf("got %d violations, want 3: %+v", len(r.Violations), r.Violations)
	}
	if r.Violations[0].Path != "a.go" || r.Violations[0].RuleID != "no-todo" {
		t.Errorf("violations[0] = %+v, want the go file's TODO", r.Violations[0])
	}
	if r.Violations[1].RuleID != "links" || r.Violations[2].RuleID != "no-todo" {
		t.Errorf("doc.md violations ordered %q then %q, want links then no-todo", r.Violations[1].RuleID, r.Violations[2].RuleID)
	}
	cov := map[string]int{}
	for _, c := range r.Coverage {
		cov[c.RuleID] = c.EvaluatedFiles
	}
	if cov["links"] != 1 {
		t.Errorf("links evaluated on %d files, want 1 (markdown only)", cov["links"])
	}
	if cov["no-todo"] != 2 {
		t.Errorf("no-todo evaluated on %d files, want 2", cov["no-todo"])
	}
}

// TestResolvableLocalPathReadsFileOnce keeps P1's invariant under the new
// dispatch: draining candidates per line must not reopen anything.
func TestResolvableLocalPathReadsFileOnce(t *testing.T) {
	opens := 0
	src := Source{
		Path: "doc.md",
		Open: func() (readCloser, error) {
			opens++
			return nopCloser{strings.NewReader("[a](one.md)\n[b](two.md)\n")}, nil
		},
	}
	r, err := RunWith(load(t, linkPolicy), []Source{src}, newMapExistence())
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	if opens != 1 {
		t.Errorf("file opened %d times, want 1", opens)
	}
	if len(r.Violations) != 2 {
		t.Errorf("got %d violations, want 2", len(r.Violations))
	}
}

// TestResolvableLocalPathManyLinksStayBounded drives more links than the worker
// pool and queue so the bounded dispatch is exercised rather than assumed.
func TestResolvableLocalPathManyLinksStayBounded(t *testing.T) {
	var b strings.Builder
	const links = 5000
	for i := 0; i < links; i++ {
		b.WriteString("[x](missing.md)\n")
	}
	files := fstest.MapFS{"doc.md": {Data: []byte(b.String())}}

	ex := newMapExistence()
	r := runLinks(t, files, ex)
	if len(r.Violations) != links {
		t.Fatalf("got %d violations, want %d", len(r.Violations), links)
	}
	if got := ex.countFor("missing.md"); got > 8 {
		t.Errorf("missing.md resolved %d times, want at most one per worker", got)
	}
}
