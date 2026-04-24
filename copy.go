package fsops

import (
	"io"
	"os"
	"path/filepath"

	"github.com/SonabaTeam/dqueue"
)

type Copy struct {
	SrcPath string
	NewPath string
	Fn      func(err error)
}

func (c *Copy) run() {
	err := copyPath(c.SrcPath, c.NewPath)

	if c.Fn != nil {
		c.Fn(err)
	}
}

func (c *Copy) Submit() {
	dqueue.Push(func() {
		c.run()
	}, 0)
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return copyDir(src, dst)
	}

	return copyFile(src, dst)
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, os.ModePerm)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
