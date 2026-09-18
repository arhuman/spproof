package rules

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

// Existence answers whether a path names something on the filesystem.
//
// It exists because the engine walks through io/fs.FS, which rejects "..", and
// a link target legitimately uses "..": a check resolving link targets cannot
// reuse the walk's filesystem. This interface is that separate door, narrow
// enough that a test backs it with a map.
//
// Exists returns (true, nil) when the path names something, (false, nil) when
// it demonstrably does not, and a non-nil error when existence could not be
// established at all (a permission failure, for instance). A caller must not
// read (false, err) as "absent": the two are different facts, and only the
// first was actually observed.
type Existence interface {
	Exists(path string) (bool, error)
}

// OSExistence answers from the real filesystem via os.Stat. Paths are resolved
// by the operating system relative to the process working directory, so a
// relative path means what a shell in the same directory would mean.
type OSExistence struct{}

// Exists reports whether path names something, treating only a
// "does not exist" failure as a negative answer. Any other stat failure is an
// error, since it establishes nothing about the path.
func (OSExistence) Exists(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("rules: %w", err)
	}
	return true, nil
}

// ExistsCache memoizes an Existence so a path referenced by many documents is
// stat-ed once per run. Errors are cached alongside answers: a permission
// failure is as repeatable as a miss within one run, and re-asking would only
// multiply the same syscall.
//
// A cache is safe for concurrent use by multiple goroutines. Two goroutines
// racing on the same uncached path may both call the underlying Existence;
// that costs a duplicate stat, never a wrong answer, and avoids holding a lock
// across a syscall.
type ExistsCache struct {
	under Existence

	mu      sync.RWMutex
	answers map[string]existsAnswer
}

type existsAnswer struct {
	ok  bool
	err error
}

// NewExistsCache wraps under. A nil under is replaced by OSExistence, so a
// zero-configuration caller gets the production behavior rather than a panic.
func NewExistsCache(under Existence) *ExistsCache {
	if under == nil {
		under = OSExistence{}
	}
	return &ExistsCache{under: under, answers: make(map[string]existsAnswer)}
}

// Exists returns the memoized answer for path, consulting the underlying
// Existence only on the first ask.
func (c *ExistsCache) Exists(path string) (bool, error) {
	c.mu.RLock()
	a, ok := c.answers[path]
	c.mu.RUnlock()
	if ok {
		return a.ok, a.err
	}

	found, err := c.under.Exists(path)

	c.mu.Lock()
	c.answers[path] = existsAnswer{ok: found, err: err}
	c.mu.Unlock()
	return found, err
}
