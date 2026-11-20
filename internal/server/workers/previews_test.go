package workers

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
)

var tPR = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func prWorld(t *testing.T) (*store.Store, *Previews, *time.Time) {
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
	ex(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, created_at, updated_at) VALUES ('srv_1','hel1','1.2.3.4','A','','',1,1)`)
	ex(`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('prj_1','acme',1,1)`)
	ex(`INSERT INTO environments (id, project_id, name, server_id, preview, created_at, updated_at) VALUES ('env_s','prj_1','staging','srv_1',1,1,1)`)
	svc := `INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, release_json, schedule, version, created_at, updated_at)
		VALUES (?, 'env_s', ?, ?, ?, 'main', ?, '/', 60000, 30000, ?, ?, ?, 1, 1)`
	ex(svc, "svc_api", "api", "web", "https://github.com/acme/api", 10000, `["./migrate"]`, "", 0)
	ex(svc, "svc_jobs", "jobs", "worker", "https://github.com/acme/jobs", 0, `[]`, "", 0)
	ex(svc, "svc_cron", "report", "cron", "https://github.com/acme/api", 0, `[]`, "0 3 * * *", 0)
	ex(svc, "svc_db", "acme-db", "postgres", "", 0, `[]`, "", 17)
	ex(`INSERT INTO domains (id, service_id, host, dns_state, cert_state, created_at, updated_at) VALUES ('dom_1','svc_api','api.acme.uz','ok','issued',1,1)`)
	ex(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, ref, build, created_at, updated_at) VALUES
		('v1','env_s','','SENTRY_DSN','sb','sr','',0,1,1), ('v2','env_s','svc_api','DATABASE_URL','','','acme-db.url',0,1,1),
		('v3','env_s','svc_api','NPM_TOKEN','nb','nr','',1,1,1), ('v4','env_s','svc_db','PASSWORD','pb','pr','',0,1,1), ('v5','env_s','svc_db','URL','ub','ur','',0,1,1)`)
	ex(`INSERT INTO settings (key, value) VALUES ('recovery.pubkey', 'cmVjb3Zlcnk=')`)
	addPR(t, st, "pre_12", 12, "waiting", tPR.UnixMilli())
	now := tPR
	a := app.New(st)
	a.Now = func() time.Time { return now }
	return st, &Previews{St: st, App: a, Now: func() time.Time { return now }}, &now
}

func addPR(t *testing.T, st *store.Store, id string, n int, state string, push int64) {
	if _, err := st.DB.Exec(`INSERT INTO previews (id, project_id, source_env_id, number, repo, title, branch, head_sha, state, last_push_at, created_at, updated_at)
		VALUES (?, 'prj_1', 'env_s', ?, 'acme/api', 'Faster search', 'search', 'abc1234', ?, ?, ?, ?)`, id, n, state, push, push, push); err != nil {
		t.Fatal(err)
	}
}

func prState(st *store.Store, id string) (state, env string) {
	st.DB.QueryRow(`SELECT state, env_id FROM previews WHERE id = ?`, id).Scan(&state, &env)
	return
}

func queuedFor(st *store.Store, env string) map[string]int {
	rows, _ := st.DB.Query(`SELECT s.name FROM deploys d JOIN services s ON s.id = d.service_id WHERE s.environment_id = ? AND d.state = 'queued'`, env)
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var n string
		rows.Scan(&n)
		out[n]++
	}
	return out
}

func TestPreviewMakesEnvironment(t *testing.T) {
	st, w, _ := prWorld(t)
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, env := prState(st, "pre_12")
	var name, server, of string
	st.DB.QueryRow(`SELECT name, server_id, preview_of FROM environments WHERE id = ?`, env).Scan(&name, &server, &of)
	if state != "making" || name != "pr-12" || server != "srv_1" || of != "env_s" {
		t.Fatal(state, name, server, of)
	}
	branches := map[string]string{}
	rows, _ := st.DB.Query(`SELECT name, branch FROM services WHERE environment_id = ?`, env)
	for rows.Next() {
		var n, b string
		rows.Scan(&n, &b)
		branches[n] = b
	}
	rows.Close()
	var domains int
	st.DB.QueryRow(`SELECT count(*) FROM domains d JOIN services s ON s.id = d.service_id WHERE s.environment_id = ?`, env).Scan(&domains)
	if len(branches) != 3 || branches["api"] != "search" || branches["jobs"] != "main" || branches["acme-db"] == "x" || domains != 0 {
		t.Fatalf("%v domains %d", branches, domains)
	}
}

func TestPreviewKeepsReleaseCommand(t *testing.T) {
	st, w, _ := prWorld(t)
	w.Tick(context.Background())
	_, env := prState(st, "pre_12")
	var release string
	st.DB.QueryRow(`SELECT release_json FROM services WHERE environment_id = ? AND name = 'api'`, env).Scan(&release)
	if release != `["./migrate"]` {
		t.Fatalf("release %q", release)
	}
}

func TestPreviewCopiesBoxesAndRefs(t *testing.T) {
	st, w, _ := prWorld(t)
	var src string
	st.DB.QueryRow(`SELECT source_env_id FROM previews WHERE id = 'pre_12'`).Scan(&src)
	st.DB.Exec(`INSERT INTO secret_sources (id, environment_id, provider, agent_box, recovery_box, created_at, updated_at) VALUES ('src_1', ?, 'doppler', 'db', 'dr', 1, 1)`, src)
	w.Tick(context.Background())
	_, env := prState(st, "pre_12")
	var box string
	st.DB.QueryRow(`SELECT agent_box || '|' || recovery_box FROM secret_sources WHERE environment_id = ?`, env).Scan(&box)
	if box != "db|dr" {
		t.Fatalf("sources %q", box)
	}
	got := map[string]string{}
	rows, _ := st.DB.Query(`SELECT v.key, v.agent_box || '|' || v.recovery_box || '|' || v.ref || '|' || v.build FROM env_vars v WHERE v.environment_id = ?`, env)
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		got[k] = v
	}
	rows.Close()
	if len(got) != 3 || got["SENTRY_DSN"] != "sb|sr||0" || got["DATABASE_URL"] != "||acme-db.url|0" || got["NPM_TOKEN"] != "nb|nr||1" {
		t.Fatalf("%v", got)
	}
}

func TestPreviewDatabasesFirst(t *testing.T) {
	st, w, _ := prWorld(t)
	ctx := context.Background()
	w.Tick(ctx)
	_, env := prState(st, "pre_12")
	if q := queuedFor(st, env); len(q) != 1 || q["acme-db"] != 1 {
		t.Fatalf("%v", q)
	}
	w.Tick(ctx)
	if q := queuedFor(st, env); len(q) != 1 {
		t.Fatalf("apps queued before the database is live: %v", q)
	}
	st.DB.Exec(`UPDATE deploys SET state = 'live' WHERE state = 'queued'`)
	st.DB.Exec(`UPDATE services SET current_deploy_id = (SELECT id FROM deploys WHERE service_id = services.id) WHERE environment_id = ? AND kind = 'postgres'`, env)
	w.Tick(ctx)
	if q := queuedFor(st, env); q["api"] != 1 || q["jobs"] != 1 {
		t.Fatalf("%v", q)
	}
	if state, _ := prState(st, "pre_12"); state != "live" {
		t.Fatal(state)
	}
	w.Tick(ctx)
	if q := queuedFor(st, env); q["api"] != 1 {
		t.Fatalf("queued twice: %v", q)
	}
}

func TestSixthPreviewWaits(t *testing.T) {
	st, w, _ := prWorld(t)
	st.DB.Exec(`UPDATE previews SET number = 6, id = 'pre_6' WHERE id = 'pre_12'`)
	for i := 1; i <= 5; i++ {
		addPR(t, st, "pre_"+string(rune('0'+i)), i, "live", tPR.UnixMilli())
	}
	w.Tick(context.Background())
	if state, _ := prState(st, "pre_6"); state != "waiting" {
		t.Fatal(state)
	}
	st.DB.Exec(`UPDATE previews SET state = 'closing' WHERE id = 'pre_1'`)
	w.Tick(context.Background())
	if state, _ := prState(st, "pre_6"); state != "making" {
		t.Fatal(state)
	}
}

func envGone(st *store.Store, env string) bool {
	var marked, live int
	st.DB.QueryRow(`SELECT count(*) FROM environments WHERE id = ? AND deleted_at IS NOT NULL AND purge_at IS NOT NULL`, env).Scan(&marked)
	st.DB.QueryRow(`SELECT count(*) FROM services WHERE environment_id = ? AND deleted_at IS NULL`, env).Scan(&live)
	return marked == 1 && live == 0
}

func TestPreviewRemovedOnClose(t *testing.T) {
	st, w, now := prWorld(t)
	w.Tick(context.Background())
	_, env := prState(st, "pre_12")
	st.DB.Exec(`UPDATE previews SET state = 'closing'`)
	w.Tick(context.Background())
	var purgeAt int64
	st.DB.QueryRow(`SELECT purge_at FROM environments WHERE id = ?`, env).Scan(&purgeAt)
	if state, _ := prState(st, "pre_12"); state != "closed" || !envGone(st, env) || purgeAt > now.UnixMilli() {
		t.Fatal(state, purgeAt)
	}
}

func TestPreviewIdleSevenDays(t *testing.T) {
	st, w, now := prWorld(t)
	w.Tick(context.Background())
	_, env := prState(st, "pre_12")
	*now = now.Add(7*24*time.Hour + time.Minute)
	w.Tick(context.Background())
	if state, _ := prState(st, "pre_12"); state != "closed" || !envGone(st, env) {
		t.Fatal(state)
	}
}

func TestCloseWhileMaking(t *testing.T) {
	st, w, _ := prWorld(t)
	ctx := context.Background()
	w.Tick(ctx)
	_, env := prState(st, "pre_12")
	st.DB.Exec(`UPDATE previews SET state = 'closing'`)
	w.Tick(ctx)
	st.DB.Exec(`UPDATE deploys SET state = 'live'`)
	w.Tick(ctx)
	if q := queuedFor(st, env); q["api"] != 0 || !envGone(st, env) {
		t.Fatalf("%v", q)
	}
}

func table(n int, rows ...string) string {
	return fmt.Sprintf("**Yawl** · acme / pr-%d\n\n| | |\n|---|---|\n", n) + strings.Join(rows, "\n")
}

type fakePR struct {
	bodies []string
	ids    []int64
}

func (f *fakePR) Comment(_ context.Context, repo string, number, id int64, body string) (int64, error) {
	f.bodies, f.ids = append(f.bodies, body), append(f.ids, id)
	if id == 0 {
		return 99, nil
	}
	return id, nil
}

func TestPreviewCommentLifecycle(t *testing.T) {
	st, w, _ := prWorld(t)
	gh := &fakePR{}
	w.GH, w.PanelURL = gh, "https://panel.acme.uz/"
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		addPR(t, st, "pre_"+string(rune('0'+i)), 100+i, "live", tPR.UnixMilli())
	}
	st.DB.Exec(`UPDATE previews SET comment = 'x', comment_id = 1 WHERE id <> 'pre_12'`)
	w.Tick(ctx)
	if len(gh.bodies) != 1 || gh.bodies[0] != table(12, "| Preview | ⏳ pr-12 waits: 5 previews are running. It starts when one closes. |") || gh.ids[0] != 0 {
		t.Fatalf("%q", gh.bodies)
	}
	w.Tick(ctx)
	st.DB.Exec(`DELETE FROM previews WHERE id <> 'pre_12'`)
	w.Tick(ctx)
	_, env := prState(st, "pre_12")
	st.DB.Exec(`UPDATE deploys SET state = 'live' WHERE state = 'queued'`)
	st.DB.Exec(`UPDATE services SET current_deploy_id = (SELECT id FROM deploys WHERE service_id = services.id) WHERE environment_id = ?`, env)
	w.Tick(ctx)
	st.DB.Exec(`UPDATE deploys SET state = 'live', commit_sha = 'abc1234ffff' WHERE state = 'queued'`)
	st.DB.Exec(`UPDATE services SET current_deploy_id = (SELECT id FROM deploys WHERE service_id = services.id ORDER BY created_at DESC LIMIT 1) WHERE environment_id = ?`, env)
	w.Tick(ctx)
	if last := gh.bodies[len(gh.bodies)-1]; last != table(12, "| Preview | ✅ [api-pr-12.1-2-3-4.sslip.io](https://api-pr-12.1-2-3-4.sslip.io) |", "| Commit | `abc1234` |", "| Logs | [acme / pr-12 / api](https://panel.acme.uz/p/acme/pr-12/s/api) |") || gh.ids[len(gh.ids)-1] != 99 {
		t.Fatalf("%q %v", gh.bodies, gh.ids)
	}
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, cause, created_at, updated_at) SELECT 'dep_bad', id, 'bad5678beef', 'failed', 'Health check failed', 9999999999999, 1 FROM services WHERE environment_id = ? AND name = 'api'`, env)
	w.Tick(ctx)
	if last := gh.bodies[len(gh.bodies)-1]; last != table(12, "| Preview | ❌ Deploy of `bad5678` failed: Health check failed |", "| Logs | [acme / pr-12 / api](https://panel.acme.uz/p/acme/pr-12/s/api) |") {
		t.Fatalf("%q", last)
	}
	st.DB.Exec(`UPDATE previews SET state = 'closing'`)
	w.Tick(ctx)
	if last := gh.bodies[len(gh.bodies)-1]; last != table(12, "| Preview | 🗑 Removed |") || gh.ids[len(gh.ids)-1] != 99 {
		t.Fatalf("%q", gh.bodies)
	}
	n := len(gh.bodies)
	w.Tick(ctx)
	if len(gh.bodies) != n {
		t.Fatal("removed said twice")
	}
}

func TestForkPreviewComment(t *testing.T) {
	st, w, _ := prWorld(t)
	gh := &fakePR{}
	w.GH = gh
	st.DB.Exec(`UPDATE previews SET state = 'fork'`)
	w.Tick(context.Background())
	w.Tick(context.Background())
	if len(gh.bodies) != 1 || gh.bodies[0] != table(12, "| Preview | 🚫 Previews are not made for changes from forks: their code would run on this server. |") {
		t.Fatalf("%q", gh.bodies)
	}
}

func TestPreviewsQuietNotWatchedNorBackedUp(t *testing.T) {
	st, w, _ := prWorld(t)
	w.Tick(context.Background())
	_, env := prState(st, "pre_12")
	var dbID string
	st.DB.QueryRow(`SELECT id FROM services WHERE environment_id = ? AND kind = 'postgres'`, env).Scan(&dbID)
	var dep string
	st.DB.QueryRow(`SELECT id FROM deploys WHERE service_id = ?`, dbID).Scan(&dep)
	r := NewRunner(st, &fakeSender{fail: map[string]string{"EnsureDatabase": "disk full"}}, nil)
	r.Run(context.Background(), dep)
	if got := outbox(t, st); len(got) != 0 {
		t.Fatalf("%q", got)
	}
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) SELECT 'dep_w', id, 'c', 'live', 1, 1 FROM services WHERE environment_id = ? AND name = 'api'`, env)
	st.DB.Exec(`UPDATE services SET current_deploy_id = 'dep_w' WHERE environment_id = ? AND name = 'api'`, env)
	if web, _ := st.Q.WatchedWebServices(context.Background()); len(web) != 0 {
		t.Fatalf("a preview is watched: %+v", web)
	}
	pg, _ := st.Q.ListDatabaseServices(context.Background())
	for _, d := range pg {
		if d.ID == dbID {
			t.Fatal("a preview's database is backed up")
		}
	}
}

func TestPreviewPurgedByTrash(t *testing.T) {
	st, w, now := prWorld(t)
	ctx := context.Background()
	w.Tick(ctx)
	_, env := prState(st, "pre_12")
	st.DB.Exec(`UPDATE previews SET state = 'closing'`)
	w.Tick(ctx)
	tr := &Trash{St: st, GW: &fakeSender{}, App: w.App, Now: func() time.Time { return *now }}
	for i := 0; i < 3; i++ {
		if err := tr.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var svcs, envs int
	st.DB.QueryRow(`SELECT count(*) FROM services WHERE environment_id = ?`, env).Scan(&svcs)
	st.DB.QueryRow(`SELECT count(*) FROM environments WHERE id = ?`, env).Scan(&envs)
	if svcs != 0 || envs != 0 {
		t.Fatalf("services %d, environment %d left", svcs, envs)
	}
	if got := outbox(t, st); len(got) != 0 {
		t.Fatalf("the purge spoke: %q", got)
	}
}

func TestPreviewPurgeWaitsForDeploy(t *testing.T) {
	st, w, now := prWorld(t)
	ctx := context.Background()
	w.Tick(ctx)
	_, env := prState(st, "pre_12")
	st.DB.Exec(`UPDATE deploys SET state = 'starting' WHERE service_id IN (SELECT id FROM services WHERE environment_id = ?)`, env)
	st.DB.Exec(`UPDATE previews SET state = 'closing'`)
	w.Tick(ctx)
	tr := &Trash{St: st, GW: &fakeSender{}, App: w.App, Now: func() time.Time { return *now }}
	tr.Tick(ctx)
	tr.Tick(ctx)
	var svcs int
	st.DB.QueryRow(`SELECT count(*) FROM services WHERE environment_id = ?`, env).Scan(&svcs)
	if svcs == 0 {
		t.Fatal("purged while its deploy was still starting")
	}
	st.DB.Exec(`UPDATE deploys SET state = 'failed' WHERE state = 'starting'`)
	tr.Tick(ctx)
	tr.Tick(ctx)
	st.DB.QueryRow(`SELECT count(*) FROM services WHERE environment_id = ?`, env).Scan(&svcs)
	if svcs != 0 {
		t.Fatalf("%d services left after the deploy ended", svcs)
	}
}

func TestPurgeSourceWithPreviews(t *testing.T) {
	st, w, now := prWorld(t)
	ctx := context.Background()
	w.Tick(ctx)
	st.DB.Exec(`UPDATE previews SET state = 'closing'`)
	w.Tick(ctx)
	past := now.Add(-time.Hour).UnixMilli()
	st.DB.Exec(`UPDATE services SET deleted_at = ?, purge_at = ? WHERE deleted_at IS NULL`, past, past)
	st.DB.Exec(`UPDATE environments SET deleted_at = ?, purge_at = ? WHERE deleted_at IS NULL`, past, past)
	st.DB.Exec(`UPDATE projects SET deleted_at = ?, purge_at = ?`, past, past)
	st.DB.Exec(`UPDATE deploys SET state = 'failed'`)
	tr := &Trash{St: st, GW: &fakeSender{}, App: w.App, Now: func() time.Time { return *now }}
	for i := 0; i < 3; i++ {
		if err := tr.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var envs, prjs int
	st.DB.QueryRow(`SELECT count(*) FROM environments`).Scan(&envs)
	st.DB.QueryRow(`SELECT count(*) FROM projects`).Scan(&prjs)
	if envs != 0 || prjs != 0 {
		t.Fatalf("environments %d, projects %d left", envs, prjs)
	}
}

func TestPreviewSourceGoneNotMade(t *testing.T) {
	for _, q := range []string{`UPDATE environments SET deleted_at = 1, purge_at = 9999999999999 WHERE id = 'env_s'`, `UPDATE environments SET preview = 0 WHERE id = 'env_s'`} {
		st, w, _ := prWorld(t)
		st.DB.Exec(q)
		w.Tick(context.Background())
		var n int
		st.DB.QueryRow(`SELECT count(*) FROM environments WHERE name = 'pr-12'`).Scan(&n)
		if state, _ := prState(st, "pre_12"); state != "closed" || n != 0 {
			t.Fatalf("%s: %s, %d", q, state, n)
		}
	}
}

func TestReopenWhilePurging(t *testing.T) {
	st, w, _ := prWorld(t)
	ctx := context.Background()
	w.Tick(ctx)
	_, old := prState(st, "pre_12")
	st.DB.Exec(`UPDATE previews SET state = 'closing'`)
	w.Tick(ctx)
	st.DB.Exec(`UPDATE previews SET state = 'waiting'`)
	w.Tick(ctx)
	var live int
	st.DB.QueryRow(`SELECT count(*) FROM services WHERE environment_id = ? AND deleted_at IS NULL`, old).Scan(&live)
	if state, _ := prState(st, "pre_12"); state != "waiting" || live != 0 {
		t.Fatalf("%s, %d services brought back", state, live)
	}
	st.DB.Exec(`DELETE FROM env_vars WHERE environment_id = ?`, old)
	st.DB.Exec(`DELETE FROM deploys WHERE service_id IN (SELECT id FROM services WHERE environment_id = ?)`, old)
	st.DB.Exec(`DELETE FROM services WHERE environment_id = ?`, old)
	st.DB.Exec(`DELETE FROM environments WHERE id = ?`, old)
	w.Tick(ctx)
	if state, env := prState(st, "pre_12"); state != "making" || env == old {
		t.Fatal(state, env)
	}
}

func TestPreviewDatabaseFailureSaidOnPR(t *testing.T) {
	st, w, now := prWorld(t)
	gh := &fakePR{}
	w.GH = gh
	ctx := context.Background()
	w.Tick(ctx)
	_, env := prState(st, "pre_12")
	st.DB.Exec(`UPDATE deploys SET state = 'failed', cause = 'disk full' WHERE service_id IN (SELECT id FROM services WHERE environment_id = ? AND kind = 'postgres')`, env)
	w.Tick(ctx)
	if state, _ := prState(st, "pre_12"); state != "failed" || len(gh.bodies) == 0 || gh.bodies[len(gh.bodies)-1] != table(12, "| Preview | ❌ acme-db failed: disk full |") {
		t.Fatalf("%s %q", state, gh.bodies)
	}
	if n, _ := st.Q.RunningPreviews(ctx, "prj_1"); n != 0 {
		t.Fatalf("a failed preview holds a slot: %d", n)
	}
	*now = now.Add(time.Minute)
	if err := w.App.RenewPreviews(ctx, "acme/api", "search", "def5678", ""); err != nil {
		t.Fatal(err)
	}
	w.Tick(ctx)
	if state, _ := prState(st, "pre_12"); state != "making" || queuedFor(st, env)["acme-db"] != 1 {
		t.Fatalf("%s %v", state, queuedFor(st, env))
	}
}

func TestPreviewVolumesNotBackedUp(t *testing.T) {
	st, w, _ := prWorld(t)
	st.DB.Exec(`UPDATE services SET volumes_json = '[{"Name":"uploads","Path":"/u"}]', backup_at = '03:30' WHERE id = 'svc_api'`)
	w.Tick(context.Background())
	_, env := prState(st, "pre_12")
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) SELECT 'dep_v', id, 'c', 'live', 1, 1 FROM services WHERE environment_id = ? AND name = 'api'`, env)
	st.DB.Exec(`UPDATE services SET current_deploy_id = 'dep_v' WHERE environment_id = ? AND name = 'api'`, env)
	vols, _ := st.Q.ListVolumeServices(context.Background())
	for _, v := range vols {
		var e string
		st.DB.QueryRow(`SELECT environment_id FROM services WHERE id = ?`, v.ID).Scan(&e)
		if e == env {
			t.Fatal("a preview's volume is backed up")
		}
	}
}

func TestPreviewRefusesCrossEnvRef(t *testing.T) {
	st, w, _ := prWorld(t)
	gh := &fakePR{}
	w.GH = gh
	st.DB.Exec(`UPDATE env_vars SET ref = 'shared/db.url' WHERE id = 'v2'`)
	w.Tick(context.Background())
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM environments WHERE name = 'pr-12'`).Scan(&n)
	want := "Preview: not made: DATABASE_URL refers to shared/db.url, another environment's database, which this pull request's migrations would change."
	if state, _ := prState(st, "pre_12"); state != "failed" || n != 0 || len(gh.bodies) == 0 || gh.bodies[len(gh.bodies)-1] != table(12, "| Preview | ❌ "+strings.TrimPrefix(want, "Preview: ")+" |") {
		t.Fatalf("%s %d %q", state, n, gh.bodies)
	}
}

func TestPreviewRefusesTakenFreeHost(t *testing.T) {
	st, w, _ := prWorld(t)
	gh := &fakePR{}
	w.GH = gh
	st.DB.Exec(`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('prj_z','zeta',1,1)`)
	st.DB.Exec(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_z','prj_z','pr-12','srv_1',1,1)`)
	st.DB.Exec(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, created_at, updated_at)
		VALUES ('svc_z', 'env_z', 'api', 'web', 'https://x/y', 'main', 10000, '/', 60000, 30000, 1, 1)`)
	w.Tick(context.Background())
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM environments WHERE name = 'pr-12' AND project_id = 'prj_1'`).Scan(&n)
	want := "Preview: not made: api-pr-12.1-2-3-4.sslip.io is already zeta/pr-12/api's; name this service or environment differently."
	if state, _ := prState(st, "pre_12"); state != "failed" || n != 0 || len(gh.bodies) == 0 || gh.bodies[len(gh.bodies)-1] != table(12, "| Preview | ❌ "+strings.TrimPrefix(want, "Preview: ")+" |") {
		t.Fatalf("%s %d %q", state, n, gh.bodies)
	}
}

func TestLongWaitNotRemovedAtOnce(t *testing.T) {
	st, w, now := prWorld(t)
	st.DB.Exec(`UPDATE previews SET last_push_at = ?`, now.Add(-8*24*time.Hour).UnixMilli())
	w.Tick(context.Background())
	*now = now.Add(time.Minute)
	w.Tick(context.Background())
	if state, _ := prState(st, "pre_12"); state != "making" {
		t.Fatal(state)
	}
}

func TestPreviewKeepsLimits(t *testing.T) {
	st, w, _ := prWorld(t)
	st.DB.Exec(`UPDATE services SET cpu = 0.5, memory = 512000000 WHERE environment_id = 'env_s' AND name = 'api'`)
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, env := prState(st, "pre_12")
	var cpu float64
	var mem int64
	st.DB.QueryRow(`SELECT cpu, memory FROM services WHERE environment_id = ? AND name = 'api'`, env).Scan(&cpu, &mem)
	if cpu != 0.5 || mem != 512e6 {
		t.Fatal(cpu, mem)
	}
}

func TestPreviewKeepsSettings(t *testing.T) {
	st, w, _ := prWorld(t)
	st.DB.Exec(`UPDATE services SET builder = 'railpack', replicas = 3, tags_json = '["team-a"]', watch_json = '["src/"]', www = 'bare', noindex = 1
		WHERE environment_id = 'env_s' AND name = 'api'`)
	st.DB.Exec(`UPDATE services SET pitr = 1, keep_json = '{"daily":3,"weekly":0,"monthly":0}' WHERE environment_id = 'env_s' AND name = 'acme-db'`)
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, env := prState(st, "pre_12")
	var builder, tags, watch, www, keep string
	var replicas, noindex, pitr int64
	st.DB.QueryRow(`SELECT builder, replicas, tags_json, watch_json, www, noindex FROM services WHERE environment_id = ? AND name = 'api'`, env).Scan(&builder, &replicas, &tags, &watch, &www, &noindex)
	st.DB.QueryRow(`SELECT pitr, keep_json FROM services WHERE environment_id = ? AND name = 'acme-db'`, env).Scan(&pitr, &keep)
	if builder != "railpack" || replicas != 3 || tags != `["team-a"]` || watch != `["src/"]` || www != "bare" || noindex != 1 || pitr != 0 ||
		keep != `{"daily":3,"weekly":0,"monthly":0}` {
		t.Fatal(builder, replicas, tags, watch, www, noindex, pitr, keep)
	}
}

func TestPreviewKeepsImage(t *testing.T) {
	st, w, _ := prWorld(t)
	st.DB.Exec(`UPDATE services SET image = 'louislam/uptime-kuma:1', repo = '' WHERE environment_id = 'env_s' AND name = 'jobs'`)
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, env := prState(st, "pre_12")
	var image string
	st.DB.QueryRow(`SELECT image FROM services WHERE environment_id = ? AND name = 'jobs'`, env).Scan(&image)
	if image != "louislam/uptime-kuma:1" {
		t.Fatalf("%q", image)
	}
}

func TestPreviewOverLimit(t *testing.T) {
	st, w, _ := prWorld(t)
	n, _ := st.Q.CountLiveServices(context.Background())
	w.App.Limits = app.Limits{Services: int(n)}
	w.Tick(context.Background())
	var state, cause string
	st.DB.QueryRow(`SELECT state, cause FROM previews WHERE id = 'pre_12'`).Scan(&state, &cause)
	var envs int
	st.DB.QueryRow(`SELECT COUNT(*) FROM environments WHERE name = 'pr-12'`).Scan(&envs)
	if envs != 0 || !strings.Contains(cause, "services in the beta") {
		t.Fatalf("%s %q envs %d", state, cause, envs)
	}
}

type failingPR struct{ n int }

func (f *failingPR) Comment(context.Context, string, int64, int64, string) (int64, error) {
	f.n++
	return 0, errors.New("403 Resource not accessible by integration")
}

func TestPreviewCommentFailureHourly(t *testing.T) {
	st, w, now := prWorld(t)
	gh := &failingPR{}
	w.GH = gh
	ctx := context.Background()
	st.DB.Exec(`UPDATE previews SET state = 'failed', cause = 'x failed'`)
	w.Tick(ctx)
	*now = now.Add(time.Minute)
	w.Tick(ctx)
	if gh.n != 1 {
		t.Fatalf("%d tries in two minutes", gh.n)
	}
	st.DB.Exec(`UPDATE previews SET cause = 'y failed'`)
	w.Tick(ctx)
	if gh.n != 2 {
		t.Fatalf("%d tries after a new text", gh.n)
	}
	*now = now.Add(time.Hour)
	w.Tick(ctx)
	if gh.n != 3 {
		t.Fatalf("%d tries after an hour", gh.n)
	}
}

func TestPreviewAutoDeployOnceLive(t *testing.T) {
	st, w, _ := prWorld(t)
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET auto_deploy = 0 WHERE id = 'svc_jobs'`)
	w.Tick(ctx)
	_, env := prState(st, "pre_12")
	if ids, err := w.App.Push(ctx, "", "acme/api", "search", "abc1234", "dev"); err != nil || len(ids) != 0 {
		t.Fatalf("a push while making queued %v %v", ids, err)
	}
	st.DB.Exec(`UPDATE deploys SET state = 'live' WHERE state = 'queued'`)
	st.DB.Exec(`UPDATE services SET current_deploy_id = (SELECT id FROM deploys WHERE service_id = services.id) WHERE environment_id = ? AND kind = 'postgres'`, env)
	w.Tick(ctx)
	auto := map[string]int{}
	rows, _ := st.DB.Query(`SELECT name, auto_deploy FROM services WHERE environment_id = ? AND kind <> 'postgres'`, env)
	for rows.Next() {
		var n string
		var a int
		rows.Scan(&n, &a)
		auto[n] = a
	}
	rows.Close()
	if auto["api"] != 1 || auto["jobs"] != 0 {
		t.Fatalf("%v", auto)
	}
}
