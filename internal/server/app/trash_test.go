package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

var tTrash = time.Date(2026, 10, 28, 12, 2, 0, 0, time.UTC)

func trashWorld(t *testing.T) (*store.Store, *App) {
	st, _ := seed(t)
	for _, q := range []string{
		`INSERT INTO deploys (id, service_id, commit_sha, image, state, created_at, updated_at) VALUES ('dep_1','svc_1','c','img:1','live',1,1)`,
		`UPDATE services SET current_deploy_id = 'dep_1' WHERE id = 'svc_1'`,
		`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, version, backup_at, created_at, updated_at)
		 VALUES ('svc_db','env_1','acme-db','postgres','','',0,'',0,0,17,'03:00',1,1)`,
	} {
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	a := New(st)
	a.Now = func() time.Time { return tTrash }
	return st, a
}

func visible(t *testing.T, st *store.Store) []string {
	ctx := context.Background()
	var out []string
	live, _ := st.Q.LiveServices(ctx)
	for _, s := range live {
		out = append(out, "live:"+s.ID)
	}
	web, _ := st.Q.WatchedWebServices(ctx)
	for _, s := range web {
		out = append(out, "web:"+s.ID)
	}
	pg, _ := st.Q.ListDatabaseServices(ctx)
	for _, s := range pg {
		out = append(out, "pg:"+s.ID)
	}
	keys, _ := st.Q.ServiceKeys(ctx)
	for _, k := range keys {
		out = append(out, "key:"+k.ID)
	}
	tree, _ := st.Q.ListEnvironmentTree(ctx)
	for _, e := range tree {
		out = append(out, "env:"+e.Env)
	}
	if _, err := st.Q.GetServiceByName(ctx, db.GetServiceByNameParams{Project: "acme", Env: "production", Service: "api"}); err == nil {
		out = append(out, "byname:api")
	}
	return out
}

func TestDeleteHidesEverywhere(t *testing.T) {
	st, a := trashWorld(t)
	ctx := context.Background()
	before := strings.Join(visible(t, st), ",")
	if err := a.Delete(ctx, "service", "svc_1", "owner"); err != nil {
		t.Fatal(err)
	}
	after := strings.Join(visible(t, st), ",")
	for _, gone := range []string{"live:svc_1", "web:svc_1", "key:svc_1", "byname:api"} {
		if !strings.Contains(before, gone) || strings.Contains(after, gone) {
			t.Fatalf("%s: before %s after %s", gone, before, after)
		}
	}
	if _, err := st.Q.GetServiceByName(ctx, db.GetServiceByNameParams{Project: "acme", Env: "production", Service: "api"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("by name: %v", err)
	}
	items, _ := a.Trash(ctx)
	if len(items) != 1 || items[0].Label != "acme/production/api" || items[0].DeletedBy != "owner" || items[0].PurgeAt != tTrash.Add(48*time.Hour).UnixMilli() {
		t.Fatalf("%+v", items)
	}
	var text string
	st.DB.QueryRow(`SELECT body FROM outbox ORDER BY created_at DESC LIMIT 1`).Scan(&text)
	if text != "acme/production/api was deleted by owner; it is purged on 30 Oct 12:02 UTC unless restored." {
		t.Fatalf("%q", text)
	}
}

func TestDeleteCascades(t *testing.T) {
	st, a := trashWorld(t)
	ctx := context.Background()
	if err := a.Delete(ctx, "project", "prj_1", "owner"); err != nil {
		t.Fatal(err)
	}
	if got := visible(t, st); len(got) != 0 {
		t.Fatalf("still visible: %v", got)
	}
	items, _ := a.Trash(ctx)
	if len(items) != 1 || items[0].Kind != "project" || items[0].Label != "acme" {
		t.Fatalf("one entry for the project: %+v", items)
	}
	if err := a.RestoreFromTrash(ctx, "project", "prj_1", "owner"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(visible(t, st), ","); !strings.Contains(got, "live:svc_1") || !strings.Contains(got, "env:production") || !strings.Contains(got, "pg:svc_db") {
		t.Fatalf("after restore: %s", got)
	}
}

func TestRestoredIsLiveEverywhere(t *testing.T) {
	st, a := trashWorld(t)
	ctx := context.Background()
	a.Delete(ctx, "service", "svc_1", "owner")
	st.DB.Exec(`UPDATE services SET stopped = 1 WHERE id = 'svc_1'`)
	if err := a.RestoreFromTrash(ctx, "service", "svc_1", "API token ci"); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(visible(t, st), ",")
	for _, want := range []string{"live:svc_1", "web:svc_1", "key:svc_1", "byname:api"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%s missing from %s", want, got)
		}
	}
	var n, rollback int
	var image string
	st.DB.QueryRow(`SELECT count(*), max(rollback), max(image) FROM deploys WHERE service_id = 'svc_1' AND state = 'queued'`).Scan(&n, &rollback, &image)
	if n != 1 || rollback != 1 || image != "img:1" {
		t.Fatalf("queued %d rollback %d image %s", n, rollback, image)
	}
	var stopped int
	st.DB.QueryRow(`SELECT stopped FROM services WHERE id = 'svc_1'`).Scan(&stopped)
	if stopped != 0 {
		t.Fatal("still marked stopped")
	}
}

func TestDeleteTwice(t *testing.T) {
	st, a := trashWorld(t)
	ctx := context.Background()
	a.Delete(ctx, "service", "svc_1", "owner")
	a.Now = func() time.Time { return tTrash.Add(time.Hour) }
	if err := a.Delete(ctx, "service", "svc_1", "owner"); err != nil {
		t.Fatal(err)
	}
	var marks, rows int
	st.DB.QueryRow(`SELECT count(*) FROM services WHERE deleted_at = ?`, tTrash.UnixMilli()).Scan(&marks)
	st.DB.QueryRow(`SELECT count(*) FROM outbox WHERE body LIKE '%was deleted%'`).Scan(&rows)
	if marks != 1 || rows != 1 {
		t.Fatalf("marks %d rows %d", marks, rows)
	}
}

func TestApplyRefusesTrashedName(t *testing.T) {
	_, a := trashWorld(t)
	ctx := context.Background()
	a.Delete(ctx, "service", "svc_1", "owner")
	_, err := a.Apply(ctx, parse(t, "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n"), "owner", false)
	if err == nil || err.Error() != "acme/production/api is in the trash until 30 Oct 12:02 UTC; restore it, or wait." {
		t.Fatalf("%v", err)
	}
}

func TestRestoreRefusedPastPurge(t *testing.T) {
	_, a := trashWorld(t)
	ctx := context.Background()
	a.Delete(ctx, "service", "svc_1", "owner")
	a.Now = func() time.Time { return tTrash.Add(49 * time.Hour) }
	if err := a.RestoreFromTrash(ctx, "service", "svc_1", "owner"); err == nil || err.Error() != "This is being purged and can no longer be restored." {
		t.Fatalf("%v", err)
	}
}

func TestRestoreSupersedesQueued(t *testing.T) {
	st, a := trashWorld(t)
	ctx := context.Background()
	a.Delete(ctx, "service", "svc_1", "owner")
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_push','svc_1','new','queued',5,5)`)
	a.RestoreFromTrash(ctx, "service", "svc_1", "owner")
	var state string
	st.DB.QueryRow(`SELECT state FROM deploys WHERE id = 'dep_push'`).Scan(&state)
	if state != "superseded" {
		t.Fatalf("queued push: %s", state)
	}
	a.Delete(ctx, "service", "svc_1", "owner")
	auto, _ := st.Q.ListAutoDeployServices(ctx)
	for _, s := range auto {
		if s.ID == "svc_1" {
			t.Fatal("a trashed service is still auto-deployed")
		}
	}
}

func TestTrashedParentHolds(t *testing.T) {
	_, a := trashWorld(t)
	ctx := context.Background()
	a.Delete(ctx, "environment", "env_1", "owner")
	if err := a.RestoreFromTrash(ctx, "service", "svc_1", "owner"); err == nil || err.Error() != "Restore acme/production first." {
		t.Fatalf("%v", err)
	}
	a.RestoreFromTrash(ctx, "environment", "env_1", "owner")
	a.Delete(ctx, "project", "prj_1", "owner")
	_, err := a.Apply(ctx, parse(t, "project: acme\nenvironment: staging\nserver: hel1\nservices:\n  - {name: x, kind: web, repo: \"https://x/y\"}\n"), "owner", false)
	if err == nil || err.Error() != "acme is in the trash until 30 Oct 12:02 UTC; restore it, or wait." {
		t.Fatalf("%v", err)
	}
}

func TestMoveRefusedWithTrash(t *testing.T) {
	st, a := trashWorld(t)
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, created_at, updated_at) VALUES ('srv_2','hk1','5.6.7.8','P','',1,1)`)
	a.Delete(ctx, "service", "svc_db", "owner")
	_, err := a.PlanMove(ctx, "acme", "production", "hk1")
	if err == nil || err.Error() != "acme/production/acme-db is in the trash; restore it, or wait until it is purged on 30 Oct 12:02 UTC." {
		t.Fatalf("%v", err)
	}
}

func TestAdoptExtendsTrash(t *testing.T) {
	st, a := trashWorld(t)
	ctx := context.Background()
	a.Delete(ctx, "service", "svc_1", "owner")
	a.Now = func() time.Time { return tTrash.Add(40 * time.Hour) }
	if err := a.Adopt(ctx, nil, "owner"); err != nil {
		t.Fatal(err)
	}
	var purge int64
	st.DB.QueryRow(`SELECT purge_at FROM services WHERE id = 'svc_1'`).Scan(&purge)
	if purge != tTrash.Add(88*time.Hour).UnixMilli() {
		t.Fatalf("purge_at %d", purge)
	}
}

func TestTrashWaitingEnvAndProject(t *testing.T) {
	st, a := trashWorld(t)
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('prj_2', 'beta', 1, 1)`)
	st.DB.Exec(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_2', 'prj_2', 'production', 'srv_1', 1, 1)`)
	st.DB.Exec(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, created_at, updated_at)
		VALUES ('svc_b', 'env_2', 'site', 'web', '', '', 0, '', 0, 0, 1, 1)`)
	a.Delete(ctx, "environment", "env_1", "owner")
	a.Delete(ctx, "project", "prj_2", "owner")
	st.DB.Exec(`UPDATE servers SET offline = 1`)
	items, _ := a.Trash(ctx)
	if len(items) != 2 {
		t.Fatalf("%+v", items)
	}
	for _, it := range items {
		if it.Waiting != "waiting for hel1 to be online to remove its containers" {
			t.Fatalf("%s: %+v", it.Kind, it)
		}
	}
	st.DB.Exec(`UPDATE servers SET offline = 0`)
	items, _ = a.Trash(ctx)
	for _, it := range items {
		if it.Waiting != "" {
			t.Fatalf("online server: %+v", it)
		}
	}
}

func TestRestoreChecksKind(t *testing.T) {
	st, a := trashWorld(t)
	ctx := context.Background()
	a.Delete(ctx, "project", "prj_1", "owner")
	for _, k := range []string{"service", "environment", "bogus"} {
		if err := a.RestoreFromTrash(ctx, k, "prj_1", "owner"); !errors.Is(err, ErrNotInTrash) {
			t.Fatalf("%s: %v", k, err)
		}
	}
	var at sql.NullInt64
	st.DB.QueryRow(`SELECT deleted_at FROM projects WHERE id = 'prj_1'`).Scan(&at)
	if !at.Valid {
		t.Fatal("a wrong kind restored the project")
	}
	if err := a.RestoreFromTrash(ctx, "project", "prj_1", "owner"); err != nil {
		t.Fatal(err)
	}
}
