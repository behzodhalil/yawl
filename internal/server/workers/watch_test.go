package workers

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type probes struct {
	fakeSender
	up     bool
	probed int
}

func (p *probes) Send(ctx context.Context, id string, c proto.Command) (proto.Result, error) {
	if c.Name != proto.CmdProbe {
		return p.fakeSender.Send(ctx, id, c)
	}
	p.probed++
	if p.up {
		return proto.Result{Key: c.Key, OK: true}, nil
	}
	return proto.Result{Key: c.Key, Cause: "GET / returned 502"}, nil
}

var tLive = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func liveAt(t *testing.T, st *store.Store, id string, at time.Time) {
	if _, err := st.DB.Exec(`UPDATE deploys SET finished_at = ? WHERE id = ?`, at.UnixMilli(), id); err != nil {
		t.Fatal(err)
	}
}

func ticks(t *testing.T, w *Watch, from time.Time, n int) time.Time {
	for i := 0; i < n; i++ {
		now := from.Add(time.Duration(i) * 30 * time.Second)
		w.Now = func() time.Time { return now }
		if err := w.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	return from.Add(time.Duration(n) * 30 * time.Second)
}

func sentTexts(t *testing.T, st *store.Store) []string {
	rows, _ := st.Q.ListOutbox(context.Background())
	var out []string
	for _, r := range rows {
		out = append(out, r.Body)
	}
	return out
}

func rollbacks(st *store.Store) int {
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM deploys WHERE rollback = 1`).Scan(&n)
	return n
}

func twoDeploys(t *testing.T) (*store.Store, string, string, string) {
	st, svc := seed(t, "web")
	first, _ := deployOnce(t, st, svc, &fakeSender{})
	second, _ := deployOnce(t, st, svc, &fakeSender{})
	liveAt(t, st, second, tLive)
	return st, svc, first, second
}

func TestAutoRollbackInWindow(t *testing.T) {
	st, _, first, second := twoDeploys(t)
	gw := &probes{}
	w := &Watch{St: st, GW: gw, App: app.New(st)}
	ticks(t, w, tLive.Add(30*time.Second), 3)
	if rollbacks(st) != 1 {
		t.Fatalf("%d rollbacks", rollbacks(st))
	}
	if d, _ := st.Q.GetDeploy(context.Background(), second); d.AutoRolledBack != 1 {
		t.Fatal("not marked")
	}
	if got := outbox(t, st); len(got) != 1 || got[0] != "acme-api failed 3 health checks within 5 minutes of going live; rolled back to "+first+"." {
		t.Fatalf("outbox %q", got)
	}
}

func TestAutoRollbackOnce(t *testing.T) {
	st, svc, _, _ := twoDeploys(t)
	gw := &probes{}
	w := &Watch{St: st, GW: gw, App: app.New(st)}
	at := ticks(t, w, tLive.Add(30*time.Second), 4)
	if rollbacks(st) != 1 {
		t.Fatalf("%d rollbacks after a fourth failure", rollbacks(st))
	}
	var rb string
	st.DB.QueryRow(`SELECT id FROM deploys WHERE rollback = 1`).Scan(&rb)
	if err := NewRunner(st, &fakeSender{}, nil).Run(context.Background(), rb); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.Q.GetService(context.Background(), svc); s.CurrentDeployID.String != rb {
		t.Fatal("rollback not current")
	}
	liveAt(t, st, rb, at)
	ticks(t, w, at.Add(30*time.Second), 3)
	if rollbacks(st) != 1 {
		t.Fatalf("%d rollbacks: a rollback was rolled back", rollbacks(st))
	}
	if got := outbox(t, st); len(got) != 2 || !strings.HasPrefix(got[1], "acme-api is down: GET / returned 502") {
		t.Fatalf("outbox %q", got)
	}
}

func TestNoRollbackWithoutPrev(t *testing.T) {
	st, svc := seed(t, "web")
	id, _ := deployOnce(t, st, svc, &fakeSender{})
	liveAt(t, st, id, tLive)
	w := &Watch{St: st, GW: &probes{}, App: app.New(st)}
	ticks(t, w, tLive.Add(30*time.Second), 3)
	s, _ := st.Q.GetService(context.Background(), svc)
	if rollbacks(st) != 0 || s.Health != "down" {
		t.Fatalf("rollbacks %d health %s", rollbacks(st), s.Health)
	}
	if got := outbox(t, st); len(got) != 1 || got[0] != "acme-api is down: GET / returned 502." {
		t.Fatalf("outbox %q", got)
	}
}

func TestDownAfterWindow(t *testing.T) {
	st, svc, _, _ := twoDeploys(t)
	w := &Watch{St: st, GW: &probes{}, App: app.New(st)}
	ticks(t, w, tLive.Add(6*time.Minute), 4)
	s, _ := st.Q.GetService(context.Background(), svc)
	if rollbacks(st) != 0 || s.Health != "down" || len(outbox(t, st)) != 1 {
		t.Fatalf("rollbacks %d health %s outbox %q", rollbacks(st), s.Health, outbox(t, st))
	}
}

func TestRecovered(t *testing.T) {
	st, svc, _, _ := twoDeploys(t)
	gw := &probes{}
	w := &Watch{St: st, GW: gw, App: app.New(st)}
	at := ticks(t, w, tLive.Add(6*time.Minute), 3)
	gw.up = true
	ticks(t, w, at, 2)
	s, _ := st.Q.GetService(context.Background(), svc)
	if got := outbox(t, st); len(got) != 2 || got[1] != "acme-api recovered." || s.Health != "ok" {
		t.Fatalf("outbox %q health %s", got, s.Health)
	}
}

func TestDownSurvivesRestart(t *testing.T) {
	st, svc, _, _ := twoDeploys(t)
	st.Q.SetServiceHealth(context.Background(), db.SetServiceHealthParams{Health: "down", ID: svc})
	w := &Watch{St: st, GW: &probes{}, App: app.New(st)}
	ticks(t, w, tLive.Add(6*time.Minute), 3)
	if got := outbox(t, st); len(got) != 0 {
		t.Fatalf("outbox %q", got)
	}
}

func heartbeat(t *testing.T, st *store.Store, pct int, at time.Time) {
	st.Q.TouchServer(context.Background(), db.TouchServerParams{LastHeartbeat: sql.NullInt64{Int64: at.UnixMilli(), Valid: true}, DiskUsedPct: int64(pct), UpdatedAt: 1, ID: "srv_1"})
}

func TestDiskAlerts(t *testing.T) {
	st, _ := seed(t, "web")
	w := &Watch{St: st, GW: &probes{up: true}, App: app.New(st)}
	var sent []string
	for _, pct := range []int{86, 87, 96, 79, 86} {
		heartbeat(t, st, pct, tLive)
		ticks(t, w, tLive, 1)
		sent = outbox(t, st)
	}
	want := []string{"hel1 disk is 85% full.", "hel1 disk is 95% full.", "hel1 disk is 85% full."}
	if strings.Join(sent, "|") != strings.Join(want, "|") {
		t.Fatalf("sent %q", sent)
	}
}

func TestOffline(t *testing.T) {
	st, _ := seed(t, "web")
	w := &Watch{St: st, GW: &probes{up: true}, App: app.New(st)}
	heartbeat(t, st, 10, tLive)
	ticks(t, w, tLive, 1)
	ticks(t, w, tLive.Add(2*time.Minute+time.Second), 1)
	ticks(t, w, tLive.Add(3*time.Minute), 1)
	heartbeat(t, st, 10, tLive.Add(3*time.Minute))
	ticks(t, w, tLive.Add(3*time.Minute+5*time.Second), 1)
	if got := outbox(t, st); strings.Join(got, "|") != "hel1 agent has been offline for 2 minutes.|hel1 agent is back." {
		t.Fatalf("sent %q", got)
	}
}

func TestFailsCountPerDeploy(t *testing.T) {
	st, svc, _, _ := twoDeploys(t)
	gw := &probes{}
	w := &Watch{St: st, GW: gw, App: app.New(st)}
	at := ticks(t, w, tLive.Add(6*time.Minute), 2)
	third, _ := deployOnce(t, st, svc, &fakeSender{})
	liveAt(t, st, third, at)
	ticks(t, w, at.Add(30*time.Second), 1)
	if rollbacks(st) != 0 {
		t.Fatal("one failed probe of a new deploy rolled it back")
	}
}

func TestDownAfterFailedAutoRollback(t *testing.T) {
	st, _, _, second := twoDeploys(t)
	w := &Watch{St: st, GW: &probes{}, App: app.New(st)}
	at := ticks(t, w, tLive.Add(30*time.Second), 3)
	st.DB.Exec(`UPDATE deploys SET state = 'failed' WHERE rollback = 1`)
	ticks(t, w, at, 3)
	if d, _ := st.Q.GetDeploy(context.Background(), second); d.AutoRolledBack != 1 || rollbacks(st) != 1 {
		t.Fatalf("auto %d rollbacks %d", d.AutoRolledBack, rollbacks(st))
	}
	if got := sentTexts(t, st); len(got) != 2 || !strings.HasPrefix(got[1], "acme-api is down:") {
		t.Fatalf("outbox %q", got)
	}
}

type deadAgent struct{ fakeSender }

func (d *deadAgent) Send(ctx context.Context, id string, c proto.Command) (proto.Result, error) {
	if c.Name == proto.CmdProbe {
		return proto.Result{}, context.DeadlineExceeded
	}
	return d.fakeSender.Send(ctx, id, c)
}

func TestAgentAwayIsNotAFailure(t *testing.T) {
	st, _, _, _ := twoDeploys(t)
	w := &Watch{St: st, GW: &deadAgent{}, App: app.New(st)}
	ticks(t, w, tLive.Add(30*time.Second), 4)
	if rollbacks(st) != 0 || len(sentTexts(t, st)) != 0 {
		t.Fatalf("rollbacks %d outbox %q", rollbacks(st), sentTexts(t, st))
	}
}

func TestRecoveredReroutes(t *testing.T) {
	st, _, _, _ := twoDeploys(t)
	gw := &probes{}
	w := &Watch{St: st, GW: gw, App: app.New(st)}
	at := ticks(t, w, tLive.Add(6*time.Minute), 3)
	gw.up = true
	ticks(t, w, at, 1)
	for _, c := range gw.sent {
		if c.Name == proto.CmdSwitchRoute {
			return
		}
	}
	t.Fatalf("no route sent on recovery: %v", gw.sent)
}

func TestOfflineGraceAfterRestart(t *testing.T) {
	st, _ := seed(t, "web")
	w := &Watch{St: st, GW: &probes{up: true}, App: app.New(st)}
	heartbeat(t, st, 10, tLive)
	ticks(t, w, tLive.Add(10*time.Minute), 1)
	heartbeat(t, st, 10, tLive.Add(10*time.Minute+30*time.Second))
	ticks(t, w, tLive.Add(11*time.Minute), 1)
	if got := outbox(t, st); len(got) != 0 {
		t.Fatalf("sent %q", got)
	}
	ticks(t, w, tLive.Add(13*time.Minute), 1)
	if got := outbox(t, st); strings.Join(got, "|") != "hel1 agent has been offline for 2 minutes." {
		t.Fatalf("sent %q", got)
	}
}

func TestAutoRollbackEventKeepsText(t *testing.T) {
	st, _, _, second := twoDeploys(t)
	w := &Watch{St: st, GW: &probes{}, App: app.New(st)}
	ticks(t, w, tLive.Add(30*time.Second), 3)
	var body string
	st.DB.QueryRow(`SELECT body_json FROM events WHERE kind = 'deploy.auto_rollback' AND subject_id = ?`, second).Scan(&body)
	if !strings.Contains(body, `"text":"acme-api failed 3 health checks`) {
		t.Fatalf("body %s", body)
	}
}

func TestAutoRollbackErrorRetries(t *testing.T) {
	st, svc, _, _ := twoDeploys(t)
	st.DB.Exec(`CREATE TRIGGER no_deploys BEFORE INSERT ON deploys BEGIN SELECT RAISE(ABORT, 'disk I/O error'); END`)
	w := &Watch{St: st, GW: &probes{}, App: app.New(st)}
	var err error
	for i := 0; i < 3; i++ {
		now := tLive.Add(time.Duration(i+1) * 30 * time.Second)
		w.Now = func() time.Time { return now }
		err = w.Tick(context.Background())
	}
	if s, _ := st.Q.GetService(context.Background(), svc); err == nil || s.Health == "down" || len(outbox(t, st)) != 0 {
		t.Fatalf("err %v health %s outbox %q", err, s.Health, outbox(t, st))
	}
	st.DB.Exec(`DROP TRIGGER no_deploys`)
	ticks(t, w, tLive.Add(2*time.Minute), 1)
	if rollbacks(st) != 1 {
		t.Fatalf("%d rollbacks", rollbacks(st))
	}
}

func TestFailsForgetsReplaced(t *testing.T) {
	st, svc, _, second := twoDeploys(t)
	w := &Watch{St: st, GW: &probes{}, App: app.New(st)}
	at := ticks(t, w, tLive.Add(6*time.Minute), 2)
	third, _ := deployOnce(t, st, svc, &fakeSender{})
	liveAt(t, st, third, at)
	ticks(t, w, at.Add(30*time.Second), 1)
	if _, ok := w.fails[second]; ok || len(w.fails) != 1 {
		t.Fatalf("fails %v", w.fails)
	}
}
