package app

import (
	"context"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestRestartQueuesLiveVersion(t *testing.T) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	if _, err := a.Restart(ctx, svc, "owner"); err == nil || err.Error() != "acme-api has not been deployed yet; deploy it first." {
		t.Fatalf("nothing live: %v", err)
	}
	cur := live(t, a, svc, 1)
	id, err := a.Restart(ctx, svc, "owner")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := st.Q.GetDeploy(ctx, id)
	if d.Restart != 1 || d.Rollback != 1 || d.Image != "yawl/acme-production-api:"+cur || d.State != "queued" {
		t.Fatalf("%+v", d)
	}
}

func TestStopStart(t *testing.T) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	live(t, a, svc, 1)
	st.DB.Exec(`UPDATE services SET repo = 'https://github.com/acme/api', branch = 'main', auto_deploy = 1 WHERE id = ?`, svc)
	if err := a.Stop(ctx, svc, "owner"); err != nil {
		t.Fatal(err)
	}
	if ids, _ := a.Push(ctx, "", "acme/api", "main", "c0ffee", "behzod"); len(ids) != 0 {
		t.Fatalf("a push deployed a stopped service: %v", ids)
	}
	if rows, _ := st.Q.LiveServices(ctx); len(rows) != 0 {
		t.Fatalf("probed while stopped: %+v", rows)
	}
	st.DB.Exec(`UPDATE services SET stopped = 1 WHERE id = ?`, svc)
	id, err := a.Start(ctx, svc, "owner")
	if err != nil {
		t.Fatal(err)
	}
	var us, stopped int
	st.DB.QueryRow(`SELECT user_stopped, stopped FROM services WHERE id = ?`, svc).Scan(&us, &stopped)
	if d, _ := st.Q.GetDeploy(ctx, id); us != 0 || stopped != 1 || d.Restart != 1 {
		t.Fatalf("user_stopped %d stopped %d deploy %+v", us, stopped, d)
	}
	st.DB.Exec(`UPDATE services SET deleted_at = 1 WHERE id = ?`, svc)
	if _, err := a.Start(ctx, svc, "owner"); err == nil || err.Error() != "acme-api is in the trash; restore it first." {
		t.Fatalf("in trash: %v", err)
	}
	_ = db.Deploy{}
}

func TestStartRefusedKeepsStop(t *testing.T) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	if err := a.Stop(ctx, svc, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(ctx, svc, "owner"); err == nil {
		t.Fatal("started with nothing live")
	}
	var us int
	st.DB.QueryRow(`SELECT user_stopped FROM services WHERE id = ?`, svc).Scan(&us)
	if us != 1 {
		t.Fatal("a refused Start cleared the stop")
	}
}

func TestStopSupersedesQueued(t *testing.T) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	live(t, a, svc, 1)
	id, err := a.Deploy(ctx, svc, "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Stop(ctx, svc, "owner"); err != nil {
		t.Fatal(err)
	}
	if d, _ := st.Q.GetDeploy(ctx, id); d.State != "superseded" {
		t.Fatalf("queued deploy %s after a stop", d.State)
	}
}

func TestRestoreKeepsStopped(t *testing.T) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	live(t, a, svc, 1)
	if err := a.Stop(ctx, svc, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := a.Delete(ctx, "service", svc, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := a.RestoreFromTrash(ctx, "service", svc, "owner"); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.Q.ActiveDeploys(ctx, svc); n != 0 {
		t.Fatalf("%d deploys queued for a stopped service", n)
	}
}
