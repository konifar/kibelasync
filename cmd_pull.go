package kibelasync

import (
	"context"
	"flag"
	"io"

	"github.com/konifar/kibelasync/kibela"
)

type cmdPull struct{}

func (cp *cmdPull) name() string {
	return "pull"
}

func (cp *cmdPull) description() string {
	return "sync all markdowns"
}

func (cp *cmdPull) run(ctx context.Context, argv []string, outStream io.Writer, errStream io.Writer) error {
	fs := flag.NewFlagSet("kibelasync pull", flag.ContinueOnError)
	var (
		full   = fs.Bool("full", false, "pull every markdowns")
		dir    = fs.String("dir", "notes", "sync directory")
		folder = fs.String("folder", "", "folder in kibela")
		limit  = fs.Int("limit", 0, "sync directory")
	)
	fs.SetOutput(errStream)

	if err := fs.Parse(argv); err != nil {
		return err
	}

	ki, err := kibela.New(version)
	if err != nil {
		return err
	}
	args := fs.Args()
	if len(args) > 0 {
		for _, arg := range args {
			if err := ki.PullNote(ctx, *dir, arg); err != nil {
				return err
			}
		}
		return nil
	}

	if *full {
		return ki.PullFullNotes(ctx, *dir, *folder, *limit)
	}
	return ki.PullNotes(ctx, *dir, *folder, *limit)
}
