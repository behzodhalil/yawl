package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

func (a *App) Push(ctx context.Context, delivery, fullName, branch, commit, pusher string) ([]string, error) {
	return a.PushVia(ctx, "GitHub", delivery, fullName, branch, commit, pusher, PushInfo{})
}

type PushInfo struct {
	Message string
	Files   []string
	Partial bool
}

var skipWords = []string{"[skip ci]", "[ci skip]", "[skip deploy]", "[skip yawl]"}

func watchMatch(glob, file string) bool {
	glob = strings.TrimPrefix(glob, "./")
	if strings.HasSuffix(glob, "/") {
		glob += "**"
	}
	var re strings.Builder
	re.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; {
		case c == '*' && i+1 < len(glob) && glob[i+1] == '*':
			i++
			if i+1 < len(glob) && glob[i+1] == '/' {
				i++
				re.WriteString("(?:.*/)?")
			} else {
				re.WriteString(".*")
			}
		case c == '*':
			re.WriteString("[^/]*")
		case c == '?':
			re.WriteString("[^/]")
		default:
			re.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	re.WriteString("$")
	m, err := regexp.MatchString(re.String(), file)
	return err == nil && m
}

func (p PushInfo) wants(watchJSON string) bool {
	var globs []string
	json.Unmarshal([]byte(watchJSON), &globs)
	if len(globs) == 0 || len(p.Files) == 0 || p.Partial {
		return true
	}
	for _, f := range p.Files {
		for _, g := range globs {
			if watchMatch(g, f) {
				return true
			}
		}
	}
	return false
}

func (a *App) PushVia(ctx context.Context, via, delivery, fullName, branch, commit, pusher string, info PushInfo) ([]string, error) {
	fullName = strings.ToLower(fullName)
	unpaid := a.unpaid(ctx)
	var ids []string
	err := a.St.Tx(ctx, func(q *db.Queries) error {
		if delivery != "" {
			if seen, err := q.DeliverySeen(ctx, delivery); err != nil || seen > 0 {
				return err
			}
		}
		skipped := func(subject, reason string) error {
			return Event(ctx, q, a.Now().UnixMilli(), "push.skipped", subject, via+" push by "+pusher,
				map[string]string{"repo": fullName, "branch": branch, "commit": commit, "delivery": delivery, "reason": reason})
		}
		for _, w := range skipWords {
			if strings.Contains(strings.ToLower(info.Message), w) {
				return skipped(fullName, "the commit message says "+w)
			}
		}
		svcs, err := q.ListAutoDeployServices(ctx)
		if err != nil {
			return err
		}
		for _, s := range svcs {
			if name, ok := spec.RepoKey(s.Repo); !ok || name != fullName || s.Branch != branch {
				continue
			}
			if !info.wants(s.WatchJson) {
				if err := skipped(s.ID, "no changed file matches its watch paths"); err != nil {
					return err
				}
				continue
			}
			latest, err := q.LatestDeployCommit(ctx, s.ID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if latest == commit {
				continue
			}
			if unpaid != nil {
				if err := skipped(s.ID, unpaid.Error()); err != nil {
					return err
				}
				continue
			}
			id, err := a.queue(ctx, q, s.ID, commit, via+" push by "+pusher, pusher, delivery)
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	return ids, err
}
