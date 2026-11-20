package workers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestUserStopOnAgent(t *testing.T) {
	st, svc := seed(t, "web")
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	a := app.New(st)
	if err := a.Stop(context.Background(), svc, "owner"); err != nil {
		t.Fatal(err)
	}
	gw.sent = nil
	w := &Trash{St: st, GW: gw, App: a, Now: time.Now}
	if err := w.StopUserStopped(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(sentArgs(gw), "\n")
	if !strings.Contains(got, "RemoveRoute api-production.") || !strings.Contains(got, "Stop new1") {
		t.Fatalf("sent:\n%s", got)
	}
	gw.sent = nil
	w.StopUserStopped(context.Background())
	if len(gw.sent) != 0 {
		t.Fatalf("stopped twice: %v", sentArgs(gw))
	}
}

func TestRestartDatabaseStopsFirst(t *testing.T) {
	st, _ := seed(t, "web")
	dbID := addDB(t, st)
	gw := &fakeSender{}
	deployOnce(t, st, dbID, gw)
	gw.sent = nil
	id, err := app.New(st).Restart(context.Background(), dbID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	r := NewRunner(st, gw, nil)
	if err := r.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if names := gw.names(); len(names) != 2 || names[0] != proto.CmdStop || names[1] != proto.CmdEnsureDatabase {
		t.Fatalf("sent %v", names)
	}
	var s proto.StopArgs
	json.Unmarshal(gw.sent[0].Args, &s)
	if s.Container != "yawl-acme-production-acme-db" {
		t.Fatalf("stop %+v", s)
	}
}

func TestDeployOfStoppedService(t *testing.T) {
	st, svc := seed(t, "web")
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	a := app.New(st)
	a.Stop(context.Background(), svc, "owner")
	w := &Trash{St: st, GW: gw, App: a, Now: time.Now}
	w.StopUserStopped(context.Background())
	id, _ := a.Deploy(context.Background(), svc, "", "system")
	if err := NewRunner(st, gw, nil).Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	var stopped int
	st.DB.QueryRow(`SELECT stopped FROM services WHERE id = ?`, svc).Scan(&stopped)
	if stopped != 0 {
		t.Fatal("live again, yet marked stopped: nothing would stop it")
	}
	gw.sent = nil
	w.StopUserStopped(context.Background())
	if !strings.Contains(strings.Join(sentArgs(gw), "\n"), "Stop ") {
		t.Fatalf("not stopped again: %v", sentArgs(gw))
	}
	if _, err := a.Restart(context.Background(), svc, "owner"); err != nil {
		t.Fatal(err)
	}
	var us int
	st.DB.QueryRow(`SELECT user_stopped FROM services WHERE id = ?`, svc).Scan(&us)
	if us != 0 {
		t.Fatal("the owner's restart left it stopped")
	}
}

func TestStoppedLeftOut(t *testing.T) {
	st, svc := seed(t, "web")
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	dbID := addDB(t, st)
	deployOnce(t, st, dbID, gw)
	ctx := context.Background()
	before, _ := st.Q.WatchedWebServices(ctx)
	dbsBefore, _ := st.Q.ListDatabaseServices(ctx)
	st.DB.Exec(`UPDATE services SET user_stopped = 1`)
	after, _ := st.Q.WatchedWebServices(ctx)
	dbsAfter, _ := st.Q.ListDatabaseServices(ctx)
	crons, _ := st.Q.LiveCronServices(ctx)
	if len(before) == 0 || len(after) != 0 || len(dbsBefore) == 0 || len(dbsAfter) != 0 || len(crons) != 0 {
		t.Fatalf("watched %d then %d, databases %d then %d", len(before), len(after), len(dbsBefore), len(dbsAfter))
	}
}

type stopDuring struct {
	probes
	st  *store.Store
	svc string
}

func (p *stopDuring) Send(ctx context.Context, id string, c proto.Command) (proto.Result, error) {
	if c.Name == proto.CmdProbe {
		p.st.DB.Exec(`UPDATE services SET user_stopped = 1 WHERE id = ?`, p.svc)
	}
	return p.probes.Send(ctx, id, c)
}

func TestStopDuringFailingProbe(t *testing.T) {
	st, svc, _, _ := twoDeploys(t)
	w := &Watch{St: st, GW: &probes{}, App: app.New(st)}
	at := ticks(t, w, tLive.Add(30*time.Second), 2)
	w.GW = &stopDuring{st: st, svc: svc}
	ticks(t, w, at, 1)
	s, _ := st.Q.GetService(context.Background(), svc)
	if rollbacks(st) != 0 || s.Health == "down" {
		t.Fatalf("rollbacks %d health %s", rollbacks(st), s.Health)
	}
}

func TestStopClosesDown(t *testing.T) {
	st, svc := seed(t, "web")
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	ctx := context.Background()
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "watch.url", Value: "https://watch.example"})
	st.DB.Exec(`UPDATE services SET health = 'down' WHERE id = ?`, svc)
	a := app.New(st)
	a.Stop(ctx, svc, "owner")
	if err := (&Trash{St: st, GW: gw, App: a, Now: time.Now}).StopUserStopped(ctx); err != nil {
		t.Fatal(err)
	}
	s, _ := st.Q.GetService(ctx, svc)
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM outbox WHERE incident = ? AND action = 'close'`, "service:"+svc).Scan(&n)
	if s.Health != "ok" || n != 1 {
		t.Fatalf("health %s, closes %d", s.Health, n)
	}
}

func TestStartFailedSwitchNoRollback(t *testing.T) {
	st, svc := seed(t, "web")
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	ctx := context.Background()
	a := app.New(st)
	a.Stop(ctx, svc, "owner")
	w := &Trash{St: st, GW: gw, App: a, Now: time.Now}
	if err := w.StopUserStopped(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := a.Start(ctx, svc, "owner")
	if err != nil {
		t.Fatal(err)
	}
	gw.sent, gw.fail = nil, map[string]string{proto.CmdDrain: "container is not running"}
	if err := NewRunner(st, gw, nil).Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	for _, c := range gw.sent {
		if strings.HasSuffix(c.Key, ":rollback") {
			t.Fatalf("routed back to the stopped container: %s", c.Args)
		}
	}
	if d, _ := st.Q.GetDeploy(ctx, id); d.State == "rolled_back" {
		t.Fatal("rolled back to a stopped version")
	}
}
