package workers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/statuspage"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/server/watch"
	"github.com/behzodhalil/yawl/internal/watchsig"
)

type Watcher struct {
	St     *store.Store
	V      *vault.Vault
	GW     Sender
	Code   func() ([]byte, error)
	CFBase string
	HTTP   *http.Client
	Now    func() time.Time
}

const expectEvery = time.Hour

func (w *Watcher) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Watcher) get(ctx context.Context, k string) string {
	v, _ := w.St.Q.GetSetting(ctx, k)
	return v
}
func (w *Watcher) set(ctx context.Context, k, v string) {
	w.St.Q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: v})
}

func (w *Watcher) Tick(ctx context.Context) error {
	q := w.St.Q
	cf, ok := watch.FromSettings(ctx, q, w.V, w.CFBase)
	if !ok {
		return nil
	}
	now := w.now()
	ms := now.UnixMilli()
	fp, key := w.redeploy(ctx, cf)
	if u := w.get(ctx, "watch.url"); u != "" && key != nil {
		c := watch.Client{URL: u, Key: key, HTTP: w.HTTP}
		if ack, err := c.Signal(ctx, "panel", 0); err != nil {
			w.set(ctx, "watch.error", "The watcher did not take the panel's signal: "+err.Error())
		} else {
			if cause := w.working(ctx, ack); cause != "" {
				w.set(ctx, "watch.error", cause)
			} else {
				w.set(ctx, "watch.ack_at", strconv.FormatInt(ms, 10))
				if fp == w.get(ctx, "watch.deployed") {
					q.DeleteSettingKey(ctx, "watch.error")
				}
			}
			if !app.Adopting(ctx, q) {
				w.agents(ctx, u, key, fp)
			}
			if err := w.expect(ctx, c, u, now); err != nil {
				slog.Warn("watcher: expectations", "err", err)
			}
			w.backups(ctx, c, u)
		}
	}
	return w.quiet(ctx, ms)
}

func (w *Watcher) working(ctx context.Context, a watch.Ack) string {
	const minute = int64(time.Minute / time.Millisecond)
	if a.LastRun == 0 {
		since, err := strconv.ParseInt(w.get(ctx, "watch.norun_since"), 10, 64)
		if err != nil {
			w.set(ctx, "watch.norun_since", strconv.FormatInt(a.Ack, 10))
			since = a.Ack
		}
		if a.Ack-since > 5*minute {
			return "The watcher's minute has not run since it was deployed"
		}
	} else {
		w.St.Q.DeleteSettingKey(ctx, "watch.norun_since")
		if age := a.Ack - a.LastRun; age > 3*minute {
			return fmt.Sprintf("The watcher's minute last ran %d minutes ago", age/minute)
		}
	}
	if a.BacklogAt > 0 && a.Ack-a.BacklogAt > 10*minute {
		return fmt.Sprintf("The watcher has not sent a message for %d minutes", (a.Ack-a.BacklogAt)/minute)
	}
	return ""
}

func (w *Watcher) redeploy(ctx context.Context, cf watch.Cloudflare) (string, []byte) {
	q := w.St.Q
	key, err := watch.Key(ctx, q, w.V)
	if err != nil {
		return "", nil
	}
	code, err := w.Code()
	if err != nil {
		w.set(ctx, "watch.error", err.Error())
		return "", key
	}
	secrets, vars, err := watch.Telegram(ctx, q, w.V)
	if err != nil {
		w.set(ctx, "watch.error", err.Error())
		return "", key
	}
	fp := watch.Fingerprint(code, key, secrets, vars)
	if fp == w.get(ctx, "watch.deployed") {
		return fp, key
	}
	if _, err := watch.Deploy(ctx, w.St, w.V, cf, code); err != nil {
		slog.Warn("watcher: deploy", "err", err)
		return fp, key
	}
	q.DeleteSettingKey(ctx, "watch.expect_sha")
	return fp, key
}

func (w *Watcher) expect(ctx context.Context, c watch.Client, url string, now time.Time) error {
	list, err := watch.Build(ctx, w.St.Q)
	if err != nil {
		return err
	}
	tz := time.Local.String()
	if tz == "Local" || tz == "" {
		tz = "UTC"
	}
	cfg := map[string]string{"watch_url": url, "tz": tz}
	if _, hosts := app.PanelHosts(ctx, w.St.Q, ""); len(hosts) > 0 {
		cfg["panel_url"] = "https://" + hosts[len(hosts)-1]
	}
	page, err := statuspage.WatcherConfig(ctx, w.St.DB, now)
	if err != nil {
		return err
	}
	cfg["status"] = page
	chats, err := watch.Chats(ctx, w.St.Q, w.V, list)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(chats)
	cfg["chats"] = string(b)
	b, _ = json.Marshal([]any{cfg, list})
	sum := sha256.Sum256(b)
	sha := hex.EncodeToString(sum[:])
	at, _ := strconv.ParseInt(w.get(ctx, "watch.expect_at"), 10, 64)
	reset := w.get(ctx, "watch.reset") == "1"
	if !reset && sha == w.get(ctx, "watch.expect_sha") && now.Sub(time.UnixMilli(at)) < expectEvery {
		return nil
	}
	if err := c.Expect(ctx, cfg, list, reset); err != nil {
		return err
	}
	if reset {
		w.St.Q.DeleteSettingKey(ctx, "watch.reset")
	}
	w.set(ctx, "watch.expect_sha", sha)
	w.set(ctx, "watch.expect_at", strconv.FormatInt(now.UnixMilli(), 10))
	return nil
}

func (w *Watcher) backups(ctx context.Context, c watch.Client, url string) {
	dbs, err := w.St.Q.ListDatabaseServices(ctx)
	if err != nil {
		return
	}
	for _, d := range dbs {
		b, err := w.St.Q.LatestOKBackup(ctx, d.ID)
		if err != nil || !b.FinishedAt.Valid {
			continue
		}
		k, mark := "watch.backup."+d.ID, url+"|"+strconv.FormatInt(b.FinishedAt.Int64, 10)
		if w.get(ctx, k) == mark {
			continue
		}
		if _, err := c.Signal(ctx, "backup:"+d.ID, b.FinishedAt.Int64); err == nil {
			w.set(ctx, k, mark)
		}
	}
}

func (w *Watcher) agents(ctx context.Context, url string, key []byte, fp string) {
	servers, err := w.St.Q.ListServers(ctx)
	if err != nil {
		return
	}
	for _, s := range servers {
		k := "watch.agent." + s.ID
		if s.Offline == 1 || w.get(ctx, k) == watch.AgentMarker(fp, url) {
			continue
		}
		cmd, _ := proto.NewCommand(fmt.Sprintf("watch:%s:%d", s.ID, w.now().UnixMilli()), proto.CmdWatch,
			proto.WatchArgs{URL: url, Key: hex.EncodeToString(watchsig.AgentKey(key, s.ID))})
		sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		res, err := w.GW.Send(sctx, s.ID, cmd)
		cancel()
		if err != nil || !res.OK {
			slog.Warn("watcher: agent not watched", "server", s.Name, "err", err, "cause", res.Cause)
			continue
		}
		w.set(ctx, k, watch.AgentMarker(fp, url))
	}
}

func (w *Watcher) quiet(ctx context.Context, ms int64) error {
	if w.get(ctx, "watch.url") == "" {
		return nil
	}
	since, err := strconv.ParseInt(w.get(ctx, "watch.wait_since"), 10, 64)
	if err != nil {
		w.set(ctx, "watch.wait_since", strconv.FormatInt(ms, 10))
		since = ms
	}
	ack, _ := strconv.ParseInt(w.get(ctx, "watch.ack_at"), 10, 64)
	down := ms-max(ack, since) >= watch.AnswerAfter.Milliseconds()
	sent := w.get(ctx, "watch.down_sent") == "1"
	switch {
	case down && !sent:
		cause := strings.TrimSuffix(w.get(ctx, "watch.error"), ".")
		if cause == "" {
			cause = "it has not answered for 5 minutes"
		}
		return w.St.Tx(ctx, func(q *db.Queries) error {
			if err := q.SetSetting(ctx, db.SetSettingParams{Key: "watch.down_sent", Value: "1"}); err != nil {
				return err
			}
			return announce(ctx, q, ms, "watch.down", "watch", map[string]string{"cause": cause},
				app.WatcherDown(cause, "The watcher is not answering: "+cause+". Alerts come from the panel until it does."))
		})
	case !down && sent:
		return w.St.Tx(ctx, func(q *db.Queries) error {
			if err := q.DeleteSettingKey(ctx, "watch.down_sent"); err != nil {
				return err
			}
			return announce(ctx, q, ms, "watch.up", "watch", nil, app.WatcherUp("The watcher is answering again."))
		})
	}
	return nil
}
