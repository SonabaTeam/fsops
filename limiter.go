package fsops

import (
	"os"
	"strconv"
)

// globalLimiter caps how many Copy/Delete operations run at the same time,
// regardless of how many Submit() calls come in. Without this, a burst of
// requests during peak load would each spawn their own goroutine (and their
// own internal worker pool for Copy), collectively overwhelming the
// filesystem — too many open file descriptors, and every task competing
// for the same disk bandwidth ends up slower than running fewer at once.
//
// This is a simple counting semaphore: acquire() blocks until a slot is
// free, release() gives it back. Submit() acquires before starting work and
// releases when it's done, so excess requests simply queue up in FIFO-ish
// order (channel semantics don't guarantee strict FIFO under contention,
// but in practice it's close) instead of running unbounded in parallel.
var globalLimiter = newLimiter(resolveMaxConcurrentOps())

type limiter struct {
	slots chan struct{}
}

func newLimiter(n int) *limiter {
	if n <= 0 {
		n = 1
	}
	return &limiter{slots: make(chan struct{}, n)}
}

func (l *limiter) acquire() { l.slots <- struct{}{} }
func (l *limiter) release() { <-l.slots }

// resolveMaxConcurrentOps controls how many Copy/Delete operations (not
// individual files — whole operations) run at once. Defaults to 4, which
// keeps a handful of large directory copies/deletes running in parallel
// without letting an unbounded burst of requests all hit the disk at once.
// Override with FSOPS_MAX_CONCURRENT_OPS for tuning without a rebuild.
func resolveMaxConcurrentOps() int {
	if v := os.Getenv("FSOPS_MAX_CONCURRENT_OPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 4
}
