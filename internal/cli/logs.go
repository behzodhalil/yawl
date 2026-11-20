package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

var followEvery = 2 * time.Second

func logsCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	file := fs.String("file", "yawl.yaml", "spec file naming the project and environment")
	follow := fs.Bool("follow", false, "keep printing new lines (also -F)")
	fs.BoolVar(follow, "F", false, "keep printing new lines")
	q := fs.String("q", "", "only lines containing this text (any case)")
	n := fs.Int("n", 200, "how many of the newest lines (at most 1000)")
	errs := fs.Bool("errors", false, "only the lines the app wrote to stderr")
	for i, a := range args {
		if a == "-f" {
			args[i] = "-follow"
		}
	}
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: yawl logs <service> [-f] [-q text] [-n 200] [--errors]")
	}
	f, err := readSpec(*file)
	if err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/api/services/%s/%s/%s/logs", f.Project, f.Environment, pos[0])
	of, isFile := stdout.(*os.File)
	color := isFile && term.IsTerminal(int(of.Fd())) && os.Getenv("NO_COLOR") == ""
	var since time.Time
	for {
		v := url.Values{"n": {strconv.Itoa(*n)}}
		if *q != "" {
			v.Set("q", *q)
		}
		if *errs {
			v.Set("stream", "stderr")
		}
		if !since.IsZero() {
			v.Set("since", since.Format(time.RFC3339Nano))
		}
		var data struct {
			Lines []struct {
				Time                 time.Time
				Deploy, Text, Stream string
			}
		}
		if err := c.do(ctx, "GET", path+"?"+v.Encode(), nil, &data); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		for _, l := range data.Lines {
			text := l.Time.UTC().Format("2006-01-02 15:04:05") + "  " + l.Text
			if l.Stream == "stderr" {
				text = strings.Replace(text, "  ", "  stderr  ", 1)
				if color {
					text = "\x1b[31m" + text + "\x1b[0m"
				}
			}
			fmt.Fprintln(stdout, text)
			since = l.Time
		}
		if !*follow {
			return nil
		}
		if len(data.Lines) >= min(*n, 1000) {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(followEvery):
		}
	}
}
