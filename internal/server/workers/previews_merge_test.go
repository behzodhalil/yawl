package workers

import (
	"context"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func livePreview(t *testing.T, w *Previews) string {
	t.Helper()
	st, ctx := w.St, context.Background()
	w.Tick(ctx)
	_, env := prState(st, "pre_12")
	st.DB.Exec(`UPDATE deploys SET state = 'live' WHERE state = 'queued'`)
	st.DB.Exec(`UPDATE services SET current_deploy_id = (SELECT id FROM deploys WHERE service_id = services.id) WHERE environment_id = ?`, env)
	w.Tick(ctx)
	st.DB.Exec(`UPDATE deploys SET state = 'live', commit_sha = 'abc1234ffff' WHERE state = 'queued'`)
	st.DB.Exec(`UPDATE services SET current_deploy_id = (SELECT id FROM deploys WHERE service_id = services.id ORDER BY created_at DESC LIMIT 1) WHERE environment_id = ?`, env)
	return env
}

func TestPreviewDomain(t *testing.T) {
	st, w, _ := prWorld(t)
	gh := &fakePR{}
	w.GH = gh
	st.DB.Exec(`UPDATE environments SET preview_domain = 'preview.acme.uz' WHERE id = 'env_s'`)
	env := livePreview(t, w)
	var hosts []string
	rows, _ := st.DB.Query(`SELECT d.host FROM domains d JOIN services s ON s.id = d.service_id WHERE s.environment_id = ?`, env)
	for rows.Next() {
		var h string
		rows.Scan(&h)
		hosts = append(hosts, h)
	}
	rows.Close()
	if len(hosts) != 1 || hosts[0] != "api-pr-12.preview.acme.uz" {
		t.Fatal(hosts)
	}
	w.Tick(context.Background())
	if last := gh.bodies[len(gh.bodies)-1]; last != table(12, "| Preview | ✅ [api-pr-12.1-2-3-4.sslip.io](https://api-pr-12.1-2-3-4.sslip.io) |", "| Commit | `abc1234` |") {
		t.Fatalf("%q", last)
	}
	st.DB.Exec(`UPDATE domains SET dns_state = 'ok' WHERE host = 'api-pr-12.preview.acme.uz'`)
	w.Tick(context.Background())
	if last := gh.bodies[len(gh.bodies)-1]; last != table(12, "| Preview | ✅ [api-pr-12.preview.acme.uz](https://api-pr-12.preview.acme.uz) |", "| Commit | `abc1234` |") {
		t.Fatalf("%q", last)
	}
}

func TestPreviewDomainTakenUsesFreeHost(t *testing.T) {
	st, w, _ := prWorld(t)
	st.DB.Exec(`UPDATE environments SET preview_domain = 'preview.acme.uz' WHERE id = 'env_s'`)
	st.DB.Exec(`INSERT INTO domains (id, service_id, host, created_at, updated_at) VALUES ('dom_x', 'svc_jobs', 'api-pr-12.preview.acme.uz', 1, 1)`)
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state, _ := prState(st, "pre_12"); state != "making" {
		t.Fatal(state)
	}
}

func TestMergedCommentFollowsDeploy(t *testing.T) {
	st, w, now := prWorld(t)
	gh := &fakePR{}
	w.GH = gh
	ctx := context.Background()
	livePreview(t, w)
	w.Tick(ctx)
	st.DB.Exec(`UPDATE previews SET state = 'closing', base = 'main', merge_sha = 'e51f0d2aaaa', merged_at = ?`, now.UnixMilli())
	w.Tick(ctx)
	last := func() string { return gh.bodies[len(gh.bodies)-1] }
	if last() != table(12, "| staging · api | ⏳ Waiting for the push to `main` |", "| Preview | 🗑 Removed |") {
		t.Fatalf("%q", last())
	}
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_m', 'svc_api', 'e51f0d2aaaa', 'running', 1, 1)`)
	w.Tick(ctx)
	if last() != table(12, "| staging · api | ⏳ Deploying `e51f0d2` |", "| Preview | 🗑 Removed |") {
		t.Fatalf("%q", last())
	}
	st.DB.Exec(`UPDATE deploys SET state = 'live' WHERE id = 'dep_m'`)
	w.Tick(ctx)
	if last() != table(12, "| staging · api | ✅ [api.acme.uz](https://api.acme.uz) · `e51f0d2` |", "| Preview | 🗑 Removed |") {
		t.Fatalf("%q", last())
	}
	n := len(gh.bodies)
	st.DB.Exec(`UPDATE deploys SET state = 'failed', cause = 'later' WHERE id = 'dep_m'`)
	w.Tick(ctx)
	if len(gh.bodies) != n {
		t.Fatal("edited after its last word")
	}
}

func TestMergedCommentSkippedAndFailed(t *testing.T) {
	st, w, now := prWorld(t)
	gh := &fakePR{}
	w.GH = gh
	ctx := context.Background()
	livePreview(t, w)
	w.Tick(ctx)
	st.DB.Exec(`UPDATE previews SET state = 'closing', base = 'main', merge_sha = 'e51f0d2aaaa', merged_at = ?`, now.UnixMilli())
	st.DB.Exec(`INSERT INTO events (id, kind, subject_id, actor, body_json, created_at, updated_at) VALUES ('evt_s', 'push.skipped', 'svc_api', 'x', '{"commit":"e51f0d2aaaa"}', 1, 1)`)
	w.Tick(ctx)
	if last := gh.bodies[len(gh.bodies)-1]; last != table(12, "| staging · api | ➖ Not deployed: this change does not touch it |", "| Preview | 🗑 Removed |") {
		t.Fatalf("%q", last)
	}
	var done int
	st.DB.QueryRow(`SELECT comment_done FROM previews WHERE id = 'pre_12'`).Scan(&done)
	if done != 1 {
		t.Fatal("not done")
	}

	st.DB.Exec(`UPDATE previews SET comment_done = 0`)
	st.DB.Exec(`DELETE FROM events WHERE id = 'evt_s'`)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, cause, created_at, updated_at) VALUES ('dep_f', 'svc_api', 'e51f0d2aaaa', 'failed', 'Health check failed', 1, 1)`)
	w.Tick(ctx)
	if last := gh.bodies[len(gh.bodies)-1]; last != table(12, "| staging · api | ❌ Deploy of `e51f0d2` failed: Health check failed |", "| Preview | 🗑 Removed |") {
		t.Fatalf("%q", last)
	}
}

func TestMergedCommentGivesUp(t *testing.T) {
	st, w, now := prWorld(t)
	gh := &fakePR{}
	w.GH = gh
	ctx := context.Background()
	livePreview(t, w)
	w.Tick(ctx)
	st.DB.Exec(`UPDATE previews SET state = 'closing', base = 'main', merge_sha = 'e51f0d2aaaa', merged_at = ?`, now.UnixMilli())
	w.Tick(ctx)
	*now = now.Add(25 * time.Hour)
	w.Tick(ctx)
	var done int
	st.DB.QueryRow(`SELECT comment_done FROM previews WHERE id = 'pre_12'`).Scan(&done)
	if done != 1 {
		t.Fatal("still watching")
	}
}

func TestPreviewLeavesOutNoPreview(t *testing.T) {
	st, w, _ := prWorld(t)
	st.DB.Exec(`UPDATE services SET no_preview = 1 WHERE name = 'jobs'`)
	w.Tick(context.Background())
	_, env := prState(st, "pre_12")
	var jobs, api int
	st.DB.QueryRow(`SELECT count(*) FROM services WHERE environment_id = ? AND name = 'jobs'`, env).Scan(&jobs)
	st.DB.QueryRow(`SELECT count(*) FROM services WHERE environment_id = ? AND name = 'api'`, env).Scan(&api)
	if env == "" || jobs != 0 || api != 1 {
		t.Fatal(env, jobs, api)
	}

	st2, w2, _ := prWorld(t)
	st2.DB.Exec(`UPDATE services SET no_preview = 1 WHERE name = 'acme-db'`)
	w2.Tick(context.Background())
	var state, cause string
	st2.DB.QueryRow(`SELECT state, cause FROM previews WHERE id = 'pre_12'`).Scan(&state, &cause)
	if state != "failed" || cause != `not made: DATABASE_URL refers to acme-db, which has "preview: false".` {
		t.Fatal(state, cause)
	}
}

func TestPreviewCard(t *testing.T) {
	st, w, now := prWorld(t)
	ctx := context.Background()
	card := func() Card {
		t.Helper()
		var p db.Preview
		st.DB.QueryRow(`SELECT id, project_id, source_env_id, number, repo, state, env_id, cause, base, merge_sha, merged_at FROM previews WHERE id = 'pre_12'`).Scan(
			&p.ID, &p.ProjectID, &p.SourceEnvID, &p.Number, &p.Repo, &p.State, &p.EnvID, &p.Cause, &p.Base, &p.MergeSha, &p.MergedAt)
		c, err := PreviewCard(ctx, st.Q, p)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	steps := func(c Card) (out string) {
		for _, s := range c.Steps {
			out += s.Name + ":" + s.State + " "
		}
		return
	}
	env := livePreview(t, w)
	w.Tick(ctx)
	if c := card(); steps(c) != "build:done deploy:done live:done " || c.URL != "https://api-pr-12.1-2-3-4.sslip.io" || c.Service != "api" || c.Commit != "abc1234ffff" {
		t.Fatalf("%+v", c)
	}
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) SELECT 'dep_q', id, 'def5678', 'queued', 9999999999998, 1 FROM services WHERE environment_id = ? AND name = 'api'`, env)
	if c := card(); steps(c) != "build:run deploy:todo live:todo " || c.URL != "" {
		t.Fatalf("queued %+v", c)
	}
	st.DB.Exec(`UPDATE deploys SET state = 'failed', cause = 'npm ci failed' WHERE id = 'dep_q'`)
	st.DB.Exec(`INSERT INTO deploy_steps (id, deploy_id, step, state, started_at, finished_at, created_at, updated_at) VALUES ('ds1', 'dep_q', 'build', 'failed', 1, 2, 1, 1)`)
	if c := card(); steps(c) != "build:fail deploy:todo live:todo " || c.Cause != "npm ci failed" {
		t.Fatalf("failed %+v", c)
	}
	st.DB.Exec(`UPDATE previews SET state = 'closing', base = 'main', merge_sha = 'e51f0d2aaaa', merged_at = ?`, now.UnixMilli())
	w.Tick(ctx)
	if c := card(); len(c.Landed) != 2 || c.Landed[0][0] != "staging · api" || c.Landed[1] != [2]string{"Preview", "Removed"} {
		t.Fatalf("merged %+v", c)
	}
}
