package fsops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// defaultConcurrency is deliberately not tied to CPU core count — file
// copying is I/O bound (mostly waiting on read/write syscalls), so a
// higher worker count keeps more I/O in flight at once even on a
// CPU-limited container. Override via Concurrency field or the
// FSOPS_COPY_CONCURRENCY env var if this doesn't fit your storage backend.
const defaultConcurrency = 32

// bufPool reuses copy buffers across files instead of allocating a new
// one per file — matters once concurrency is high, since GC pressure from
// many short-lived large buffers can eat into the gains from concurrency.
var bufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 1<<20) // 1MB, much larger than io.Copy's default 32KB
		return &buf
	},
}

// Copy copies SrcPath to NewPath. Directories are copied recursively with
// files processed concurrently across a worker pool.
type Copy struct {
	SrcPath  string
	NewPath  string
	Callback func(err error)

	// Concurrency caps how many files are copied in parallel. Defaults to
	// defaultConcurrency (or FSOPS_COPY_CONCURRENCY if set) when zero.
	Concurrency int
}

func (c *Copy) Submit() {
	go func() {
		globalLimiter.acquire()
		defer globalLimiter.release()

		err := c.run()
		if c.Callback != nil {
			c.Callback(err)
		}
	}()
}

func (c *Copy) run() error {
	if c.SrcPath == "" {
		return errors.New("fsops: SrcPath must not be empty")
	}
	if c.NewPath == "" {
		return errors.New("fsops: NewPath must not be empty")
	}

	srcAbs, err := filepath.Abs(c.SrcPath)
	if err != nil {
		return fmt.Errorf("fsops: resolving SrcPath: %w", err)
	}
	dstAbs, err := filepath.Abs(c.NewPath)
	if err != nil {
		return fmt.Errorf("fsops: resolving NewPath: %w", err)
	}
	if srcAbs == dstAbs {
		return errors.New("fsops: SrcPath and NewPath are the same location")
	}
	if isSubPath(srcAbs, dstAbs) {
		return errors.New("fsops: NewPath is inside SrcPath")
	}

	concurrency := c.Concurrency
	if concurrency <= 0 {
		concurrency = resolveConcurrency()
	}

	return copyPath(srcAbs, dstAbs, concurrency)
}

func resolveConcurrency() int {
	if v := os.Getenv("FSOPS_COPY_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultConcurrency
}

func isSubPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (!filepath.IsAbs(rel) && rel[0] != '.')
}

func copyPath(src, dst string, concurrency int) error {
	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("fsops: stat source: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return copySymlink(src, dst)
	}
	if info.IsDir() {
		return copyDir(src, dst, concurrency)
	}
	return copyFile(src, dst, info.Mode())
}

// copyDir walks the source tree sequentially (directories must be created
// before files can land inside them) but dispatches each file copy to a
// bounded worker pool, so many files copy concurrently instead of one at
// a time.
func copyDir(src, dst string, concurrency int) error {
	type job struct {
		path string
		info os.FileInfo
		rel  string
	}

	jobs := make(chan job)
	errCh := make(chan error, concurrency)
	var wg sync.WaitGroup

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				target := filepath.Join(dst, j.rel)
				var err error
				if j.info.Mode()&os.ModeSymlink != 0 {
					err = copySymlink(j.path, target)
				} else {
					err = copyFile(j.path, target, j.info.Mode())
				}
				if err != nil {
					select {
					case errCh <- fmt.Errorf("fsops: copying %s: %w", j.rel, err):
					default:
					}
					cancel()
					return
				}
			}
		}()
	}

	walkErr := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		if info.IsDir() {
			target := filepath.Join(dst, rel)
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return err
			}
			return nil
		}

		select {
		case jobs <- job{path: path, info: info, rel: rel}:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	})

	close(jobs)
	wg.Wait()

	if walkErr != nil && !errors.Is(walkErr, context.Canceled) {
		return walkErr
	}
	select {
	case err := <-errCh:
		return err
	default:
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	defer out.Close()

	bufPtr := bufPool.Get().(*[]byte)
	defer bufPool.Put(bufPtr)

	if _, err := io.CopyBuffer(out, in, *bufPtr); err != nil {
		return err
	}
	return out.Sync()
}

func copySymlink(src, dst string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return err
	}
	_ = os.Remove(dst)
	return os.Symlink(target, dst)
}
