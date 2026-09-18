// Package engine walks a tree, resolves which rules apply to each file, and
// runs them in a single buffered pass per file.
//
// The engine never writes to the filesystem and never prints: it returns a
// Result that the caller renders.
package engine

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/arhuman/spproof/internal/policy"
	"github.com/arhuman/spproof/internal/rules"
)

// MaxLineLength bounds the reader's buffer. Peak memory of a run is a small
// multiple of this constant, never a function of file size. A line longer than
// this fails the run rather than being silently truncated, because a truncated
// line could hide the very match a rule is proving absent.
const MaxLineLength = 1 << 20 // 1 MiB

// ErrLineTooLong is returned when a file contains a line over MaxLineLength.
var ErrLineTooLong = errors.New("engine: line exceeds maximum length")

// Result is the outcome of a run: the violations found, and the per-rule record
// of where each rule actually ran.
type Result struct {
	Violations []rules.Violation
	Coverage   []RuleCoverage
}

// RuleCoverage records how often a rule was actually evaluated. A rule with
// zero EvaluatedFiles held vacuously: it never ran, which is a different fact
// from "it ran and found nothing" and must not read the same way.
type RuleCoverage struct {
	RuleID         string
	Check          string
	EvaluatedFiles int
}

// OK reports whether every applicable rule held.
func (r Result) OK() bool { return len(r.Violations) == 0 }

// Source is one unit of content to check: a path plus a way to open it. It lets
// stdin be checked under an assumed name without existing on disk.
type Source struct {
	// Path is the name the file type is keyed off and that violations report.
	Path string
	// Open returns the content. The engine closes the reader.
	Open func() (io.ReadCloser, error)

	// Explicit marks a file the caller named directly rather than one found by
	// walking a directory. Naming a file is itself the selection, so the
	// policy's file globs are not consulted for it: every rule applicable to
	// its type runs. Files discovered by a walk leave this false and are
	// selected by the globs as usual.
	Explicit bool
}

// Run applies the policy to every source and returns a deterministic result.
//
// Each file is read exactly once regardless of how many rules apply to it. A
// rule whose check cannot decide the file's type is dropped for that file only;
// the file is still visited by the remaining rules.
//
// Existence resolution for contextual rules runs against the real filesystem.
// Use RunWith to supply another rules.Existence.
func Run(p *policy.Policy, sources []Source) (Result, error) {
	return RunWith(p, sources, nil)
}

// RunWith is Run with an explicit rules.Existence backing the contextual
// checks. A nil exists means the real filesystem.
//
// Existence answers are cached for the whole run, so a path referenced by fifty
// documents is resolved once, and they are resolved off the read loop: a run
// never reports a verdict while a resolution is still outstanding, since a
// premature verdict would report clean for a reason no better than not having
// waited.
func RunWith(p *policy.Policy, sources []Source, exists rules.Existence) (Result, error) {
	evaluated := make(map[string]int, len(p.Rules))
	var violations []rules.Violation

	res := newResolver(exists)
	for _, src := range sources {
		v, err := checkFile(p, src, evaluated, res)
		if err != nil {
			res.wait()
			return Result{}, err
		}
		violations = append(violations, v...)
	}
	violations = append(violations, res.wait()...)

	sortViolations(violations)
	return Result{Violations: violations, Coverage: coverage(p, evaluated)}, nil
}

func checkFile(p *policy.Policy, src Source, evaluated map[string]int, res *resolver) ([]rules.Violation, error) {
	meta := rules.FileMeta{Path: src.Path, Type: rules.TypeOf(src.Path)}

	active := make([]rules.Rule, 0, len(p.Rules))
	for _, pr := range p.Rules {
		if !src.Explicit && !pr.Matches(src.Path) {
			continue
		}
		if !pr.Factory.AppliesTo(meta.Type) {
			continue
		}
		r := pr.Factory.New(pr.Spec)
		r.Init(meta)
		active = append(active, r)
		evaluated[pr.Spec.ID]++
	}
	if len(active) == 0 {
		return nil, nil
	}

	rc, err := src.Open()
	if err != nil {
		return nil, fmt.Errorf("engine: open %s: %w", src.Path, err)
	}
	defer rc.Close()

	contextual := make([]rules.Contextual, 0, len(active))
	for _, r := range active {
		if c, ok := r.(rules.Contextual); ok {
			contextual = append(contextual, c)
		}
	}

	var out []rules.Violation
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 64*1024), MaxLineLength)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		for _, r := range active {
			out = append(out, r.OnLine(n, line)...)
		}
		for _, c := range contextual {
			res.dispatch(c.TakeCandidates())
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, fmt.Errorf("%w: %s", ErrLineTooLong, src.Path)
		}
		return nil, fmt.Errorf("engine: read %s: %w", src.Path, err)
	}
	for _, r := range active {
		out = append(out, r.Finish()...)
	}
	for _, c := range contextual {
		res.dispatch(c.TakeCandidates())
	}
	return out, nil
}

func coverage(p *policy.Policy, evaluated map[string]int) []RuleCoverage {
	out := make([]RuleCoverage, 0, len(p.Rules))
	for _, pr := range p.Rules {
		out = append(out, RuleCoverage{
			RuleID:         pr.Spec.ID,
			Check:          pr.Spec.Check,
			EvaluatedFiles: evaluated[pr.Spec.ID],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RuleID < out[j].RuleID })
	return out
}

// sortViolations orders by path, then file-scoped before line-scoped, then
// line, then rule id, then column, so two runs over an unchanged tree emit
// byte-identical output. A file-scoped violation has no line and sorts first
// within its file: it is about the file as a whole, so it reads before any
// position inside it.
func sortViolations(v []rules.Violation) {
	sort.SliceStable(v, func(i, j int) bool {
		a, b := v[i], v[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.FileScoped != b.FileScoped {
			return a.FileScoped
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.RuleID != b.RuleID {
			return a.RuleID < b.RuleID
		}
		return a.Column < b.Column
	})
}

// Collect expands the given roots into sources, walking directories
// recursively. Paths are interpreted relative to fsys and returned
// slash-separated. A root of "." walks everything.
//
// A root that names a file is marked Explicit, so it is checked by every rule
// applicable to its type regardless of the policy's globs. A root that names a
// directory is walked, and the files found under it are selected by the globs.
func Collect(fsys fs.FS, roots []string) ([]Source, error) {
	var out []Source
	seen := make(map[string]struct{})

	for _, root := range roots {
		root = path.Clean(strings.TrimPrefix(root, "./"))
		info, err := fs.Stat(fsys, root)
		if err != nil {
			return nil, fmt.Errorf("engine: stat %s: %w", root, err)
		}
		if !info.IsDir() {
			addSource(fsys, root, seen, &out, true)
			continue
		}
		err = fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if skipDir(d.Name()) && p != root {
					return fs.SkipDir
				}
				return nil
			}
			addSource(fsys, p, seen, &out, false)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("engine: walk %s: %w", root, err)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func addSource(fsys fs.FS, p string, seen map[string]struct{}, out *[]Source, explicit bool) {
	if _, dup := seen[p]; dup {
		return
	}
	seen[p] = struct{}{}
	*out = append(*out, Source{
		Path:     p,
		Open:     func() (io.ReadCloser, error) { return fsys.Open(p) },
		Explicit: explicit,
	})
}

// skipDir excludes directories whose content is never project policy surface.
// Walking them is wasted time on the hook path and noise in a report.
func skipDir(name string) bool {
	switch name {
	case ".git", ".jj", "node_modules", "vendor":
		return true
	}
	return false
}
