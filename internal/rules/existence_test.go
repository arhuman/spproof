package rules

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

var errDenied = errors.New("permission denied")

// countingExistence answers from a map and records how many times each path was
// asked, which is what a cache test asserts on.
type countingExistence struct {
	present map[string]bool
	denied  map[string]bool

	mu    sync.Mutex
	calls map[string]int
	total int
}

func newCounting(present ...string) *countingExistence {
	c := &countingExistence{
		present: make(map[string]bool, len(present)),
		denied:  map[string]bool{},
		calls:   map[string]int{},
	}
	for _, p := range present {
		c.present[p] = true
	}
	return c
}

func (c *countingExistence) Exists(path string) (bool, error) {
	c.mu.Lock()
	c.calls[path]++
	c.total++
	c.mu.Unlock()
	if c.denied[path] {
		return false, errDenied
	}
	return c.present[path], nil
}

func (c *countingExistence) countFor(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[path]
}

func (c *countingExistence) totalCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// TestExistsCacheStatsOnce is the reason the cache exists: the same path
// referenced by many documents costs one syscall for the whole run.
func TestExistsCacheStatsOnce(t *testing.T) {
	under := newCounting("shared.md")
	c := NewExistsCache(under)

	for i := 0; i < 50; i++ {
		found, err := c.Exists("shared.md")
		if err != nil || !found {
			t.Fatalf("Exists = (%v, %v), want (true, nil)", found, err)
		}
	}
	if got := under.countFor("shared.md"); got != 1 {
		t.Errorf("underlying Existence called %d times, want 1", got)
	}
}

// TestExistsCacheRemembersMisses caches the negative answer too: a broken link
// repeated across a tree must not cost one syscall per occurrence.
func TestExistsCacheRemembersMisses(t *testing.T) {
	under := newCounting()
	c := NewExistsCache(under)

	for i := 0; i < 10; i++ {
		found, err := c.Exists("missing.md")
		if err != nil {
			t.Fatalf("Exists returned %v, want a clean negative", err)
		}
		if found {
			t.Fatal("Exists = true for a path the backing store does not have")
		}
	}
	if got := under.countFor("missing.md"); got != 1 {
		t.Errorf("underlying Existence called %d times, want 1", got)
	}
}

// TestExistsCacheRemembersErrors pins that an error is cached, not retried: a
// permission failure is as repeatable as a miss within one run.
func TestExistsCacheRemembersErrors(t *testing.T) {
	under := newCounting()
	under.denied["secret.md"] = true
	c := NewExistsCache(under)

	for i := 0; i < 5; i++ {
		found, err := c.Exists("secret.md")
		if !errors.Is(err, errDenied) {
			t.Fatalf("Exists error = %v, want errDenied", err)
		}
		if found {
			t.Fatal("Exists = true alongside an error")
		}
	}
	if got := under.countFor("secret.md"); got != 1 {
		t.Errorf("underlying Existence called %d times, want 1", got)
	}
}

func TestExistsCacheDistinctPathsAreDistinctAnswers(t *testing.T) {
	under := newCounting("here.md")
	c := NewExistsCache(under)

	if found, _ := c.Exists("here.md"); !found {
		t.Error("here.md should be found")
	}
	if found, _ := c.Exists("gone.md"); found {
		t.Error("gone.md should not be found")
	}
	if got := under.totalCalls(); got != 2 {
		t.Errorf("underlying Existence called %d times, want 2", got)
	}
}

// TestExistsCacheConcurrentAccess drives the cache from many goroutines at once
// so the race detector has something to find if the locking is wrong. The call
// count is not asserted here: two goroutines racing on a cold path may both ask
// the backing store, which costs a duplicate syscall and never a wrong answer.
func TestExistsCacheConcurrentAccess(t *testing.T) {
	under := newCounting("a.md", "b.md")
	c := NewExistsCache(under)

	var wrong atomic.Int64
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				for path, want := range map[string]bool{"a.md": true, "b.md": true, "z.md": false} {
					found, err := c.Exists(path)
					if err != nil || found != want {
						wrong.Add(1)
					}
				}
			}
		}()
	}
	wg.Wait()

	if n := wrong.Load(); n != 0 {
		t.Errorf("%d concurrent reads returned the wrong answer", n)
	}
}

func TestExistsCacheNilUnderUsesTheFilesystem(t *testing.T) {
	c := NewExistsCache(nil)
	dir := t.TempDir()
	existing := filepath.Join(dir, "real.md")
	if err := os.WriteFile(existing, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if found, err := c.Exists(existing); err != nil || !found {
		t.Errorf("Exists(%q) = (%v, %v), want (true, nil)", existing, found, err)
	}
	if found, err := c.Exists(filepath.Join(dir, "nope.md")); err != nil || found {
		t.Errorf("Exists on a missing path = (%v, %v), want (false, nil)", found, err)
	}
}

// TestExistsCacheOSExistenceReportsUnreadableAsError pins that a stat failure
// other than "not there" is an error rather than a clean negative: it
// establishes nothing about the path.
func TestExistsCacheOSExistenceReportsUnreadableAsError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not deny a stat")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	target := filepath.Join(locked, "hidden.md")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	found, err := OSExistence{}.Exists(target)
	if err == nil {
		t.Fatalf("Exists = (%v, nil), want an error: the path was not observed", found)
	}
	if found {
		t.Error("Exists = true alongside an error")
	}
}
