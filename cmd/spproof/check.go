package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/arhuman/spproof/internal/engine"
	"github.com/arhuman/spproof/internal/policy"
	"github.com/arhuman/spproof/internal/report"
	"github.com/arhuman/spproof/internal/version"
)

// Exit codes. These are the command's contract with CI, hooks and agents.
const (
	exitOK       = 0
	exitViolated = 1
	exitError    = 2
)

const usage = `spproof proves that declared static checks hold over files.

Usage:
  spproof check [paths...] --policy <path> [--format text|json|sarif]
  spproof check --stdin --as=<filename> --policy <path>
  spproof version

Flags:
  --policy <path>   policy file (required; never discovered, so a verdict
                    depends on nothing ambient)
  --format text     text (default), json, or sarif (SARIF 2.1.0)
  --stdin           read content from stdin instead of walking paths
  --as <filename>   name stdin content is checked under; its extension keys
                    the file type

Exit codes:
  0  every applicable rule held
  1  at least one violation
  2  the engine could not run
`

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Fprintf(stdout, "spproof %s\n", version.Build)
		return exitOK
	}
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprint(stderr, usage)
		return exitError
	}

	opts, ok := parseCheckFlags(args[1:], stderr)
	if !ok {
		return exitError
	}
	return runCheck(opts, stdin, stdout, stderr)
}

// checkOptions is the parsed and validated form of a "check" invocation.
type checkOptions struct {
	policyPath string
	format     string
	useStdin   bool
	asName     string
	paths      []string
}

// parseCheckFlags parses and validates the check flags, reporting any problem on
// stderr itself. A false second result means the caller should exit with
// exitError and print nothing more.
func parseCheckFlags(args []string, stderr io.Writer) (checkOptions, bool) {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	var (
		policyPath = fs.String("policy", "", "policy file path (required)")
		format     = fs.String("format", "text", "output format: text, json or sarif")
		useStdin   = fs.Bool("stdin", false, "read content from stdin")
		asName     = fs.String("as", "", "filename stdin content is checked under")
	)
	if err := fs.Parse(permute(fs, args)); err != nil {
		return checkOptions{}, false
	}

	if *policyPath == "" {
		fmt.Fprintln(stderr, "spproof: --policy is required")
		return checkOptions{}, false
	}
	if *format != "text" && *format != "json" && *format != "sarif" {
		fmt.Fprintf(stderr, "spproof: unknown format %q\n", *format)
		return checkOptions{}, false
	}
	return checkOptions{
		policyPath: *policyPath,
		format:     *format,
		useStdin:   *useStdin,
		asName:     *asName,
		paths:      fs.Args(),
	}, true
}

// runCheck loads the policy, runs the engine over the requested sources and
// renders the result, mapping every failure onto the command's exit codes.
func runCheck(opts checkOptions, stdin io.Reader, stdout, stderr io.Writer) int {
	p, err := policy.Load(opts.policyPath)
	if err != nil {
		fmt.Fprintf(stderr, "spproof: %v\n", err)
		return exitError
	}

	sources, err := collect(opts.paths, opts.useStdin, opts.asName, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "spproof: %v\n", err)
		return exitError
	}

	result, err := engine.Run(p, sources)
	if err != nil {
		fmt.Fprintf(stderr, "spproof: %v\n", err)
		return exitError
	}

	if err := render(stdout, opts.format, result); err != nil {
		fmt.Fprintf(stderr, "spproof: %v\n", err)
		return exitError
	}
	if !result.OK() {
		return exitViolated
	}
	return exitOK
}

// permute reorders args so every flag precedes every positional argument.
//
// The flag package stops parsing at the first non-flag argument, which would
// turn "check . --format json" into three paths. Reordering makes flag position
// irrelevant, so a flag may follow a path. A literal "--" ends flag parsing:
// everything after it is a positional, which keeps a file genuinely named
// "--format" reachable. The returned slice keeps "--" so flag.Parse stops there
// too. Argument order among the positionals is preserved.
func permute(fs *flag.FlagSet, args []string) []string {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		if strings.Contains(a, "=") || !takesValue(fs, a) {
			continue
		}
		// A non-boolean flag written as "--name value" owns the next argument;
		// leaving it behind would reclassify the value as a path.
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

// takesValue reports whether the flag spelled as this argument consumes a
// following argument. An unknown flag is assumed to, since flag.Parse will
// reject it either way and guessing the other way would eat a real path.
func takesValue(fs *flag.FlagSet, arg string) bool {
	f := fs.Lookup(strings.TrimLeft(arg, "-"))
	if f == nil {
		return true
	}
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	if ok && bf.IsBoolFlag() {
		return false
	}
	return true
}

func render(w io.Writer, format string, r engine.Result) error {
	switch format {
	case "json":
		return report.JSON(w, r)
	case "sarif":
		return report.SARIF(w, r)
	default:
		return report.Text(w, r)
	}
}

func collect(paths []string, useStdin bool, asName string, stdin io.Reader) ([]engine.Source, error) {
	if useStdin {
		return collectStdin(paths, asName, stdin)
	}
	if asName != "" {
		return nil, errors.New("--as requires --stdin")
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	return collectPaths(paths)
}

// collectStdin reads the whole stdin payload into a single source named by
// asName. It refuses rather than truncates when the payload exceeds
// maxStdinBytes, so a rule is never reported as holding over bytes the engine
// did not read.
func collectStdin(paths []string, asName string, stdin io.Reader) ([]engine.Source, error) {
	if asName == "" {
		return nil, errors.New("--stdin requires --as=<filename>")
	}
	if len(paths) > 0 {
		return nil, errors.New("--stdin takes no path arguments")
	}
	// Reading one byte past the cap is what makes an oversized payload
	// detectable at all: at exactly the cap the two cases are indistinguishable.
	content, err := io.ReadAll(io.LimitReader(stdin, maxStdinBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	if len(content) > maxStdinBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrStdinTooLarge, maxStdinBytes)
	}
	return []engine.Source{{
		Path:     filepath.ToSlash(asName),
		Open:     func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(string(content))), nil },
		Explicit: true,
	}}, nil
}

// maxStdinBytes bounds a hook's payload. A write-time hook sends one file, so
// anything larger is a misuse rather than a case to support.
const maxStdinBytes = 64 << 20

// ErrStdinTooLarge reports that stdin exceeded maxStdinBytes. The run refuses
// rather than checking a prefix of the content, so this is an exit 2 (the
// engine could not run) and never an exit 0.
var ErrStdinTooLarge = errors.New("stdin exceeds the maximum size")

// collectPaths maps OS paths onto an fs.FS rooted at the working directory.
// fs.FS rejects absolute paths and "..", so each argument is made relative to
// the root first.
func collectPaths(paths []string) ([]engine.Source, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("working directory: %w", err)
	}
	fsys := os.DirFS(root)

	rels := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", p, err)
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("path %s is outside the working directory", p)
		}
		rels = append(rels, filepath.ToSlash(rel))
	}
	return engine.Collect(fsys, rels)
}
