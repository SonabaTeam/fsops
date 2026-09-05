package fsops

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// waitForCopy blocks until the Copy's Callback callback fires, with a timeout so
// a hung goroutine fails the test instead of hanging forever.
func waitForCopy(t *testing.T, c *Copy) error {
	t.Helper()
	done := make(chan error, 1)
	c.Callback = func(err error) { done <- err }
	c.Submit()

	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("Copy.Submit() callback never fired within timeout")
		return nil
	}
}

func TestCopy_SingleFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	content := []byte("hello world")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	c := &Copy{SrcPath: src, NewPath: dst}
	if err := waitForCopy(t, c); err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("reading copied file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
}

func TestCopy_PreservesFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits don't map the same way on Windows")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "script.sh")
	dst := filepath.Join(dir, "script_copy.sh")

	if err := os.WriteFile(src, []byte("#!/bin/sh\necho hi"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	c := &Copy{SrcPath: src, NewPath: dst}
	if err := waitForCopy(t, c); err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat copied file: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("expected mode 0755, got %v", info.Mode().Perm())
	}
}

func TestCopy_DirectoryTree(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	files := map[string]string{
		"a.txt":            "file a",
		"sub/b.txt":        "file b",
		"sub/nested/c.txt": "file c",
	}
	for rel, content := range files {
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("setup mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("setup write: %v", err)
		}
	}

	c := &Copy{SrcPath: src, NewPath: dst}
	if err := waitForCopy(t, c); err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil {
			t.Errorf("reading %s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s: got %q, want %q", rel, got, want)
		}
	}
}

func TestCopy_ManyFilesConcurrently(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	const n = 200
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	for i := 0; i < n; i++ {
		name := filepath.Join(src, "file"+itoa(i)+".txt")
		if err := os.WriteFile(name, []byte("content "+itoa(i)), 0644); err != nil {
			t.Fatalf("setup write %d: %v", i, err)
		}
	}

	c := &Copy{SrcPath: src, NewPath: dst, Concurrency: 16}
	if err := waitForCopy(t, c); err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	entries, err := os.ReadDir(dst)
	if err != nil {
		t.Fatalf("reading dst dir: %v", err)
	}
	if len(entries) != n {
		t.Errorf("expected %d files, got %d", n, len(entries))
	}
	for i := 0; i < n; i++ {
		name := filepath.Join(dst, "file"+itoa(i)+".txt")
		got, err := os.ReadFile(name)
		if err != nil {
			t.Errorf("file %d missing or unreadable: %v", i, err)
			continue
		}
		want := "content " + itoa(i)
		if string(got) != want {
			t.Errorf("file %d: got %q, want %q", i, got, want)
		}
	}
}

func TestCopy_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "empty")
	dst := filepath.Join(dir, "empty_copy")

	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	c := &Copy{SrcPath: src, NewPath: dst}
	if err := waitForCopy(t, c); err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("dst directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("dst is not a directory")
	}
}

func TestCopy_Symlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require elevated privileges on Windows")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	src := filepath.Join(dir, "link")
	dst := filepath.Join(dir, "link_copy")

	if err := os.WriteFile(target, []byte("real content"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.Symlink(target, src); err != nil {
		t.Fatalf("setup symlink: %v", err)
	}

	c := &Copy{SrcPath: src, NewPath: dst}
	if err := waitForCopy(t, c); err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	got, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("dst is not a symlink: %v", err)
	}
	if got != target {
		t.Errorf("symlink target: got %q, want %q", got, target)
	}
}

func TestCopy_DirectoryWithSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require elevated privileges on Windows")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	realFile := filepath.Join(src, "real.txt")
	if err := os.WriteFile(realFile, []byte("real"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	linkPath := filepath.Join(src, "link.txt")
	if err := os.Symlink(realFile, linkPath); err != nil {
		t.Fatalf("setup symlink: %v", err)
	}

	c := &Copy{SrcPath: src, NewPath: dst}
	if err := waitForCopy(t, c); err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	got, err := os.Readlink(filepath.Join(dst, "link.txt"))
	if err != nil {
		t.Fatalf("copied link is not a symlink: %v", err)
	}
	if got != realFile {
		t.Errorf("symlink target: got %q, want %q", got, realFile)
	}
}

func TestCopy_NonexistentSource(t *testing.T) {
	dir := t.TempDir()
	c := &Copy{
		SrcPath: filepath.Join(dir, "does-not-exist"),
		NewPath: filepath.Join(dir, "dst"),
	}
	err := waitForCopy(t, c)
	if err == nil {
		t.Fatal("expected error for nonexistent source, got nil")
	}
}

func TestCopy_EmptySrcPath(t *testing.T) {
	c := &Copy{SrcPath: "", NewPath: "/tmp/whatever"}
	err := waitForCopy(t, c)
	if err == nil {
		t.Fatal("expected error for empty SrcPath, got nil")
	}
}

func TestCopy_EmptyNewPath(t *testing.T) {
	dir := t.TempDir()
	c := &Copy{SrcPath: dir, NewPath: ""}
	err := waitForCopy(t, c)
	if err == nil {
		t.Fatal("expected error for empty NewPath, got nil")
	}
}

func TestCopy_SameSourceAndDest(t *testing.T) {
	dir := t.TempDir()
	c := &Copy{SrcPath: dir, NewPath: dir}
	err := waitForCopy(t, c)
	if err == nil {
		t.Fatal("expected error when SrcPath == NewPath, got nil")
	}
}

func TestCopy_DestInsideSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "parent")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	dst := filepath.Join(src, "child")

	c := &Copy{SrcPath: src, NewPath: dst}
	err := waitForCopy(t, c)
	if err == nil {
		t.Fatal("expected error when NewPath is inside SrcPath, got nil")
	}
}

func TestCopy_ConcurrencyEnvVarOverride(t *testing.T) {
	t.Setenv("FSOPS_COPY_CONCURRENCY", "7")
	got := resolveConcurrency()
	if got != 7 {
		t.Errorf("expected concurrency 7 from env var, got %d", got)
	}
}

func TestCopy_ConcurrencyDefaultsWhenEnvUnset(t *testing.T) {
	t.Setenv("FSOPS_COPY_CONCURRENCY", "")
	got := resolveConcurrency()
	if got != defaultConcurrency {
		t.Errorf("expected default concurrency %d, got %d", defaultConcurrency, got)
	}
}

func TestCopy_ConcurrencyIgnoresInvalidEnvVar(t *testing.T) {
	t.Setenv("FSOPS_COPY_CONCURRENCY", "not-a-number")
	got := resolveConcurrency()
	if got != defaultConcurrency {
		t.Errorf("expected fallback to default %d for invalid env var, got %d", defaultConcurrency, got)
	}
}

// itoa avoids pulling in strconv just for test fixtures.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestIsSubPath(t *testing.T) {
	tests := []struct {
		parent, child string
		want          bool
	}{
		{"/a/b", "/a/b/c", true},
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/c", false},
		{"/a/b", "/a", false},
	}
	for _, tt := range tests {
		got := isSubPath(filepath.FromSlash(tt.parent), filepath.FromSlash(tt.child))
		if got != tt.want {
			t.Errorf("isSubPath(%q, %q) = %v, want %v", tt.parent, tt.child, got, tt.want)
		}
	}
}

func TestCopy_LargeFileContentIntegrity(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "large.bin")
	dst := filepath.Join(dir, "large_copy.bin")

	// Larger than the 1MB pooled buffer, to make sure multi-chunk copies
	// via io.CopyBuffer reassemble correctly.
	data := make([]byte, 5*1<<20) // 5MB
	for i := range data {
		data[i] = byte(i % 256)
	}
	if err := os.WriteFile(src, data, 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	c := &Copy{SrcPath: src, NewPath: dst}
	if err := waitForCopy(t, c); err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("reading copied file: %v", err)
	}
	if len(got) != len(data) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(data))
	}
	if !bytesEqual(got, data) {
		t.Error("content mismatch after copy")
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCopy_ErrorFromOneFileStopsAndReports(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based failure simulation behaves differently on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("running as root ignores permission bits, can't simulate a permission error")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// A file this process can't read.
	unreadable := filepath.Join(src, "secret.txt")
	if err := os.WriteFile(unreadable, []byte("shh"), 0000); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0644) }) // let TempDir cleanup succeed

	c := &Copy{SrcPath: src, NewPath: dst}
	err := waitForCopy(t, c)
	if err == nil {
		t.Fatal("expected an error copying an unreadable file, got nil")
	}
	if !strings.Contains(err.Error(), "secret.txt") {
		t.Errorf("expected error to mention the failing file, got: %v", err)
	}
}
