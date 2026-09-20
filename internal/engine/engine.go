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

// ErrNotRegularFile is returned when a path named directly is a symlink, device,
// socket or FIFO rather than a regular file. Such a path is refused rather than
// checked: following it could read content from outside the tree, and reading a
// FIFO can block forever. Non-regular files met while walking a directory are
// skipped silently instead, since the walk never asked for them by name.
var ErrNotRegularFile = errors.New("engine: not a regular file")

// Result is the outcome of a run: the violations found, and the per-rule record
// of where each rule actually ran.
type Result struct {
	Violations []rules.Violation
	Coverage   []RuleCoverage
	// Tolerated holds the violations a ratchet absorbed. They are real
	// findings that did not fail the run, kept here rather than dropped so a
	// caller can show what the baseline is currently hiding: a tolerated
	// violation must never read as no violation.
	Tolerated []rules.Violation
	// Ratchets records, per ratcheted rule, how the count compared to the
	// declared limits. A rule with no ratchet does not appear.
	Ratchets []RatchetStatus
}

// RatchetStatus is what a ratcheted rule counted against its limits.
type RatchetStatus struct {
	RuleID string
	// Found is the number of violations the rule produced across the run.
	Found int
	// Limit is the declared tolerance that decided the verdict, and Scope names
	// which limit it was ("run" or "file"). For the per-file scope Found is the
	// count in the worst file, since that is what the limit bounds.
	Limit int
	Scope  string
	// Exceeded reports whether this rule failed the run.
	Exceeded bool
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
	kept, tolerated, status := applyRatchets(p, violations)
	return Result{
		Violations: kept,
		Coverage:   coverage(p, evaluated),
		Tolerated:  tolerated,
		Ratchets:   status,
	}, nil
}

// applyRatchets splits the sorted violations into the ones that fail the run
// and the ones a declared baseline absorbs.
//
// A ratchet is decided by count, not by identity: the engine knows how many
// violations exist, never which are new. So a rule over its limit keeps every
// violation rather than an arbitrary excess, leaving the author to choose what
// to remove, and a rule under its limit keeps none.
//
// The input must already be sorted, which is what makes the split deterministic
// for a rule that declares both limits.
func applyRatchets(p *policy.Policy, sorted []rules.Violation) (kept, tolerated []rules.Violation, status []RatchetStatus) {
	ratcheted := make(map[string]policy.Ratchet, len(p.Rules))
	for _, pr := range p.Rules {
		if !pr.Ratchet.Zero() {
			ratcheted[pr.Spec.ID] = pr.Ratchet
		}
	}
	if len(ratcheted) == 0 {
		return sorted, nil, nil
	}

	total, worst := countPerRule(sorted, ratcheted)

	exceeded := make(map[string]bool, len(ratcheted))
	for id, r := range ratcheted {
		st := ratchetVerdict(id, r, total[id], worst[id])
		exceeded[id] = st.Exceeded
		status = append(status, st)
	}
	sort.Slice(status, func(i, j int) bool { return status[i].RuleID < status[j].RuleID })

	for _, v := range sorted {
		if _, ok := ratcheted[v.RuleID]; ok && !exceeded[v.RuleID] {
			tolerated = append(tolerated, v)
			continue
		}
		kept = append(kept, v)
	}
	return kept, tolerated, status
}

func checkFile(p *policy.Policy, src Source, evaluated map[string]int, res *resolver) ([]rules.Violation, error) {
	meta := rules.FileMeta{Path: src.Path, Type: rules.TypeOf(src.Path)}

	active := activeRules(p, src, meta, evaluated)
	if len(active) == 0 {
		return nil, nil
	}

	rc, err := src.Open()
	if err != nil {
		return nil, fmt.Errorf("engine: open %s: %w", src.Path, err)
	}
	defer rc.Close()

	contextual := contextualRules(active)
	// Classification is markdown-specific and costs a scan of every line, so it
	// is computed only when some active rule actually consumes it.
	aware := classAwareRules(active)
	if meta.Type != rules.TypeMarkdown {
		aware = nil
	}

	out, err := scanLines(rc, active, contextual, aware, res)
	if err != nil {
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

// activeRules instantiates the rules that apply to this source and counts each
// one in evaluated. An Explicit source skips the glob test: naming the file is
// itself the selection.
func activeRules(p *policy.Policy, src Source, meta rules.FileMeta, evaluated map[string]int) []rules.Rule {
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
	return active
}

// classAwareRules collects the rules that consume a line's prose/code split, so
// the classifier runs only when something reads it.
func classAwareRules(active []rules.Rule) []rules.ClassAware {
	var aware []rules.ClassAware
	for _, r := range active {
		if c, ok := r.(rules.ClassAware); ok {
			aware = append(aware, c)
		}
	}
	return aware
}

func contextualRules(active []rules.Rule) []rules.Contextual {
	contextual := make([]rules.Contextual, 0, len(active))
	for _, r := range active {
		if c, ok := r.(rules.Contextual); ok {
			contextual = append(contextual, c)
		}
	}
	return contextual
}

// scanLines feeds every line to every active rule and dispatches contextual
// candidates as they appear. The returned error is the scanner's, unwrapped, so
// the caller can distinguish a too-long line from a read failure.
func scanLines(rc io.Reader, active []rules.Rule, contextual []rules.Contextual, aware []rules.ClassAware, res *resolver) ([]rules.Violation, error) {
	var out []rules.Violation
	var cls rules.Classifier
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 64*1024), MaxLineLength)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		// Classify once per line and hand the same answer to every aware rule,
		// so two rules cannot disagree about where a fence ends.
		if len(aware) > 0 {
			c := cls.Classify(line)
			for _, a := range aware {
				a.OnClass(c)
			}
		}
		for _, r := range active {
			out = append(out, r.OnLine(n, line)...)
		}
		for _, c := range contextual {
			res.dispatch(c.TakeCandidates())
		}
	}
	return out, sc.Err()
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
		// Lstat, not Stat: Stat resolves a symlink and would report the target's
		// mode, letting a link named on the command line pass the regular-file
		// check below and be read from outside the tree.
		info, err := fs.Lstat(fsys, root)
		if err != nil {
			return nil, fmt.Errorf("engine: stat %s: %w", root, err)
		}
		if !info.IsDir() {
			// A named file that is not a regular file refuses the run rather
			// than being skipped. Naming it is a request to check it, and
			// silently checking nothing is the clean-looking verdict this tool
			// exists to prevent.
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("%w: %s", ErrNotRegularFile, root)
			}
			addSource(fsys, root, seen, &out, true)
			continue
		}
		if err := walkDir(fsys, root, seen, &out); err != nil {
			return nil, fmt.Errorf("engine: walk %s: %w", root, err)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// walkDir appends every non-skipped file under root as a non-explicit source.
// The root itself is walked even when its own name would be skipped: asking for
// a directory by name is an explicit request to look inside it.
//
// Only regular files are admitted. A symlink is not followed, so the walk cannot
// leave the tree it was given and report an outside file under an inside path. A
// device or FIFO is not opened, since reading one can block forever and a run
// that never returns is worse for a hook than one that fails. Both are skipped
// silently: an entry met while walking was never asked for by name.
func walkDir(fsys fs.FS, root string, seen map[string]struct{}, out *[]Source) error {
	return fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) && p != root {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		addSource(fsys, p, seen, out, false)
		return nil
	})
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

// countPerRule returns, per ratcheted rule, the total violation count and the
// count in whichever file carries the most. The two feed the run-scoped and
// per-file limits respectively.
func countPerRule(sorted []rules.Violation, ratcheted map[string]policy.Ratchet) (total, worst map[string]int) {
	total = make(map[string]int, len(ratcheted))
	worst = make(map[string]int, len(ratcheted))
	perFile := make(map[string]map[string]int, len(ratcheted))
	for _, v := range sorted {
		if _, ok := ratcheted[v.RuleID]; !ok {
			continue
		}
		total[v.RuleID]++
		byPath, ok := perFile[v.RuleID]
		if !ok {
			byPath = map[string]int{}
			perFile[v.RuleID] = byPath
		}
		byPath[v.Path]++
		if byPath[v.Path] > worst[v.RuleID] {
			worst[v.RuleID] = byPath[v.Path]
		}
	}
	return total, worst
}

// ratchetVerdict decides one rule against its declared limits. A rule that
// declares both fails if either is breached, and the reported scope names the
// limit that actually decided the verdict.
func ratchetVerdict(id string, r policy.Ratchet, total, worst int) RatchetStatus {
	st := RatchetStatus{RuleID: id}
	switch {
	case r.Run != nil && total > *r.Run:
		st.Found, st.Limit, st.Scope, st.Exceeded = total, *r.Run, "run", true
	case r.PerFile != nil && worst > *r.PerFile:
		st.Found, st.Limit, st.Scope, st.Exceeded = worst, *r.PerFile, "file", true
	case r.Run != nil:
		st.Found, st.Limit, st.Scope = total, *r.Run, "run"
	default:
		st.Found, st.Limit, st.Scope = worst, *r.PerFile, "file"
	}
	return st
}
