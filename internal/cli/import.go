package cli

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"

	"github.com/behzodhalil/yawl/internal/compose"
	"github.com/behzodhalil/yawl/internal/imports"
)

var importFiles = map[string]string{"render": "render.yaml", "fly": "fly.toml", "caprover": "captain-definition", "kamal": "config/deploy.yml", "dokku": ""}

func importCmd(ctx context.Context, args []string) error {
	usage := errors.New("usage: yawl import render|fly|caprover|kamal|dokku [file] [-f yawl.yaml] [--server <name>] [--project <name>] [--apply] [--yes]")
	if len(args) == 0 {
		return usage
	}
	platform := args[0]
	src, ok := importFiles[platform]
	if !ok {
		return usage
	}
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fl := importFlags(fs)
	var storage, name *string
	if platform == "dokku" {
		storage = fs.String("storage", "", "storage:list's output, saved to a file")
		name = fs.String("name", "", "the app's name (default: the folder's name)")
	}
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		src = pos[0]
	} else if src == "" {
		return errors.New("usage: yawl import dokku <config:export output> [--storage <storage:list output>] [--name <app>]")
	}
	body, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	var res compose.Result
	switch platform {
	case "render":
		res, err = imports.Render(body)
	case "fly":
		res, err = imports.Fly(body)
	case "caprover":
		res, err = imports.CapRover(body, folder())
	case "kamal":
		res, err = imports.Kamal(body)
	case "dokku":
		var st []byte
		if *storage != "" {
			if st, err = os.ReadFile(*storage); err != nil {
				return err
			}
		}
		if *name == "" {
			*name = folder()
		}
		res, err = imports.Dokku(body, st, *name)
	}
	if err != nil {
		return err
	}
	return addImport(ctx, res, filepath.Dir(src), filepath.Base(src), fl)
}
