package fsops

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitForDelete(t *testing.T, d *Delete) error {
	t.Helper()
	done := make(chan error, 1)
	d.Callback = func(err error) { done <- err }
	d.Submit()

	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("Delete.Submit() callback never fired within timeout")
		return nil
	}
}

func TestDelete_SingleFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(target, []byte("bye"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	d := &Delete{SrcPath: target}
	if err := waitForDelete(t, d); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("expected file to be gone, stat err = %v", err)
	}
}

func TestDelete_DirectoryTree(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tree")
	nested := filepath.Join(target, "a", "b")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	d := &Delete{SrcPath: target}
	if err := waitForDelete(t, d); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("expected directory tree to be gone, stat err = %v", err)
	}
}

func TestDelete_NonexistentPathIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	d := &Delete{SrcPath: filepath.Join(dir, "does-not-exist")}
	// os.RemoveAll on a nonexistent path is a no-op, not an error — same
	// semantics as the standard library.
	if err := waitForDelete(t, d); err != nil {
		t.Errorf("expected nil error for nonexistent path, got %v", err)
	}
}

func TestDelete_EmptyPathRejected(t *testing.T) {
	d := &Delete{SrcPath: ""}
	err := waitForDelete(t, d)
	if err == nil {
		t.Fatal("expected error for empty SrcPath, got nil")
	}
}

func TestDelete_RefusesFilesystemRoot(t *testing.T) {
	root := string(filepath.Separator)
	d := &Delete{SrcPath: root}
	err := waitForDelete(t, d)
	if err == nil {
		t.Fatal("expected error when deleting filesystem root, got nil")
	}
}

func TestDelete_RefusesWindowsDriveRoot(t *testing.T) {
	if filepath.VolumeName("C:\\") == "" {
		t.Skip("not running on an OS with drive letters")
	}
	d := &Delete{SrcPath: "C:\\"}
	err := waitForDelete(t, d)
	if err == nil {
		t.Fatal("expected error when deleting a drive root, got nil")
	}
}

func TestValidateDeletePath(t *testing.T) {
	dir := t.TempDir()
	safe := filepath.Join(dir, "safe-to-delete")

	if err := validateDeletePath(safe); err != nil {
		t.Errorf("expected a normal path to pass validation, got %v", err)
	}
	if err := validateDeletePath(""); err == nil {
		t.Error("expected empty path to be rejected")
	}
	if err := validateDeletePath(string(filepath.Separator)); err == nil {
		t.Error("expected filesystem root to be rejected")
	}
}
