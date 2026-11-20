package workers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

type Statuser interface {
	Status(ctx context.Context, key, sha, state, context, description, target string) error
}

type CommitStatus struct {
	St       *store.Store
	S        Statuser
	PanelURL string
	Now      func() time.Time

	retry map[string]statusRetry
}

type statusRetry struct {
	n  int
	at time.Time
}

const statusWindow = 24 * time.Hour

func (w *CommitStatus) Tick(ctx context.Context) error {
	rows, err := w.St.Q.StatusDeploys(ctx, w.Now().Add(-statusWindow).UnixMilli())
	if err != nil {
		return err
	}
	if w.retry == nil {
		w.retry = map[string]statusRetry{}
	}
	seen := map[string]bool{}
	for _, d := range rows {
		seen[d.ID] = true
		state, desc := wantStatus(d)
		if state == "" || state == d.StatusSent || w.Now().Before(w.retry[d.ID].at) {
			continue
		}
		key, ok := spec.RepoKey(d.Repo)
		if !ok {
			continue
		}
		target := fmt.Sprintf("%s/p/%s/%s/s/%s/deploys/%s", strings.TrimRight(w.PanelURL, "/"), d.Project, d.Env, d.Name, d.ID)
		forge := "github"
		if host, _, _ := strings.Cut(key, "/"); strings.Contains(host, ".") {
			forge = "gitlab"
		}
		note := "commitstatus.refused." + forge
		err := w.S.Status(ctx, key, d.CommitSha, state, "yawl/"+d.Env+"/"+d.Name, desc, target)
		if u, ok := err.(interface{ Unconnected() bool }); ok && u.Unconnected() {
		} else if r, ok := err.(interface{ Refused() bool }); ok && r.Refused() {
			w.St.Q.SetSetting(ctx, db.SetSettingParams{Key: note, Value: key + ": " + err.Error()})
		} else if err != nil {
			r := w.retry[d.ID]
			if r.n++; r.n == 1 {
				slog.Warn("commit status", "deploy", d.ID, "err", err)
			}
			r.at = w.Now().Add(min(5*time.Second<<r.n, 10*time.Minute))
			w.retry[d.ID] = r
			continue
		} else {
			w.St.Q.DeleteSettingKey(ctx, note)
		}
		delete(w.retry, d.ID)
		if err := w.St.Q.SetStatusSent(ctx, db.SetStatusSentParams{StatusSent: state, ID: d.ID}); err != nil {
			return err
		}
	}
	for id := range w.retry {
		if !seen[id] {
			delete(w.retry, id)
		}
	}
	return nil
}

func wantStatus(d db.StatusDeploysRow) (string, string) {
	switch d.State {
	case "live":
		if d.StartedAt.Valid && d.FinishedAt.Valid {
			return "success", "Live in " + shortTook(time.Duration(d.FinishedAt.Int64-d.StartedAt.Int64)*time.Millisecond)
		}
		return "success", "Live"
	case "failed", "rolled_back":
		why, _, _ := strings.Cut(d.Cause, "\n")
		if why == "" {
			why = "Failed"
		}
		return "failure", why[:min(len(why), 140)]
	case "superseded":
		if d.StatusSent == "" {
			return "", ""
		}
		if d.Cause == "Canceled." {
			return "error", "Canceled"
		}
		return "error", "Replaced by a newer deploy"
	}
	return "pending", "Deploying"
}

func shortTook(d time.Duration) string {
	switch s := int(d / time.Second); {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm %ds", s/60, s%60)
	default:
		return fmt.Sprintf("%dh %dm", s/3600, s%3600/60)
	}
}
