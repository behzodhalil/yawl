package workers

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func seedMoving(t *testing.T, db bool, step string) (*store.Store, *Move, *fakeSender) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ex := func(q string, args ...any) {
		t.Helper()
		if _, err := st.DB.Exec(q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	ex(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, created_at, updated_at) VALUES ('srv_1', 'hel1', '1.2.3.4', 'P1', '', '', 1, 1), ('srv_2', 'hk1', '5.6.7.8', 'P2', '', '', 1, 1)`)
	ex(`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('prj_1', 'acme', 1, 1)`)
	ex(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_s', 'prj_1', 'staging', 'srv_1', 1, 1)`)
	svcs := [][2]string{{"api", "web"}, {"web", "web"}}
	if db {
		svcs = append(svcs, [2]string{"staging-db", "postgres"})
	}
	for _, s := range svcs {
		container := "acme-staging-" + s[0] + "-1"
		if s[1] == "postgres" {
			container = ""
		}
		ex(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, current_deploy_id, created_at, updated_at)
			VALUES (?, 'env_s', ?, ?, 'https://x/y', 'main', 10000, '/', 60000, 30000, ?, 1, 1)`, "svc_"+s[0], s[0], s[1], "dep_"+s[0])
		ex(`INSERT INTO deploys (id, service_id, commit_sha, image, container, state, created_at, updated_at) VALUES (?, ?, 'c1', ?, ?, 'live', 1, 1)`,
			"dep_"+s[0], "svc_"+s[0], "yawl/"+s[0]+":dep_"+s[0], container)
	}
	ex(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, ref, created_at, updated_at) VALUES ('var_a', 'env_s', 'svc_api', 'TOKEN', 'old-a', 'r', '', 1, 1)`)
	ex(`INSERT INTO secret_sources (id, environment_id, provider, agent_box, recovery_box, created_at, updated_at) VALUES ('src_1', 'env_s', 'doppler', 'old-src', 'r', 1, 1)`)
	boxes := `{"var_a":"new-a","src_1":"new-src"}`
	if db {
		ex(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, ref, created_at, updated_at) VALUES ('var_p', 'env_s', 'svc_staging-db', 'PASSWORD', 'old-p', 'r', '', 1, 1), ('var_u', 'env_s', 'svc_staging-db', 'URL', 'old-u', 'r', '', 1, 1)`)
		boxes = `{"var_a":"new-a","var_p":"new-p","var_u":"new-u","src_1":"new-src"}`
	}
	ex(`INSERT INTO domains (id, service_id, host, dns_state, cert_state, created_at, updated_at) VALUES ('dom_1', 'svc_api', 'api.acme.uz', 'ok', 'issued', 1, 1)`)
	ex(`INSERT INTO env_moves (id, environment_id, from_server_id, to_server_id, step, state, boxes_json, lines_json, created_at, updated_at) VALUES ('mv_1', 'env_s', 'srv_1', 'srv_2', ?, 'running', ?, '[]', 100, 100)`, step, boxes)
	gw := &fakeSender{}
	a := app.New(st)
	return st, &Move{St: st, App: a, GW: gw, Now: func() time.Time { return time.UnixMilli(200) }}, gw
}

func moveRow(t *testing.T, st *store.Store) (step, state, cause string) {
	t.Helper()
	m, err := st.Q.GetMove(context.Background(), "mv_1")
	if err != nil {
		t.Fatal(err)
	}
	return m.Step, m.State, m.Cause
}

func TestMoveStop(t *testing.T) {
	st, w, gw := seedMoving(t, true, "stop")
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	var stopped []string
	for i, c := range gw.sent {
		var a proto.StopArgs
		json.Unmarshal(c.Args, &a)
		if c.Name != proto.CmdStop || gw.to[i] != "srv_1" {
			t.Fatalf("%s to %s", c.Name, gw.to[i])
		}
		stopped = append(stopped, a.Container)
	}
	m, _ := st.Q.GetMove(context.Background(), "mv_1")
	if !slices.Equal(stopped, []string{"acme-staging-api-1", "acme-staging-web-1"}) || m.Step != "backup" || !strings.Contains(m.StoppedJson, `"svc_api":"acme-staging-api-1"`) {
		t.Fatalf("%v %+v", stopped, m)
	}
}

func TestMoveBackup(t *testing.T) {
	st, w, gw := seedMoving(t, true, "backup")
	ctx := context.Background()
	w.Tick(ctx)
	var id, state string
	st.DB.QueryRow(`SELECT id, state FROM backups WHERE service_id = 'svc_staging-db'`).Scan(&id, &state)
	if step, _, _ := moveRow(t, st); id == "" || state != "queued" || step != "backup" || len(gw.sent) != 0 {
		t.Fatalf("%s %s %s %d", id, state, step, len(gw.sent))
	}
	w.Tick(ctx)
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM backups`).Scan(&n)
	st.DB.Exec(`UPDATE backups SET state = 'ok' WHERE id = ?`, id)
	w.Tick(ctx)
	if step, _, _ := moveRow(t, st); n != 1 || step != "restore" {
		t.Fatalf("backups %d, step %s", n, step)
	}
}

func TestMoveSwitchServer(t *testing.T) {
	for _, c := range []struct {
		db   bool
		next string
	}{{true, "restore"}, {false, "deploy"}} {
		st, w, _ := seedMoving(t, c.db, "switch_server")
		st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, created_at, updated_at) VALUES ('bk_1', 'svc_staging-db', 'k', 'ok', 1, 1)`)
		ctx := context.Background()
		if err := w.switchServer(ctx, mustMove(t, st)); err != nil {
			t.Fatal(err)
		}
		var server, box, dns, cert string
		st.DB.QueryRow(`SELECT server_id FROM environments WHERE id = 'env_s'`).Scan(&server)
		st.DB.QueryRow(`SELECT agent_box FROM env_vars WHERE id = 'var_a'`).Scan(&box)
		st.DB.QueryRow(`SELECT dns_state, cert_state FROM domains WHERE id = 'dom_1'`).Scan(&dns, &cert)
		var src string
		st.DB.QueryRow(`SELECT agent_box FROM secret_sources WHERE id = 'src_1'`).Scan(&src)
		if src != "new-src" {
			t.Fatalf("source box %q", src)
		}
		if step, _, _ := moveRow(t, st); server != "srv_2" || box != "new-a" || dns != "pending" || cert != "pending" || step != c.next {
			t.Fatalf("db=%v %s %s %s/%s %s", c.db, server, box, dns, cert, step)
		}
		want := `{"svc_api":"acme-staging-api-1","svc_web":"acme-staging-web-1"}`
		if c.db {
			want = `{"svc_api":"acme-staging-api-1","svc_staging-db":"yawl-acme-staging-staging-db","svc_web":"acme-staging-web-1"}`
		}
		var old string
		st.DB.QueryRow(`SELECT old_json FROM env_moves WHERE id = 'mv_1'`).Scan(&old)
		if old != want {
			t.Fatalf("db=%v old_json %s, want %s", c.db, old, want)
		}
	}
}

func TestMoveFailsBeforeSwitch(t *testing.T) {
	st, w, _ := seedMoving(t, true, "stop")
	ctx := context.Background()
	w.Tick(ctx)
	w.Tick(ctx)
	st.DB.Exec(`UPDATE backups SET state = 'failed', cause = 'bucket key expired'`)
	w.Tick(ctx)
	step, state, cause := moveRow(t, st)
	var rollbacks int
	st.DB.QueryRow(`SELECT COUNT(*) FROM deploys WHERE rollback = 1 AND state = 'queued' AND image IN ('yawl/api:dep_api', 'yawl/web:dep_web')`).Scan(&rollbacks)
	var server, text string
	st.DB.QueryRow(`SELECT server_id FROM environments WHERE id = 'env_s'`).Scan(&server)
	st.DB.QueryRow(`SELECT body FROM outbox ORDER BY created_at DESC LIMIT 1`).Scan(&text)
	if step != "backup" || state != "failed" || cause != "back up staging-db: bucket key expired" || rollbacks != 2 || server != "srv_1" ||
		text != "Moving staging to hk1 failed: back up staging-db: bucket key expired." {
		t.Fatalf("%s %s %q rollbacks=%d %s %q", step, state, cause, rollbacks, server, text)
	}
}

func TestMoveWaitsWhileAdopting(t *testing.T) {
	st, w, gw := seedMoving(t, true, "stop")
	st.DB.Exec(`INSERT INTO settings (key, value) VALUES ('adopt.pending', '1')`)
	w.Tick(context.Background())
	if len(gw.sent) != 0 {
		t.Fatal(gw.sent)
	}
}

func mustMove(t *testing.T, st *store.Store) db.EnvMove {
	t.Helper()
	m, err := st.Q.GetMove(context.Background(), "mv_1")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func switched(st *store.Store, backups, old string) {
	st.DB.Exec(`UPDATE environments SET server_id = 'srv_2' WHERE id = 'env_s'`)
	st.DB.Exec(`UPDATE env_moves SET backups_json = ?, old_json = ? WHERE id = 'mv_1'`, backups, old)
}

func moveDeploys(t *testing.T, st *store.Store) map[string][3]string {
	t.Helper()
	rows, err := st.DB.Query(`SELECT service_id, id, commit_sha, restore_from FROM deploys WHERE move_id = 'mv_1'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][3]string{}
	for rows.Next() {
		var svc, id, commit, from string
		rows.Scan(&svc, &id, &commit, &from)
		out[svc] = [3]string{id, commit, from}
	}
	return out
}

func TestMoveRestore(t *testing.T) {
	st, w, _ := seedMoving(t, true, "restore")
	switched(st, `{"svc_staging-db":"bk_m"}`, `{}`)
	st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, created_at, updated_at) VALUES ('bk_m', 'svc_staging-db', 'k', 'ok', 150, 150)`)
	w.App.KeepMoveKey("mv_1", "rk")
	ctx := context.Background()
	w.Tick(ctx)
	d := moveDeploys(t, st)["svc_staging-db"]
	if k, ok := w.App.TakeRestoreKey(d[0]); d[2] != "bk_m" || !ok || k != "rk" {
		t.Fatalf("%v %q", d, k)
	}
	w.Tick(ctx)
	if step, _, _ := moveRow(t, st); step != "restore" || len(moveDeploys(t, st)) != 1 {
		t.Fatal("restore must wait for its deploy", step)
	}
	st.DB.Exec(`UPDATE deploys SET state = 'failed', cause = 'disk full' WHERE id = ?`, d[0])
	w.Tick(ctx)
	if step, state, cause := moveRow(t, st); step != "restore" || state != "failed" || cause != "restore staging-db: disk full" {
		t.Fatal(step, state, cause)
	}
}

func TestMoveRestoreFromLastBackupWhenOldServerOffline(t *testing.T) {
	st, w, _ := seedMoving(t, true, "restore")
	switched(st, `{}`, `{}`)
	st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, created_at, updated_at) VALUES ('bk_old', 'svc_staging-db', 'k', 'ok', 5, 5), ('bk_last', 'svc_staging-db', 'k2', 'ok', 9, 9)`)
	w.App.KeepMoveKey("mv_1", "rk")
	w.Tick(context.Background())
	if d := moveDeploys(t, st)["svc_staging-db"]; d[2] != "bk_last" {
		t.Fatal(d)
	}
}

func TestMoveRestoreKeyLost(t *testing.T) {
	st, w, _ := seedMoving(t, true, "restore")
	switched(st, `{"svc_staging-db":"bk_m"}`, `{}`)
	st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, created_at, updated_at) VALUES ('bk_m', 'svc_staging-db', 'k', 'ok', 150, 150)`)
	ctx := context.Background()
	w.Tick(ctx)
	if step, state, cause := moveRow(t, st); step != "restore" || state != "failed" ||
		cause != "restore staging-db: the recovery key was not kept across a server restart; retry the move and enter it again" {
		t.Fatal(step, state, cause)
	}
	if err := w.App.RetryMove(ctx, "mv_1", "rk2", "owner"); err != nil {
		t.Fatal(err)
	}
	w.Tick(ctx)
	if d := moveDeploys(t, st)["svc_staging-db"]; d[0] == "" {
		t.Fatal("no restore deploy after retry")
	}
}

func TestMoveDeploy(t *testing.T) {
	st, w, gw := seedMoving(t, false, "deploy")
	switched(st, `{}`, `{"svc_api":"acme-staging-api-1","svc_web":"acme-staging-web-1"}`)
	ctx := context.Background()
	w.Tick(ctx)
	ds := moveDeploys(t, st)
	if len(ds) != 2 || ds["svc_api"][1] != "c1" {
		t.Fatal(ds)
	}
	r := NewRunner(st, gw, nil)
	for _, d := range ds {
		if err := r.Run(ctx, d[0]); err != nil {
			t.Fatal(err)
		}
	}
	for i, c := range gw.sent {
		if gw.to[i] != "srv_2" || strings.Contains(string(c.Args), "acme-staging-api-1") || strings.Contains(string(c.Args), "acme-staging-web-1") {
			t.Fatalf("%s to %s: %s", c.Name, gw.to[i], c.Args)
		}
	}
	gw.sent, gw.to = nil, nil
	st.DB.Exec(`UPDATE servers SET offline = 1 WHERE id = 'srv_1'`)
	w.Tick(ctx)
	if step, state, _ := moveRow(t, st); step != "clean" || state != "done" {
		t.Fatal(step, state)
	}
}

func TestMoveCarriesQueuedPush(t *testing.T) {
	st, w, _ := seedMoving(t, false, "deploy")
	switched(st, `{}`, `{}`)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_push', 'svc_api', 'c9', 'queued', 150, 150)`)
	w.Tick(context.Background())
	var state string
	st.DB.QueryRow(`SELECT state FROM deploys WHERE id = 'dep_push'`).Scan(&state)
	if d := moveDeploys(t, st)["svc_api"]; d[1] != "c9" || state != "superseded" {
		t.Fatal(d, state)
	}
}

func TestPushAfterDeployStepWaits(t *testing.T) {
	st, w, _ := seedMoving(t, false, "deploy")
	switched(st, `{}`, `{}`)
	ctx := context.Background()
	w.Tick(ctx)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_late', 'svc_api', 'c9', 'queued', 300, 300)`)
	runnable := func() []string {
		ds, _ := st.Q.RunnableDeploys(ctx)
		var ids []string
		for _, d := range ds {
			ids = append(ids, d.MoveID+"/"+d.CommitSha)
		}
		slices.Sort(ids)
		return ids
	}
	if got := runnable(); !slices.Equal(got, []string{"mv_1/c1", "mv_1/c1"}) {
		t.Fatal(got)
	}
	st.DB.Exec(`UPDATE deploys SET state = 'live' WHERE move_id = 'mv_1'`)
	if got := runnable(); len(got) != 0 {
		t.Fatal("the push ran while moving", got)
	}
	st.DB.Exec(`UPDATE env_moves SET state = 'done'`)
	if got := runnable(); !slices.Equal(got, []string{"/c9"}) {
		t.Fatal(got)
	}
}

func TestMoveCleanKeepsFreeHostAnotherClaims(t *testing.T) {
	st, w, _ := cleanSetup(t)
	st.DB.Exec(`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('prj_z', 'zeta', 1, 1)`)
	st.DB.Exec(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_z', 'prj_z', 'staging', 'srv_1', 1, 1)`)
	st.DB.Exec(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, created_at, updated_at)
		VALUES ('svc_z', 'env_z', 'web', 'web', 'https://x/y', 'main', 10000, '/', 60000, 30000, 1, 1)`)
	w.Tick(context.Background())
	var removals string
	st.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, app.RemovalKey("srv_1")).Scan(&removals)
	if removals != `["api-staging.1-2-3-4.sslip.io","api.acme.uz"]` {
		t.Fatal(removals)
	}
}

func cleanSetup(t *testing.T) (*store.Store, *Move, *fakeSender) {
	st, w, gw := seedMoving(t, true, "clean")
	switched(st, `{}`, `{"svc_api":"acme-staging-api-1","svc_web":"acme-staging-web-1","svc_staging-db":"yawl-acme-staging-staging-db"}`)
	st.DB.Exec(`UPDATE env_moves SET state = 'done', stopped_json = '{"svc_api":"acme-staging-api-1","svc_web":"acme-staging-web-1"}' WHERE id = 'mv_1'`)
	return st, w, gw
}

func TestMoveClean(t *testing.T) {
	st, w, gw := cleanSetup(t)
	w.Tick(context.Background())
	var drained []string
	for i, c := range gw.sent {
		var a proto.DrainArgs
		json.Unmarshal(c.Args, &a)
		if c.Name != proto.CmdDrain || gw.to[i] != "srv_1" {
			t.Fatalf("%s to %s", c.Name, gw.to[i])
		}
		drained = append(drained, a.Container)
	}
	slices.Sort(drained)
	var removals string
	st.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, app.RemovalKey("srv_1")).Scan(&removals)
	m, _ := st.Q.GetMove(context.Background(), "mv_1")
	if !slices.Equal(drained, []string{"acme-staging-api-1", "acme-staging-web-1", "yawl-acme-staging-staging-db"}) || m.Cleaned != 1 ||
		removals != `["api-staging.1-2-3-4.sslip.io","web-staging.1-2-3-4.sslip.io","api.acme.uz"]` {
		t.Fatalf("%v cleaned=%d %s", drained, m.Cleaned, removals)
	}
}

func TestMoveCleansWhenOldServerReturns(t *testing.T) {
	st, w, gw := cleanSetup(t)
	st.DB.Exec(`UPDATE servers SET offline = 1 WHERE id = 'srv_1'`)
	ctx := context.Background()
	w.Tick(ctx)
	if m, _ := st.Q.GetMove(ctx, "mv_1"); len(gw.sent) != 0 || m.Cleaned != 0 {
		t.Fatal(gw.sent, m.Cleaned)
	}
	st.DB.Exec(`UPDATE servers SET offline = 0 WHERE id = 'srv_1'`)
	w.Tick(ctx)
	if m, _ := st.Q.GetMove(ctx, "mv_1"); len(gw.sent) != 3 || m.Cleaned != 1 {
		t.Fatal(len(gw.sent), m.Cleaned)
	}
}

func TestMoveDoneAnnounced(t *testing.T) {
	st, w, _ := seedMoving(t, false, "deploy")
	switched(st, `{}`, `{}`)
	ctx := context.Background()
	w.Tick(ctx)
	st.DB.Exec(`UPDATE deploys SET state = 'live' WHERE move_id = 'mv_1'`)
	st.DB.Exec(`UPDATE servers SET offline = 1 WHERE id = 'srv_1'`)
	w.Tick(ctx)
	var text string
	st.DB.QueryRow(`SELECT body FROM outbox ORDER BY created_at DESC LIMIT 1`).Scan(&text)
	if text != "staging moved to hk1." {
		t.Fatal(text)
	}
}

func TestWorkersLeaveMovingEnvAlone(t *testing.T) {
	st, _, _ := seedMoving(t, true, "deploy")
	st.DB.Exec(`UPDATE domains SET dns_state = 'pending'`)
	ctx := context.Background()
	live, _ := st.Q.LiveServices(ctx)
	pg, _ := st.Q.ListDatabaseServices(ctx)
	doms, _ := st.Q.PendingDomains(ctx)
	if len(live) != 0 || len(pg) != 0 || len(doms) != 0 {
		t.Fatalf("live %d, postgres %d, domains %d", len(live), len(pg), len(doms))
	}
	st.DB.Exec(`UPDATE env_moves SET state = 'done'`)
	live, _ = st.Q.LiveServices(ctx)
	pg, _ = st.Q.ListDatabaseServices(ctx)
	doms, _ = st.Q.PendingDomains(ctx)
	if len(live) != 3 || len(pg) != 1 || len(doms) != 1 {
		t.Fatalf("after: live %d, postgres %d, domains %d", len(live), len(pg), len(doms))
	}
}

func TestRollbackIgnoresDeploysBeforeMove(t *testing.T) {
	st, w, _ := seedMoving(t, false, "clean")
	st.DB.Exec(`UPDATE env_moves SET state = 'done', cleaned = 1`)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, image, container, state, move_id, created_at, updated_at) VALUES ('dep_new', 'svc_api', 'c2', 'yawl/api:dep_new', 'c', 'live', 'mv_1', 150, 150)`)
	st.DB.Exec(`UPDATE services SET current_deploy_id = 'dep_new' WHERE id = 'svc_api'`)
	_, _, err := w.App.Rollback(context.Background(), "svc_api", "", "owner")
	if err == nil || err.Error() != "acme-api has no earlier version to roll back to." {
		t.Fatal(err)
	}
	if _, _, err := w.App.Rollback(context.Background(), "svc_api", "dep_api", "owner"); err == nil {
		t.Fatal("an explicit rollback to a deploy from before the move was accepted")
	}
}

func TestWorkersLeaveFailedMoveAlone(t *testing.T) {
	st, _, _ := seedMoving(t, true, "restore")
	st.DB.Exec(`UPDATE env_moves SET state = 'failed'`)
	st.DB.Exec(`UPDATE domains SET dns_state = 'pending'`)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_push', 'svc_api', 'c9', 'queued', 300, 300)`)
	ctx := context.Background()
	live, _ := st.Q.LiveServices(ctx)
	pg, _ := st.Q.ListDatabaseServices(ctx)
	doms, _ := st.Q.PendingDomains(ctx)
	run, _ := st.Q.RunnableDeploys(ctx)
	if len(live)+len(pg)+len(doms)+len(run) != 0 {
		t.Fatalf("live %d, postgres %d, domains %d, runnable %d", len(live), len(pg), len(doms), len(run))
	}
	st.DB.Exec(`UPDATE env_moves SET step = 'backup'`)
	if live, _ = st.Q.LiveServices(ctx); len(live) != 3 {
		t.Fatal(len(live))
	}
}

func TestPushDoesNotSupersedeMoveDeploy(t *testing.T) {
	st, w, _ := seedMoving(t, false, "deploy")
	switched(st, `{}`, `{}`)
	ctx := context.Background()
	w.Tick(ctx)
	if _, err := w.App.Deploy(ctx, "svc_api", "c9", "owner"); err != nil {
		t.Fatal(err)
	}
	var state string
	st.DB.QueryRow(`SELECT state FROM deploys WHERE move_id = 'mv_1' AND service_id = 'svc_api'`).Scan(&state)
	if state != "queued" {
		t.Fatal(state)
	}
}

func TestMoveDeployReadsPushInTx(t *testing.T) {
	st, w, _ := seedMoving(t, false, "deploy")
	switched(st, `{}`, `{}`)
	pushed := false
	w.beforeDeployTx = func() {
		if !pushed {
			pushed = true
			st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_push', 'svc_api', 'c9', 'queued', 150, 150)`)
		}
	}
	w.Tick(context.Background())
	if d := moveDeploys(t, st)["svc_api"]; d[1] != "c9" {
		t.Fatalf("deployed %v, not the push", d)
	}
}

func TestMoveRestoreAtomic(t *testing.T) {
	st, w, _ := seedMoving(t, true, "restore")
	switched(st, `{"svc_staging-db":"bk_m"}`, `{}`)
	st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, created_at, updated_at) VALUES ('bk_m', 'svc_staging-db', 'k', 'ok', 150, 150)`)
	w.App.KeepMoveKey("mv_1", "rk")
	if _, err := st.DB.Exec(`CREATE TRIGGER no_save BEFORE UPDATE ON env_moves BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	w.Tick(context.Background())
	if ds := moveDeploys(t, st); len(ds) != 0 {
		t.Fatalf("a restore deploy the move does not know: %v", ds)
	}
	if k, ok := w.App.TakeMoveKey("mv_1"); !ok || k != "rk" {
		t.Fatal("the recovery key was lost")
	}
}

func TestMoveCleanGivesUp(t *testing.T) {
	st, w, gw := cleanSetup(t)
	gw.fail = map[string]string{proto.CmdDrain: "permission denied"}
	ctx := context.Background()
	st.DB.Exec(`UPDATE env_moves SET finished_at = 150 WHERE id = 'mv_1'`)
	for range 29 {
		w.Tick(ctx)
	}
	if _, _, cause := moveRow(t, st); cause != "" {
		t.Fatalf("gave up early: %s", cause)
	}
	w.Tick(ctx)
	m, _ := st.Q.GetMove(ctx, "mv_1")
	want := "could not remove acme-staging-api-1 from hel1: permission denied; could not remove yawl-acme-staging-staging-db from hel1: permission denied; " +
		"could not remove acme-staging-web-1 from hel1: permission denied"
	if m.Cause != want || m.Cleaned != 0 || m.State != "done" {
		t.Fatalf("cause %q cleaned %d state %s", m.Cause, m.Cleaned, m.State)
	}
	sent := len(gw.sent)
	w.Tick(ctx)
	if len(gw.sent) != sent {
		t.Fatal("still retrying after giving up")
	}
	keys := map[string]bool{}
	for _, c := range gw.sent {
		if strings.HasSuffix(c.Key, "svc_api") || strings.Contains(c.Key, ":clean:svc_api:") {
			keys[c.Key] = true
		}
	}
	if len(keys) != 30 {
		t.Fatalf("%d distinct keys for 30 tries of one container", len(keys))
	}
	if m, _ := st.Q.GetMove(ctx, "mv_1"); m.FinishedAt.Int64 != 150 {
		t.Fatalf("finished_at %d, want the time the move finished", m.FinishedAt.Int64)
	}
}

func TestMoveCleanGivesUpKeepsRoutesOfOthers(t *testing.T) {
	st, w, gw := cleanSetup(t)
	gw.failKey = func(key string) bool { return strings.Contains(key, ":clean:svc_api:") }
	ctx := context.Background()
	for range 30 {
		w.Tick(ctx)
	}
	var removals string
	st.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, app.RemovalKey("srv_1")).Scan(&removals)
	m, _ := st.Q.GetMove(ctx, "mv_1")
	if m.Cause != "could not remove acme-staging-api-1 from hel1: permission denied" || m.Cleaned != 0 ||
		removals != `["web-staging.1-2-3-4.sslip.io"]` {
		t.Fatalf("cause %q cleaned %d removals %s", m.Cause, m.Cleaned, removals)
	}
}

func TestMoveCleanGivesUpOnTwoAtDifferentTicks(t *testing.T) {
	st, w, gw := cleanSetup(t)
	gw.failKey = func(key string) bool {
		return strings.Contains(key, ":clean:svc_api:") || strings.Contains(key, ":clean:svc_web:")
	}
	w.cleanFails = map[string]int{"mv_1/acme-staging-web-1": 1}
	ctx := context.Background()
	for range 40 {
		w.Tick(ctx)
	}
	m, _ := st.Q.GetMove(ctx, "mv_1")
	if !strings.Contains(m.Cause, "could not remove acme-staging-api-1") || !strings.Contains(m.Cause, "could not remove acme-staging-web-1") {
		t.Fatalf("cause %q", m.Cause)
	}
}

func TestMoveMessages(t *testing.T) {
	st, w, _ := seedMoving(t, false, "deploy")
	switched(st, `{}`, `{}`)
	ctx := context.Background()
	w.Tick(ctx)
	st.DB.Exec(`UPDATE deploys SET state = 'failed', cause = 'exit 1' WHERE id = ?`, moveDeploys(t, st)["svc_api"][0])
	w.Tick(ctx)
	if _, state, cause := moveRow(t, st); state != "failed" || cause != "api did not start on hk1: exit 1" {
		t.Fatal(state, cause)
	}
}

func TestMoveRemovesCron(t *testing.T) {
	st, w, gw := seedMoving(t, false, "stop")
	st.DB.Exec(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, current_deploy_id, schedule, created_at, updated_at)
		VALUES ('svc_report', 'env_s', 'report', 'cron', 'https://x/y', 'main', 0, '', 0, 0, 'dep_report', '0 3 * * *', 1, 1)`)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, image, container, state, created_at, updated_at) VALUES ('dep_report', 'svc_report', 'c1', 'yawl/report:1', '', 'live', 1, 1)`)
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	var names []string
	for i, c := range gw.sent {
		names = append(names, c.Name+"@"+gw.to[i])
		if c.Name == proto.CmdCronRemove {
			var a proto.CronRemoveArgs
			json.Unmarshal(c.Args, &a)
			if a.ID != "svc_report" {
				t.Fatalf("%+v", a)
			}
		}
		if c.Name == proto.CmdStop {
			var a proto.StopArgs
			json.Unmarshal(c.Args, &a)
			if a.Container == "" {
				t.Fatal("a Stop with no container")
			}
		}
	}
	if !slices.Contains(names, "CronRemove@srv_1") {
		t.Fatalf("sent %v", names)
	}
}

func TestMoveGivesDistinctPrivatePorts(t *testing.T) {
	st, w, _ := seedMoving(t, true, "switch_server")
	ex := func(q string) {
		t.Helper()
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	ex(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, private_port, created_at, updated_at)
		VALUES ('svc_cache', 'env_s', 'cache', 'valkey', '', '', 0, '', 0, 0, 15432, 1, 1)`)
	ex(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_cache', 'svc_cache', '', 'live', 1, 1)`)
	ex(`UPDATE services SET current_deploy_id = 'dep_cache' WHERE id = 'svc_cache'`)
	ex(`UPDATE services SET private_port = 15433 WHERE id = 'svc_staging-db'`)
	ex(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_p', 'prj_1', 'production', 'srv_2', 1, 1)`)
	ex(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, private_port, created_at, updated_at)
		VALUES ('svc_pdb', 'env_p', 'prod-db', 'postgres', '', '', 0, '', 0, 0, 15433, 1, 1)`)
	if err := w.switchServer(context.Background(), mustMove(t, st)); err != nil {
		t.Fatal(err)
	}
	var a, b int
	st.DB.QueryRow(`SELECT private_port FROM services WHERE id = 'svc_staging-db'`).Scan(&a)
	st.DB.QueryRow(`SELECT private_port FROM services WHERE id = 'svc_cache'`).Scan(&b)
	if a == b || a == 15433 || b == 15433 {
		t.Fatalf("ports %d and %d", a, b)
	}
}

func TestSwitchKeepsSourceURLChangedMidMove(t *testing.T) {
	st, w, _ := seedMoving(t, false, "switch_server")
	ctx := context.Background()
	st.DB.Exec(`UPDATE secret_sources SET provider = 'vault', url = 'https://vault.acme', path = 'p' WHERE id = 'src_1'`)
	if err := st.Q.UpsertSource(ctx, db.UpsertSourceParams{ID: "src_x", EnvironmentID: "env_s", Provider: "vault", Url: "https://attacker.example",
		Path: "p", AgentBox: "attacker", RecoveryBox: "r", CreatedAt: 300, UpdatedAt: 300}); err != nil {
		t.Fatal(err)
	}
	if err := w.switchServer(ctx, mustMove(t, st)); err != nil {
		t.Fatal(err)
	}
	var id, url, box string
	st.DB.QueryRow(`SELECT id, url, agent_box FROM secret_sources`).Scan(&id, &url, &box)
	if id != "src_1" || url != "https://attacker.example" || box != "new-src" {
		t.Fatalf("%s %s %s", id, url, box)
	}
}
