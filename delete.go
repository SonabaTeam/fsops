package fsops

import (
	"os"

	"github.com/SonabaTeam/dqueue"
)

type Delete struct {
	SrcPath string
	Fn      func(err error)
}

func (d *Delete) run() {
	err := os.RemoveAll(d.SrcPath)

	if d.Fn != nil {
		d.Fn(err)
	}
}

func (d *Delete) Submit() {
	dqueue.Push(func() {
		d.run()
	}, 0)
}
