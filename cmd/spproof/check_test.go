package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePolicy puts a policy file in a temp dir and returns its path.
func writePolicy(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.yml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return p
}

const todoPolicy = `version: 1
rules:
  - id: no-todo
    check: pattern_absent
    files: ["**/*.md", "**/*.go"]
    with:
      pattern: "TODO"
`

// inDir runs f with the working directory set to dir. The CLI resolves paths
// against the working directory, so exercising it requires changing there.
func inDir(t *testing.T, dir string, f func()) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(old) }()
	f()
}

func TestExitCodeZeroWhenClean(t *testing.T) {
	policy := writePolicy(t, todoPolicy)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("all good\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb strings.Builder
	inDir(t, dir, func() {
		if code := run([]string{"check", "--policy", policy, "."}, nil, &out, &errb); code != exitOK {
			t.Errorf("exit = %d, want 0 (stderr: %s)", code, errb.String())
		}
	})
	if out.String() != "" {
		t.Errorf("clean run wrote %q, want nothing", out.String())
	}
}

func TestExitCodeOneOnViolation(t *testing.T) {
	policy := writePolicy(t, todoPolicy)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("line\nTODO here\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb strings.Builder
	inDir(t, dir, func() {
		if code := run([]string{"check", "--policy", policy, "."}, nil, &out, &errb); code != exitViolated {
			t.Errorf("exit = %d, want 1 (stderr: %s)", code, errb.String())
		}
	})
	if !strings.HasPrefix(out.String(), "a.md:2:1: [no-todo] ") {
		t.Errorf("got %q, want an editor-jumpable a.md:2:1 line", out.String())
	}
}

// TestExitCodeTwo covers the critical split: an engine that could not run must
// never be mistaken for clean code.
func TestExitCodeTwo(t *testing.T) {
	good := writePolicy(t, todoPolicy)
	tests := []struct {
		name string
		args []string
	}{
		{"missing policy flag", []string{"check", "."}},
		{"policy file absent", []string{"check", "--policy", "/nonexistent/p.yml", "."}},
		{"unknown check", []string{"check", "--policy", writePolicy(t, "version: 1\nrules:\n  - id: a\n    check: nope\n    files: [\"*.md\"]\n"), "."}},
		{"bad version", []string{"check", "--policy", writePolicy(t, "version: 9\nrules:\n  - id: a\n    check: pattern_absent\n    files: [\"*.md\"]\n    with:\n      pattern: \"x\"\n"), "."}},
		{"unknown field", []string{"check", "--policy", writePolicy(t, "version: 1\nnope: 1\nrules:\n  - id: a\n    check: pattern_absent\n    files: [\"*.md\"]\n    with:\n      pattern: \"x\"\n"), "."}},
		{"invalid regex", []string{"check", "--policy", writePolicy(t, "version: 1\nrules:\n  - id: a\n    check: pattern_absent\n    files: [\"*.md\"]\n    with:\n      pattern: \"[bad\"\n"), "."}},
		{"empty rule set", []string{"check", "--policy", writePolicy(t, "version: 1\nrules: []\n"), "."}},
		{"unreadable path", []string{"check", "--policy", good, "no-such-file.md"}},
		{"unknown format", []string{"check", "--policy", good, "--format", "xml", "."}},
		{"stdin without as", []string{"check", "--policy", good, "--stdin"}},
		{"as without stdin", []string{"check", "--policy", good, "--as", "x.md", "."}},
		{"no subcommand", []string{}},
		{"unknown subcommand", []string{"lint"}},
	}

	dir := t.TempDir()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb strings.Builder
			inDir(t, dir, func() {
				if code := run(tc.args, strings.NewReader(""), &out, &errb); code != exitError {
					t.Errorf("exit = %d, want 2", code)
				}
			})
		})
	}
}

func TestStdinWithAs(t *testing.T) {
	policy := writePolicy(t, todoPolicy)
	dir := t.TempDir()

	var out, errb strings.Builder
	inDir(t, dir, func() {
		code := run([]string{"check", "--policy", policy, "--stdin", "--as=draft.md"},
			strings.NewReader("clean\nTODO here\n"), &out, &errb)
		if code != exitViolated {
			t.Fatalf("exit = %d, want 1 (stderr: %s)", code, errb.String())
		}
	})
	if !strings.Contains(out.String(), "draft.md:2:1:") {
		t.Errorf("got %q, want the violation reported under the --as name", out.String())
	}
}

// TestStdinTypeKeyedByAsExtension proves --as supplies the name the file type
// is keyed off, which stdin cannot provide on its own. The check is restricted
// to Go files so the type decides the outcome; a check applying to every type
// would let the file glob decide instead, which --as deliberately bypasses.
func TestStdinTypeKeyedByAsExtension(t *testing.T) {
	policy := writePolicy(t, "version: 1\nrules:\n  - id: go-only\n    check: "+goOnlyViolatingCheck+"\n    files: [\"**/*.go\"]\n    with:\n      pattern: \"TODO\"\n")
	dir := t.TempDir()

	var mdOut, goOut, errb strings.Builder
	inDir(t, dir, func() {
		if code := run([]string{"check", "--policy", policy, "--stdin", "--as=x.md"}, strings.NewReader("TODO\n"), &mdOut, &errb); code != exitOK {
			t.Errorf("markdown under a go-only rule: exit = %d, want 0", code)
		}
		if code := run([]string{"check", "--policy", policy, "--stdin", "--as=x.go"}, strings.NewReader("TODO\n"), &goOut, &errb); code != exitViolated {
			t.Errorf("go file under a go-only rule: exit = %d, want 1", code)
		}
	})
}

// TestStdinOversizedRefusesRatherThanTruncates pins the invariant that content
// the engine never read is never reported as content that held. Truncating at
// the cap would return exit 0 over a violation past it, so the refusal is the
// point: exit 2, never exit 0. The reader is synthetic rather than a real file
// so the case costs no disk.
func TestStdinOversizedRefusesRatherThanTruncates(t *testing.T) {
	policy := writePolicy(t, todoPolicy)
	dir := t.TempDir()

	// One byte past the cap is enough: the violation itself never has to be
	// reached for the run to be unevaluable.
	oversized := io.MultiReader(
		strings.NewReader("clean\n"),
		io.LimitReader(&neverEndingReader{}, maxStdinBytes),
	)

	var out, errb strings.Builder
	inDir(t, dir, func() {
		code := run([]string{"check", "--policy", policy, "--stdin", "--as=draft.md"},
			oversized, &out, &errb)
		if code != exitError {
			t.Fatalf("exit = %d, want 2 (stdout: %q, stderr: %q)", code, out.String(), errb.String())
		}
	})
	if !strings.Contains(errb.String(), "stdin exceeds the maximum size") {
		t.Errorf("stderr = %q, want it to name the size refusal", errb.String())
	}
}

// TestStdinAtCapStillRuns guards the boundary from the other side: the refusal
// must trigger past the cap, not at it, or the fix would reject valid payloads.
func TestStdinAtCapStillRuns(t *testing.T) {
	policy := writePolicy(t, todoPolicy)
	dir := t.TempDir()

	atCap := io.LimitReader(&neverEndingReader{}, maxStdinBytes)

	var out, errb strings.Builder
	inDir(t, dir, func() {
		code := run([]string{"check", "--policy", policy, "--stdin", "--as=draft.md"}, atCap, &out, &errb)
		if code != exitOK {
			t.Fatalf("exit = %d, want 0 at exactly the cap (stderr: %q)", code, errb.String())
		}
	})
}

// TestStdinAndFileAgree is the equivalence the truncation defect broke: the
// same bytes must produce the same verdict through either input path.
func TestStdinAndFileAgree(t *testing.T) {
	policy := writePolicy(t, todoPolicy)
	content := "clean\nTODO here\n"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "draft.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var fileOut, stdinOut, errb strings.Builder
	var fileCode, stdinCode int
	inDir(t, dir, func() {
		fileCode = run([]string{"check", "--policy", policy, "draft.md"}, strings.NewReader(""), &fileOut, &errb)
		stdinCode = run([]string{"check", "--policy", policy, "--stdin", "--as=draft.md"}, strings.NewReader(content), &stdinOut, &errb)
	})
	if fileCode != stdinCode {
		t.Errorf("exit codes disagree: file = %d, stdin = %d", fileCode, stdinCode)
	}
	if fileOut.String() != stdinOut.String() {
		t.Errorf("output disagrees:\n file  = %q\n stdin = %q", fileOut.String(), stdinOut.String())
	}
}

// neverEndingReader yields non-violating lines forever, so a test can build an
// oversized payload without allocating one. The newlines matter: a single line
// past engine.MaxLineLength would fail the run for the wrong reason and mask
// what these tests are pinning.
type neverEndingReader struct{ n int }

func (r *neverEndingReader) Read(p []byte) (int, error) {
	for i := range p {
		if r.n%64 == 63 {
			p[i] = '\n'
		} else {
			p[i] = 'a'
		}
		r.n++
	}
	return len(p), nil
}

func TestJSONFormat(t *testing.T) {
	policy := writePolicy(t, todoPolicy)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("TODO\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb strings.Builder
	inDir(t, dir, func() {
		if code := run([]string{"check", "--policy", policy, "--format", "json", "."}, nil, &out, &errb); code != exitViolated {
			t.Fatalf("exit = %d, want 1 (stderr: %s)", code, errb.String())
		}
	})
	for _, want := range []string{`"ruleId": "no-todo"`, `"uri": "a.md"`, `"startLine": 1`, `"evaluatedFiles": 1`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %s:\n%s", want, out.String())
		}
	}
}

// TestByteIdenticalOutputAcrossRuns is the determinism guarantee stated end to
// end, at the level a CI job actually diffs.
func TestByteIdenticalOutputAcrossRuns(t *testing.T) {
	policy := writePolicy(t, todoPolicy)
	dir := t.TempDir()
	files := map[string]string{
		"z.md":     "TODO\nTODO TODO\n",
		"a.md":     "TODO\n",
		"sub/m.go": "// TODO\n",
		"sub/a.md": "TODO TODO\n",
	}
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			var first string
			inDir(t, dir, func() {
				for i := 0; i < 3; i++ {
					var out, errb strings.Builder
					if code := run([]string{"check", "--policy", policy, "--format", format, "."}, nil, &out, &errb); code != exitViolated {
						t.Fatalf("exit = %d, want 1 (stderr: %s)", code, errb.String())
					}
					if i == 0 {
						first = out.String()
						continue
					}
					if out.String() != first {
						t.Fatalf("run %d output differs:\n got %q\nwant %q", i, out.String(), first)
					}
				}
			})
			if first == "" {
				t.Fatal("expected output")
			}
		})
	}
}

// rootMarkdownPolicy selects only root-level markdown, so a file in a
// subdirectory does not match the glob. It is the shape that made an explicitly
// named violating file report clean.
const rootMarkdownPolicy = `version: 1
rules:
  - id: no-todo
    check: pattern_absent
    files: ["*.md"]
    with:
      pattern: "TODO"
`

// writeFile creates dir/name with body, making parent directories as needed.
func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestExplicitFileIgnoresGlobs is the regression for the founding failure mode:
// naming a file whose path does not match the policy globs used to drop every
// rule, evaluate nothing, and exit 0, which read as "the rule held".
func TestExplicitFileIgnoresGlobs(t *testing.T) {
	policy := writePolicy(t, rootMarkdownPolicy)
	dir := t.TempDir()
	writeFile(t, dir, "sub/nested.md", "clean\nTODO here\n")

	var out, errb strings.Builder
	inDir(t, dir, func() {
		if code := run([]string{"check", "--policy", policy, "--format", "json", "sub/nested.md"}, nil, &out, &errb); code != exitViolated {
			t.Fatalf("exit = %d, want 1 (stderr: %s)", code, errb.String())
		}
	})
	for _, want := range []string{`"evaluatedFiles": 1`, `"uri": "sub/nested.md"`, `"startLine": 2`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %s:\n%s", want, out.String())
		}
	}
}

// TestExplicitFileWithNoApplicableRule keeps the other half of the split: a rule
// dropped because no check decides the file's type is legitimately vacuous,
// unlike a rule dropped by a glob that the caller overrode by naming the file.
func TestExplicitFileWithNoApplicableRule(t *testing.T) {
	policy := writePolicy(t, "version: 1\nrules:\n  - id: go-only\n    check: "+goOnlyCheck+"\n    files: [\"**/*.go\"]\n    with:\n      pattern: \"TODO\"\n")
	dir := t.TempDir()
	writeFile(t, dir, "notes.txt", "TODO here\n")

	var out, errb strings.Builder
	inDir(t, dir, func() {
		if code := run([]string{"check", "--policy", policy, "--format", "json", "notes.txt"}, nil, &out, &errb); code != exitOK {
			t.Fatalf("exit = %d, want 0 (stderr: %s)", code, errb.String())
		}
	})
	if !strings.Contains(out.String(), `"evaluatedFiles": 0`) {
		t.Errorf("want zero evaluated rules, got:\n%s", out.String())
	}
}

// TestDirectoryWalkStillHonoursGlobs proves the explicit-file rule did not widen
// directory behaviour: during a walk the globs remain the selection mechanism.
func TestDirectoryWalkStillHonoursGlobs(t *testing.T) {
	policy := writePolicy(t, rootMarkdownPolicy)
	dir := t.TempDir()
	writeFile(t, dir, "sub/nested.md", "TODO here\n")

	var out, errb strings.Builder
	inDir(t, dir, func() {
		if code := run([]string{"check", "--policy", policy, "--format", "json", "."}, nil, &out, &errb); code != exitOK {
			t.Fatalf("exit = %d, want 0: the subdirectory file must not match *.md (stderr: %s)", code, errb.String())
		}
	})
	if !strings.Contains(out.String(), `"evaluatedFiles": 0`) {
		t.Errorf("walk evaluated a file the globs exclude:\n%s", out.String())
	}
}

// TestFlagsAfterPositionalArguments covers the parse order defect: the flag
// package stops at the first non-flag argument, so a trailing --format used to
// be swallowed as a path.
func TestFlagsAfterPositionalArguments(t *testing.T) {
	policy := writePolicy(t, todoPolicy)
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "TODO\n")

	var flagsFirst, flagsLast, errb strings.Builder
	inDir(t, dir, func() {
		if code := run([]string{"check", "--policy", policy, "--format", "json", "."}, nil, &flagsFirst, &errb); code != exitViolated {
			t.Fatalf("flags first: exit = %d, want 1 (stderr: %s)", code, errb.String())
		}
		if code := run([]string{"check", ".", "--policy", policy, "--format", "json"}, nil, &flagsLast, &errb); code != exitViolated {
			t.Fatalf("flags last: exit = %d, want 1 (stderr: %s)", code, errb.String())
		}
	})
	if flagsFirst.String() != flagsLast.String() {
		t.Errorf("flag position changed output:\n flags first: %q\n flags last:  %q", flagsFirst.String(), flagsLast.String())
	}
	if flagsFirst.String() == "" {
		t.Fatal("expected output")
	}
}

// TestEndOfFlagsTerminator keeps a file whose name looks like a flag reachable.
func TestEndOfFlagsTerminator(t *testing.T) {
	policy := writePolicy(t, todoPolicy)
	dir := t.TempDir()
	writeFile(t, dir, "--format", "TODO\n")

	var out, errb strings.Builder
	inDir(t, dir, func() {
		if code := run([]string{"check", "--policy", policy, "--", "--format"}, nil, &out, &errb); code != exitViolated {
			t.Fatalf("exit = %d, want 1 (stderr: %s)", code, errb.String())
		}
	})
	if !strings.Contains(out.String(), "--format:1:1:") {
		t.Errorf("got %q, want the violation reported under the literal filename", out.String())
	}
}

// TestStdinFollowsExplicitFileRule ties --as to the explicit-file semantics: the
// name is the selection, so the policy globs are not consulted for it.
func TestStdinFollowsExplicitFileRule(t *testing.T) {
	policy := writePolicy(t, rootMarkdownPolicy)
	dir := t.TempDir()

	var out, errb strings.Builder
	inDir(t, dir, func() {
		code := run([]string{"check", "--policy", policy, "--format", "json", "--stdin", "--as=sub/nested.md"},
			strings.NewReader("clean\nTODO here\n"), &out, &errb)
		if code != exitViolated {
			t.Fatalf("exit = %d, want 1 (stderr: %s)", code, errb.String())
		}
	})
	for _, want := range []string{`"evaluatedFiles": 1`, `"uri": "sub/nested.md"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %s:\n%s", want, out.String())
		}
	}
}

// TestDogfoodPolicyEnforcesRepo runs the committed policy over the repository
// it governs, which is the project's stated first dogfood target.
func TestDogfoodPolicyEnforcesRepo(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".spproof.yml")); err != nil {
		t.Skipf("no committed policy: %v", err)
	}

	var out, errb strings.Builder
	inDir(t, root, func() {
		code := run([]string{"check", "--policy", ".spproof.yml", "."}, nil, &out, &errb)
		if code != exitOK {
			t.Errorf("spproof does not pass its own policy (exit %d):\n%s%s", code, out.String(), errb.String())
		}
	})
}

// TestFrontmatterMalformedYAMLExitsOne pins the boundary between the tool's two
// YAML parses. The policy file failing to parse is a broken run (exit 2); a
// checked file's frontmatter failing to parse is a finding about that file
// (exit 1). Conflating them would either hide a real finding behind a crash or
// report a broken policy as clean code.
func TestFrontmatterMalformedYAMLExitsOne(t *testing.T) {
	policy := writePolicy(t, "version: 1\nrules:\n  - id: fm\n    check: yaml_frontmatter\n    files: [\"*.md\"]\n    with:\n      required_keys: [\"name\"]\n")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("---\nname: [unclosed\n---\n\n# Body\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb strings.Builder
	inDir(t, dir, func() {
		if code := run([]string{"check", "--policy", policy, "."}, nil, &out, &errb); code != exitViolated {
			t.Errorf("exit = %d, want %d (stderr: %s)", code, exitViolated, errb.String())
		}
	})
	if !strings.Contains(out.String(), "does not parse") {
		t.Errorf("output = %q, want the parse failure reported as a finding", out.String())
	}
}

// TestFrontmatterUnusableConfigExitsTwo is the other half: a rule that asks
// nothing of a file cannot hold or fail, so it must refuse the run rather than
// render as a check that passed.
func TestFrontmatterUnusableConfigExitsTwo(t *testing.T) {
	policy := writePolicy(t, "version: 1\nrules:\n  - id: fm\n    check: yaml_frontmatter\n    files: [\"*.md\"]\n    with:\n      required_keys: []\n")
	dir := t.TempDir()

	var out, errb strings.Builder
	inDir(t, dir, func() {
		if code := run([]string{"check", "--policy", policy, "."}, nil, &out, &errb); code != exitError {
			t.Errorf("exit = %d, want %d", code, exitError)
		}
	})
}
