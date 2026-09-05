package fsops

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiter_CapsConcurrency(t *testing.T) {
	const cap = 3
	l := newLimiter(cap)

	var current int32
	var maxObserved int32
	var wg sync.WaitGroup

	const totalTasks = 20
	for i := 0; i < totalTasks; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.acquire()
			defer l.release()

			n := atomic.AddInt32(&current, 1)
			for {
				old := atomic.LoadInt32(&maxObserved)
				if n <= old || atomic.CompareAndSwapInt32(&maxObserved, old, n) {
					break
				}
			}

			time.Sleep(20 * time.Millisecond) // hold the slot briefly to force contention
			atomic.AddInt32(&current, -1)
		}()
	}
	wg.Wait()

	if maxObserved > cap {
		t.Errorf("observed %d concurrent holders, want <= %d", maxObserved, cap)
	}
	if maxObserved < cap {
		// Not strictly a bug, but suspicious: with 20 tasks and a cap of 3,
		// we'd expect to actually saturate the limiter at least once.
		t.Logf("warning: never saturated the limiter (max observed %d, cap %d) — test may not be exercising contention", maxObserved, cap)
	}
}

func TestLimiter_AllTasksEventuallyComplete(t *testing.T) {
	l := newLimiter(2)
	const totalTasks = 50

	var completed int32
	var wg sync.WaitGroup
	for i := 0; i < totalTasks; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.acquire()
			defer l.release()
			atomic.AddInt32(&completed, 1)
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("tasks did not all complete within timeout — possible deadlock in limiter")
	}

	if got := atomic.LoadInt32(&completed); got != totalTasks {
		t.Errorf("expected all %d tasks to complete, got %d", totalTasks, got)
	}
}

func TestResolveMaxConcurrentOps_Default(t *testing.T) {
	t.Setenv("FSOPS_MAX_CONCURRENT_OPS", "")
	if got := resolveMaxConcurrentOps(); got != 4 {
		t.Errorf("expected default 4, got %d", got)
	}
}

func TestResolveMaxConcurrentOps_EnvOverride(t *testing.T) {
	t.Setenv("FSOPS_MAX_CONCURRENT_OPS", "10")
	if got := resolveMaxConcurrentOps(); got != 10 {
		t.Errorf("expected 10 from env var, got %d", got)
	}
}

func TestResolveMaxConcurrentOps_InvalidFallsBackToDefault(t *testing.T) {
	t.Setenv("FSOPS_MAX_CONCURRENT_OPS", "not-a-number")
	if got := resolveMaxConcurrentOps(); got != 4 {
		t.Errorf("expected fallback to default 4, got %d", got)
	}
}
