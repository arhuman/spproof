package engine

import (
	"sync"

	"github.com/arhuman/spproof/internal/rules"
)

// resolveWorkers bounds how many existence resolutions run at once, and
// resolveQueue how many wait to. A document tree can carry tens of thousands of
// links; one goroutine per link would trade a bounded cost for an unbounded one
// on exactly the large trees the bound is for. The queue is what keeps the line
// reader off the syscall: it hands a candidate over and reads the next line.
const (
	resolveWorkers = 8
	resolveQueue   = 1024
)

// resolver resolves link candidates off the read loop.
//
// It owns the only concurrency in a run. Workers start on first use and stop on
// wait; candidates queued but not yet resolved are still resolved, because wait
// closes the queue and joins the workers rather than abandoning them. That is
// what makes "the run waited for every dispatched candidate" structural rather
// than a matter of timing.
//
// A resolver is used from a single goroutine (the reader) for dispatch and
// wait; only its workers run concurrently.
type resolver struct {
	exists rules.Existence
	queue  chan rules.Candidate
	wg     sync.WaitGroup

	mu  sync.Mutex
	out []rules.Violation
}

func newResolver(exists rules.Existence) *resolver {
	return &resolver{exists: rules.NewExistsCache(exists)}
}

// dispatch hands candidates to the workers. It returns as soon as they are
// queued: the caller is the line reader, which must not wait on a syscall.
func (r *resolver) dispatch(cs []rules.Candidate) {
	if len(cs) == 0 {
		return
	}
	r.start()
	for _, c := range cs {
		r.queue <- c
	}
}

func (r *resolver) start() {
	if r.queue != nil {
		return
	}
	r.queue = make(chan rules.Candidate, resolveQueue)
	r.wg.Add(resolveWorkers)
	for i := 0; i < resolveWorkers; i++ {
		go func() {
			defer r.wg.Done()
			for c := range r.queue {
				r.resolve(c)
			}
		}()
	}
}

func (r *resolver) resolve(c rules.Candidate) {
	found, err := r.exists.Exists(c.Target)
	if found && err == nil {
		return
	}
	v := c.Violation(err)
	r.mu.Lock()
	r.out = append(r.out, v)
	r.mu.Unlock()
}

// wait drains every queued candidate, joins the workers and returns the
// violations. Arrival order is nondeterministic, so the caller must sort before
// reporting.
func (r *resolver) wait() []rules.Violation {
	if r.queue == nil {
		return nil
	}
	close(r.queue)
	r.wg.Wait()
	r.queue = nil

	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.out
	r.out = nil
	return out
}
