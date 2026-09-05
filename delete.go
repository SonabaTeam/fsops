package fsops

import (
	"errors"
	"os"
	"path/filepath"
)

type Delete struct {
	SrcPath  string
	Callback func(err error)
}

func (d *Delete) Submit() {
	go func() {
		globalLimiter.acquire()
		defer globalLimiter.release()

		err := d.run()
		if d.Callback != nil {
			d.Callback(err)
		}
	}()
}

func (d *Delete) run() error {
	if err := validateDeletePath(d.SrcPath); err != nil {
		return err
	}
	return os.RemoveAll(d.SrcPath)
}

func validateDeletePath(path string) error {
	if path == "" {
		return errors.New("fsops: SrcPath must not be empty")
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	cleaned := filepath.Clean(abs)

	root := filepath.VolumeName(cleaned) + string(filepath.Separator)
	if cleaned == root || cleaned == string(filepath.Separator) {
		return errors.New("fsops: refusing to delete filesystem root")
	}

	return nil
}
