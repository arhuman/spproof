package rules

import "fmt"

func init() {
	Register("file_line_max", fileLineMaxFactory{})
}

type fileLineMaxFactory struct{}

// New reads the bound once per file. Validate has already proved it is present
// and positive, so the error cannot occur here.
func (fileLineMaxFactory) New(s Spec) Rule {
	limit, _ := requireMax(s)
	return &fileLineMax{spec: s, max: limit}
}

// AppliesTo accepts every type: counting lines needs no knowledge of the file's
// syntax.
func (fileLineMaxFactory) AppliesTo(FileType) bool { return true }

func (fileLineMaxFactory) Validate(s Spec) error {
	_, err := requireMax(s)
	return err
}

type fileLineMax struct {
	spec  Spec
	max   int
	path  string
	lines int
}

func (r *fileLineMax) Init(f FileMeta) { r.path = f.Path }

// OnLine counts and never reports: a file is only too long once it is
// exhausted, so the verdict comes from Finish. Reporting at the line that
// crossed the limit would name a line that is not itself the problem.
func (r *fileLineMax) OnLine(int, string) []Violation {
	r.lines++
	return nil
}

// Finish reports one file-scoped violation when the file ran past the maximum.
// The length is a property of the whole file, so there is no line to point at.
func (r *fileLineMax) Finish() []Violation {
	if r.lines <= r.max {
		return nil
	}
	return []Violation{{
		RuleID:     r.spec.ID,
		Path:       r.path,
		FileScoped: true,
		Message:    r.spec.Msg(fmt.Sprintf("file is %d lines, over the maximum of %d", r.lines, r.max)),
	}}
}

// Prepare caches the validated bound so New does not re-decode per file.
func (fileLineMaxFactory) Prepare(s Spec) (any, error) { return requireMax(s) }
