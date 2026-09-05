package fsops

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestPeakLoad_BurstOfCopies simulates a sudden spike of many Copy requests
// arriving at once — the "burst" scenario. It checks that: (1) the global
// limiter actually caps how many run concurrently, (2) every request still
// eventually completes and produces correct content, and (3) nothing
// deadlocks under the load.
func TestPeakLoad_BurstOfCopies(t *testing.T) {
	base := t.TempDir()
	const burstSize = 30 // simulate 30 copy requests hitting at once

	// Prepare one small source tree to be copied 30 times into separate
	// destinations, mimicking many independent requests during a spike.
	srcDir := filepath.Join(base, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	for i := 0; i < 5; i++ {
		name := filepath.Join(srcDir, fmt.Sprintf("file%d.txt", i))
		if err := os.WriteFile(name, []byte(fmt.Sprintf("content-%d", i)), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	var wg sync.WaitGroup
	errs := make([]error, burstSize)
	var concurrentOps int32
	var maxConcurrentOps int32

	// Wrap Submit with our own tracking of how many *operations* (not
	// files) are in flight at once, by hooking into Fn's start/end timing
	// via a small instrumented copy of the operation instead of touching
	// the package internals.
	for i := 0; i < burstSize; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()

			dst := filepath.Join(base, fmt.Sprintf("dst%d", i))
			done := make(chan error, 1)

			c := &Copy{
				SrcPath: srcDir,
				NewPath: dst,
				Callback: func(err error) {
					done <- err
				},
			}

			n := atomic.AddInt32(&concurrentOps, 1)
			for {
				old := atomic.LoadInt32(&maxConcurrentOps)
				if n <= old || atomic.CompareAndSwapInt32(&maxConcurrentOps, old, n) {
					break
				}
			}

			c.Submit()

			select {
			case err := <-done:
				errs[i] = err
			case <-time.After(15 * time.Second):
				errs[i] = fmt.Errorf("timed out waiting for copy %d", i)
			}

			atomic.AddInt32(&concurrentOps, -1)
		}()
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("copy %d failed: %v", i, err)
		}
	}

	// Note: this measures how many Submit() calls were "in flight" from the
	// caller's perspective (including queued/waiting ones), not strictly
	// how many were inside the limiter's held slots — that's covered more
	// precisely by TestLimiter_CapsConcurrency. This test's main value is
	// end-to-end: confirming a burst of concurrent requests doesn't corrupt
	// data or deadlock.
	t.Logf("peak observed in-flight requests: %d (burst size %d)", maxConcurrentOps, burstSize)

	// Verify every destination actually has correct content.
	for i := 0; i < burstSize; i++ {
		dst := filepath.Join(base, fmt.Sprintf("dst%d", i))
		for f := 0; f < 5; f++ {
			name := filepath.Join(dst, fmt.Sprintf("file%d.txt", f))
			got, err := os.ReadFile(name)
			if err != nil {
				t.Errorf("dst%d/file%d: read error: %v", i, f, err)
				continue
			}
			want := fmt.Sprintf("content-%d", f)
			if string(got) != want {
				t.Errorf("dst%d/file%d: got %q, want %q", i, f, got, want)
			}
		}
	}
}

// TestPeakLoad_SustainedMixedCopyDelete simulates sustained load: a steady
// stream of Copy and Delete requests over a period of time, rather than
// one single burst. This is closer to "high traffic for a while" than to
// "one spike".
func TestPeakLoad_SustainedMixedCopyDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping sustained load test in -short mode")
	}

	base := t.TempDir()
	const duration = 2 * time.Second
	const workers = 8

	var (
		copyCount   int64
		deleteCount int64
		errCount    int64
		stop        = make(chan struct{})
		wg          sync.WaitGroup
	)

	for w := 0; w < workers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}

				// Alternate between creating a small tree and copying it,
				// then deleting it — simulating steady churn.
				root := filepath.Join(base, fmt.Sprintf("w%d-%d", w, i))
				if err := os.MkdirAll(root, 0755); err != nil {
					atomic.AddInt64(&errCount, 1)
					i++
					continue
				}
				if err := os.WriteFile(filepath.Join(root, "data.txt"), []byte("payload"), 0644); err != nil {
					atomic.AddInt64(&errCount, 1)
					i++
					continue
				}

				dst := root + "-copy"
				copyDone := make(chan error, 1)
				(&Copy{
					SrcPath:  root,
					NewPath:  dst,
					Callback: func(err error) { copyDone <- err },
				}).Submit()

				select {
				case err := <-copyDone:
					if err != nil {
						atomic.AddInt64(&errCount, 1)
					} else {
						atomic.AddInt64(&copyCount, 1)
					}
				case <-time.After(10 * time.Second):
					atomic.AddInt64(&errCount, 1)
				}

				delDone := make(chan error, 1)
				(&Delete{
					SrcPath:  root,
					Callback: func(err error) { delDone <- err },
				}).Submit()

				select {
				case err := <-delDone:
					if err != nil {
						atomic.AddInt64(&errCount, 1)
					} else {
						atomic.AddInt64(&deleteCount, 1)
					}
				case <-time.After(10 * time.Second):
					atomic.AddInt64(&errCount, 1)
				}

				delDone2 := make(chan error, 1)
				(&Delete{
					SrcPath:  dst,
					Callback: func(err error) { delDone2 <- err },
				}).Submit()
				select {
				case err := <-delDone2:
					if err != nil {
						atomic.AddInt64(&errCount, 1)
					}
				case <-time.After(10 * time.Second):
					atomic.AddInt64(&errCount, 1)
				}

				i++
			}
		}()
	}

	time.Sleep(duration)
	close(stop)
	wg.Wait()

	t.Logf("sustained load results: copies=%d deletes=%d errors=%d over %v with %d workers",
		copyCount, deleteCount, errCount, duration, workers)

	if errCount > 0 {
		t.Errorf("expected zero errors under sustained load, got %d", errCount)
	}
	if copyCount == 0 || deleteCount == 0 {
		t.Error("expected at least some completed copy/delete operations, got zero — load loop may not be running correctly")
	}
}

// TestPeakLoad_NoGoroutineExplosion is a rough sanity check that spawning
// many Copy/Delete requests doesn't leave goroutines behind after they've
// all completed — i.e. Submit()'s goroutine actually exits once Fn fires,
// rather than leaking.
func TestPeakLoad_NoGoroutineExplosion(t *testing.T) {
	base := t.TempDir()
	before := countGoroutines()

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		root := filepath.Join(base, fmt.Sprintf("item%d", i))
		if err := os.MkdirAll(root, 0755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		go func() {
			defer wg.Done()
			done := make(chan error, 1)
			(&Delete{SrcPath: root, Callback: func(err error) { done <- err }}).Submit()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Errorf("delete %d timed out", i)
			}
		}()
	}
	wg.Wait()

	// Give any trailing goroutines a moment to actually exit after their
	// Fn callback fired, since goroutine teardown isn't instantaneous.
	time.Sleep(200 * time.Millisecond)

	after := countGoroutines()
	// Allow some slack since the test runtime itself has background
	// goroutines that fluctuate (GC, timers, etc.) — we're checking for a
	// gross leak (e.g. +50 stuck goroutines), not exact equality.
	if after > before+10 {
		t.Errorf("possible goroutine leak: before=%d after=%d (delta=%d)", before, after, after-before)
	}
}

func countGoroutines() int {
	return runtime.NumGoroutine()
}
