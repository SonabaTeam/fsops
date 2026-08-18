package fsops

import "os"

type Delete struct {
	SrcPath string
	Fn      func(err error)
}

func (d *Delete) Submit() {
	go func() {
		err := os.RemoveAll(d.SrcPath)
		if d.Fn != nil {
			d.Fn(err)
		}
	}()
}
