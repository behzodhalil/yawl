package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/seal"
	"github.com/behzodhalil/yawl/internal/spec"
)

var pollEvery = time.Second

type backupRow struct {
	ID, State, Cause string
	Size             int64
	CreatedAt        int64 `json:"created_at"`
}

func dbCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: yawl db backup|backups|restore|import <database>")
	}
	sub := args[0]
	fs := flag.NewFlagSet("db", flag.ContinueOnError)
	file := fs.String("f", "yawl.yaml", "spec file naming the project and environment")
	into := fs.String("into", "", "the new database a restore goes into")
	at := fs.String("at", "", "restore to this time instead of a backup, like 2026-10-30T17:40:00Z (PITR)")
	from := fs.String("from", "", "read the backup from this copy instead of the main storage")
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	b, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	f, err := spec.Parse(b)
	if err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("name the database")
	}
	path := fmt.Sprintf("/api/services/%s/%s/%s", f.Project, f.Environment, pos[0])
	switch {
	case sub == "backup" && len(pos) == 1:
		var out struct{ ID string }
		if err := c.do(ctx, "POST", path+"/backups", nil, &out); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Backup %s queued.\n", out.ID)
		for {
			var rows []backupRow
			if err := c.do(ctx, "GET", path+"/backups", nil, &rows); err != nil {
				return err
			}
			for _, r := range rows {
				if r.ID != out.ID {
					continue
				}
				switch r.State {
				case "ok":
					fmt.Fprintf(stdout, "Backup %s ok, %s.\n", r.ID, humanSize(r.Size))
					return nil
				case "failed":
					return fmt.Errorf("backup %s failed: %s", r.ID, r.Cause)
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pollEvery):
			}
		}

	case sub == "backups" && len(pos) == 1:
		var rows []backupRow
		if err := c.do(ctx, "GET", path+"/backups", nil, &rows); err != nil {
			return err
		}
		for _, r := range rows {
			line := fmt.Sprintf("%-22s%-9s%-10s%s", r.ID, r.State, humanSize(r.Size), time.UnixMilli(r.CreatedAt).UTC().Format("2006-01-02 15:04 UTC"))
			if r.Cause != "" {
				line += "  " + r.Cause
			}
			fmt.Fprintln(stdout, line)
		}
		var w struct {
			PITR     bool
			From, To int64
		}
		if c.do(ctx, "GET", path+"/pitr", nil, &w) == nil && w.PITR && w.From > 0 {
			f := func(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339) }
			fmt.Fprintf(stdout, "Restore to any second from %s to %s: yawl db restore %s --at <time> --into <new name>\n", f(w.From), f(w.To), pos[0])
		}
		return nil

	case sub == "import" && len(pos) == 2:
		var out struct{ Deploy string }
		var err error
		switch src := pos[1]; {
		case strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://"):
			b, _ := json.Marshal(map[string]string{"url": src})
			err = c.do(ctx, "POST", path+"/import", b, &out)
		case strings.HasPrefix(src, "server:"):
			b, _ := json.Marshal(map[string]string{"path": strings.TrimPrefix(src, "server:")})
			err = c.do(ctx, "POST", path+"/import", b, &out)
		default:
			f, ferr := os.Open(src)
			if ferr != nil {
				return ferr
			}
			defer f.Close()
			err = c.send(ctx, "PUT", path+"/import?name="+url.QueryEscape(filepath.Base(src)), f, &out)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Importing into %s; a snapshot is kept first.\n", pos[0])
		return follow(ctx, c, out.Deploy)
	case sub == "restore" && len(pos) == 2 && *at == "":
		return restoreCmd(ctx, c, path, pos[0], map[string]string{"backup": pos[1], "from": *from}, *into)
	case sub == "restore" && *at != "" && *from != "":
		return errors.New("--from reads a backup from a copy; --at reads the main storage only")
	case sub == "restore" && len(pos) == 1 && *at != "":
		return restoreCmd(ctx, c, path, pos[0], map[string]string{"at": *at}, *into)
	}
	return errors.New("usage: yawl db backup|backups <database>, yawl db restore <database> <backup id>|--at <time> --into <new name>, or yawl db import <database> <file>|<https link>|server:<name>")
}

func humanSize(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
}

func restoreCmd(ctx context.Context, c *client, path, from string, body map[string]string, into string) error {
	if into == "" {
		return errors.New("--into <new name> is required: a restore goes into a new database")
	}
	key, err := readHidden("Recovery key: ")
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if _, _, err := seal.DecodeRecovery(key); err != nil {
		return fmt.Errorf("that is not a recovery key: %w", err)
	}
	var keys struct {
		AgentPubkey string `json:"agent_pubkey"`
		Server      string `json:"server"`
	}
	if err := c.do(ctx, "GET", path+"/keys", nil, &keys); err != nil {
		return err
	}
	if keys.AgentPubkey == "" {
		return fmt.Errorf("server %s has no secrets key; re-join it", keys.Server)
	}
	box, err := sealTo(keys.AgentPubkey, key)
	if err != nil {
		return err
	}
	body["into"], body["recovery_box"] = into, box
	js, _ := json.Marshal(body)
	var out struct{ Deploy string }
	if err := c.do(ctx, "POST", path+"/restore", js, &out); err != nil {
		return err
	}
	if err := follow(ctx, c, out.Deploy); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Restored into %s. Point an app at it: yawl env set <app> DATABASE_URL --ref %s.url, then yawl deploy <app>.\n", into, into)
	return nil
}
