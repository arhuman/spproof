package engine

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/arhuman/spproof/internal/policy"
	"github.com/arhuman/spproof/internal/rules"
)

// The corpus is committed rather than generated so a reading means the same
// thing across runs and machines. It is stored as .txt so the Go toolchain does
// not compile it, and fed to the engine under a .go name so every rule applies:
// .txt types as unknown, which has no comment concept, and the two comment
// checks would silently drop.
const (
	corpusPath  = "corpus10k.go.txt"
	corpusName  = "corpus10k.go"
	benchPolicy = "policy-bench.yml"
)

func testdata(t testing.TB, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func loadCorpus(t testing.TB) []byte {
	t.Helper()
	data, err := os.ReadFile(testdata(t, corpusPath))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func loadBenchPolicy(t testing.TB) *policy.Policy {
	t.Helper()
	p, err := policy.Load(testdata(t, benchPolicy))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type nopCloserReader struct{ io.Reader }

func (nopCloserReader) Close() error { return nil }

// BenchmarkThroughput measures a full run over the committed 10000-line corpus
// with every check active. Policy load is excluded: it is measured separately by
// BenchmarkStartup, and charging it per iteration would hide the per-line cost
// this benchmark exists to report.
func BenchmarkThroughput(b *testing.B) {
	p := loadBenchPolicy(b)
	data := loadCorpus(b)
	src := []Source{{
		Path:     corpusName,
		Open:     func() (io.ReadCloser, error) { return nopCloserReader{bytes.NewReader(data)}, nil },
		Explicit: true,
	}}

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r, err := RunWith(p, src, rules.NewExistsCache(newMapExistence()))
		if err != nil {
			b.Fatal(err)
		}
		if len(r.Violations) == 0 {
			b.Fatal("corpus produced no violations: the benchmark is measuring a run that found nothing")
		}
	}
}

// BenchmarkStartup measures what a process pays before it reads the first line:
// reading the policy, decoding it, validating it, and compiling every regex.
// It is the component of the hook path budget the engine controls.
func BenchmarkStartup(b *testing.B) {
	path := testdata(b, benchPolicy)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := policy.Load(path); err != nil {
			b.Fatal(err)
		}
	}
}

// The three strategies R24 leaves to measurement rather than assumption. Each
// answers the same question, "does this line contain the pattern", and the
// corpus is the worst case for all of them: matches are rare, so a strategy
// that short-circuits on a hit almost never gets to.
func BenchmarkStrategyCompiledOnly(b *testing.B) {
	data := loadCorpus(b)
	re := regexp.MustCompile("TODO")
	lines := strings.Split(string(data), "\n")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := 0
		for _, l := range lines {
			if re.MatchString(l) {
				n++
			}
		}
		if n == 0 {
			b.Fatal("no matches")
		}
	}
}

func BenchmarkStrategyPrefilter(b *testing.B) {
	data := loadCorpus(b)
	re := regexp.MustCompile("TODO")
	lines := strings.Split(string(data), "\n")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := 0
		for _, l := range lines {
			if strings.Contains(l, "TODO") && re.MatchString(l) {
				n++
			}
		}
		if n == 0 {
			b.Fatal("no matches")
		}
	}
}

func BenchmarkStrategyAssembledAlternation(b *testing.B) {
	data := loadCorpus(b)
	re := regexp.MustCompile("TODO|FIXME|XXX|HACK")
	lines := strings.Split(string(data), "\n")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := 0
		for _, l := range lines {
			if re.MatchString(l) {
				n++
			}
		}
		if n == 0 {
			b.Fatal("no matches")
		}
	}
}
