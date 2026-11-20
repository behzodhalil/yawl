package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestRollbackNoTarget(t *testing.T) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	if _, _, err := a.Rollback(ctx, svc, "", "owner"); err == nil || err.Error() != "acme-api has no earlier version to roll back to." {
		t.Fatalf("no deploys: %v", err)
	}
	id, _ := a.Deploy(ctx, svc, "", "owner")
	st.Q.SetDeployImage(ctx, db.SetDeployImageParams{Image: "img:1", ID: id})
	st.Q.SetDeployState(ctx, db.SetDeployStateParams{State: "live", ID: id})
	st.Q.SetCurrentDeploy(ctx, db.SetCurrentDeployParams{CurrentDeployID: sql.NullString{String: id, Valid: true}, ID: svc})
	if _, _, err := a.Rollback(ctx, svc, "", "owner"); err == nil || err.Error() != "acme-api has no earlier version to roll back to." || !errors.Is(err, ErrNoEarlier) {
		t.Fatalf("one deploy: %v", err)
	}
}

func TestRollbackEventKeepsNote(t *testing.T) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	live(t, a, svc, 10)
	b := live(t, a, svc, 20)
	st.Q.AddDeployStep(ctx, db.AddDeployStepParams{ID: "stp_r", DeployID: b, Step: "release", StartedAt: 1, CreatedAt: 1, UpdatedAt: 1})
	st.Q.FinishDeployStep(ctx, db.FinishDeployStepParams{State: "ok", DeployID: b, Step: "release", UpdatedAt: 1})
	id, _, err := a.Rollback(ctx, svc, "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	var body string
	st.DB.QueryRow(`SELECT body_json FROM events WHERE kind = 'deploy.queued' AND subject_id = ?`, id).Scan(&body)
	if !strings.Contains(body, `"note":"The database is unchanged: `+b+` ran a release command."`) {
		t.Fatalf("body %s", body)
	}
}

func TestRollbackNoteWithoutRelease(t *testing.T) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	id, _ := a.Deploy(ctx, svc, "", "owner")
	if n := RollbackNote(ctx, st.Q, id); n != "" {
		t.Fatalf("note %q", n)
	}
}

func live(t *testing.T, a *App, svc string, at int64) string {
	ctx := context.Background()
	id, _ := a.Deploy(ctx, svc, "", "owner")
	a.St.DB.Exec(`UPDATE deploys SET image = ?, state = 'live', created_at = ? WHERE id = ?`, "yawl/acme-production-api:"+id, at, id)
	a.St.Q.SetCurrentDeploy(ctx, db.SetCurrentDeployParams{CurrentDeployID: sql.NullString{String: id, Valid: true}, ID: svc})
	return id
}

func runRollback(t *testing.T, a *App, svc, to string, at int64) string {
	ctx := context.Background()
	id, _, err := a.Rollback(ctx, svc, to, "owner")
	if err != nil {
		t.Fatal(err)
	}
	a.St.DB.Exec(`UPDATE deploys SET state = 'live', created_at = ? WHERE id = ?`, at, id)
	a.St.Q.SetCurrentDeploy(ctx, db.SetCurrentDeployParams{CurrentDeployID: sql.NullString{String: id, Valid: true}, ID: svc})
	return id
}

func TestRollbackSkipsTheBadVersion(t *testing.T) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	live(t, a, svc, 10)
	b := live(t, a, svc, 20)
	st.DB.Exec(`UPDATE deploys SET auto_rolled_back = 1 WHERE id = ?`, b)
	runRollback(t, a, svc, "", 30)
	if _, to, err := a.Rollback(ctx, svc, "", "owner"); err == nil {
		t.Fatalf("rolled back to %s, the version that was rolled away from", to)
	}
}

func TestRollbackTwiceGoesFurther(t *testing.T) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	first := live(t, a, svc, 10)
	second := live(t, a, svc, 20)
	live(t, a, svc, 30)
	runRollback(t, a, svc, "", 40)
	if _, to, err := a.Rollback(ctx, svc, "", "owner"); err != nil || to != first {
		t.Fatalf("second rollback to %s (%v), want %s (not %s)", to, err, first, second)
	}
}

func TestRollbackCopiesSubject(t *testing.T) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	first := live(t, a, svc, 10)
	st.DB.Exec(`UPDATE deploys SET subject = 'Show totals' WHERE id = ?`, first)
	live(t, a, svc, 20)
	id, _, err := a.Rollback(ctx, svc, first, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := st.Q.GetDeploy(ctx, id); d.Subject != "Show totals" || d.Actor != "owner" {
		t.Fatalf("subject %q actor %q", d.Subject, d.Actor)
	}
}

func TestRollbackOK(t *testing.T) {
	cur := db.Deploy{ID: "dep_cur", Image: "img:3", State: "live", CreatedAt: 30}
	good := db.Deploy{ID: "dep_old", Image: "img:1", State: "live", CreatedAt: 10}
	if !RollbackOK(good, cur, 5) {
		t.Fatal("an earlier live version after the move")
	}
	if RollbackOK(good, db.Deploy{}, 5) {
		t.Fatal("offered with nothing current")
	}
	for name, d := range map[string]db.Deploy{
		"failed":      {ID: "a", Image: "img:1", State: "failed", CreatedAt: 10},
		"no image":    {ID: "b", State: "live", CreatedAt: 10},
		"current":     cur,
		"same image":  {ID: "c", Image: "img:3", State: "live", CreatedAt: 10},
		"before move": {ID: "d", Image: "img:1", State: "live", CreatedAt: 4},
		"rolled from": {ID: "e", Image: "img:1", State: "live", CreatedAt: 10, AutoRolledBack: 1},
	} {
		if RollbackOK(d, cur, 5) {
			t.Errorf("%s: offered", name)
		}
	}
}

func TestRollbackSkipsSameDigest(t *testing.T) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	first := live(t, a, svc, 10)
	second := live(t, a, svc, 20)
	third := live(t, a, svc, 30)
	st.DB.Exec(`UPDATE deploys SET digest = CASE id WHEN ? THEN 'sha256:aaa' ELSE 'sha256:bbb' END WHERE id IN (?, ?, ?)`, first, first, second, third)
	cur, _ := st.Q.GetDeploy(ctx, third)
	sec, _ := st.Q.GetDeploy(ctx, second)
	if RollbackOK(sec, cur, 0) {
		t.Fatal("offered to roll back onto the running digest")
	}
	id, to, err := a.Rollback(ctx, svc, "", "owner")
	if err != nil || to != first {
		t.Fatalf("rolled back to %s (%v), want %s", to, err, first)
	}
	if d, _ := st.Q.GetDeploy(ctx, id); d.Digest != "sha256:aaa" {
		t.Fatalf("rollback deploy digest %q", d.Digest)
	}
}
