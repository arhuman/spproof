//go:build !race

// The performance gates do not build under -race. The race detector instruments
// every memory access and costs about 27x on this workload (0.8 ms becomes
// 22 ms), so a timed run under it measures the instrumentation rather than the
// engine, and any budget that accommodated it would be too loose to catch a real
// regression. Correctness under concurrency is proven by the resolver tests,
// which do run under -race; these two only measure.

package engine

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/arhuman/spproof/internal/rules"
)

// The two performance gates. Both thresholds are product decisions recorded in
// the plan, not readings copied back from a benchmark: a threshold chosen to fit
// the measurement is a threshold calibrated to pass.
//
// throughputBudget bounds a full run over the committed 10000 line corpus with
// every check active. The reading when it was set was 0.78 ms, so this leaves
// about 6x headroom: enough to absorb a loaded CI runner, tight enough that a
// real regression fails rather than hides.
//
// hookPathBudget bounds one process invocation over one file, end to end,
// including process start. It is fixed by what a write-time hook can afford and
// is not negotiable against a measurement: a reading above it is a finding about
// the implementation, never a reason to raise the bar.
const (
	throughputBudget = 5 * time.Millisecond
	hookPathBudget   = 50 * time.Millisecond
)

// gateRuns is the number of timed attempts a gate takes. The verdict is the
// FASTEST run, not the mean: a timing test on shared hardware measures the
// machine's worst moment as much as the code, and the minimum is the reading
// least polluted by a neighbouring process. A regression slows the floor too.
const gateRuns = 5

func TestThroughputGate(t *testing.T) {
	p := loadBenchPolicy(t)
	data := loadCorpus(t)
	src := []Source{{
		Path:     corpusName,
		Open:     func() (io.ReadCloser, error) { return nopCloserReader{bytes.NewReader(data)}, nil },
		Explicit: true,
	}}

	best := time.Duration(1<<63 - 1)
	for i := 0; i < gateRuns; i++ {
		start := time.Now()
		r, err := RunWith(p, src, rules.NewExistsCache(newMapExistence()))
		elapsed := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Violations) == 0 {
			t.Fatal("corpus produced no violations: the gate is timing a run that found nothing")
		}
		if elapsed < best {
			best = elapsed
		}
	}

	t.Logf("10000 lines, every check active: %v (budget %v)", best, throughputBudget)
	if best > throughputBudget {
		t.Errorf("throughput %v exceeds the budget of %v", best, throughputBudget)
	}
}

func TestHookPathGate(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	// The fixture satisfies the benchmark policy, so the timed runs exit 0. A
	// run exiting 1 would still be a valid measurement, but it would hide a
	// genuine engine error behind the same exit code.
	body := "package main\n\nfunc compute(a int, b string) {}\n\nfunc main() { compute(1, \"x\") }\n"
	if err := os.WriteFile(filepath.Join(dir, "one.go"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := testdata(t, benchPolicy)

	// One warm run first: the gate measures steady-state invocation, not the
	// operating system's first read of a freshly linked binary.
	warm := exec.CommandContext(t.Context(), bin, "check", "--policy", policy, "one.go")
	warm.Dir = dir
	if out, err := warm.CombinedOutput(); err != nil {
		t.Fatalf("warm-up run failed, so the fixture does not satisfy the policy: %v\n%s", err, out)
	}

	best := time.Duration(1<<63 - 1)
	for i := 0; i < gateRuns; i++ {
		cmd := exec.CommandContext(t.Context(), bin, "check", "--policy", policy, "one.go")
		cmd.Dir = dir
		start := time.Now()
		err := cmd.Run()
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if elapsed < best {
			best = elapsed
		}
	}

	t.Logf("one file, one invocation, including process start: %v (budget %v)", best, hookPathBudget)
	if best > hookPathBudget {
		t.Errorf("hook path %v exceeds the budget of %v", best, hookPathBudget)
	}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "spproof")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "../../cmd/spproof")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the binary: %v\n%s", err, out)
	}
	return bin
}
