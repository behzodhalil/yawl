package workers

import (
	"context"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func pr7(st *store.Store) (state, env, sha string) {
	st.DB.QueryRow(`SELECT state, env_id, head_sha FROM previews WHERE number = 7`).Scan(&state, &env, &sha)
	return
}

func sendersWorld(t *testing.T) (*store.Store, *Previews, *fakePR) {
	t.Helper()
	st, w, _ := prWorld(t)
	gh := &fakePR{}
	w.GH = gh
	ctx := context.Background()
	st.DB.Exec(`DELETE FROM previews`)
	st.DB.Exec(`INSERT INTO secret_sources (id, environment_id, provider, url, path, agent_box, recovery_box, created_at, updated_at) VALUES ('src_1', 'env_s', 'vault', 'https://vault.acme', 'secret/data/shop', 'vb', 'vr', 1, 1)`)
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: app.PreviewSendersKey("prj_1"), Value: "owner\ndependabot[bot]"})
	return st, w, gh
}

func makeLive(t *testing.T, st *store.Store, w *Previews) string {
	t.Helper()
	ctx := context.Background()
	w.Tick(ctx)
	_, env, _ := pr7(st)
	st.DB.Exec(`UPDATE deploys SET state = 'live' WHERE state = 'queued'`)
	st.DB.Exec(`UPDATE services SET current_deploy_id = (SELECT id FROM deploys WHERE service_id = services.id) WHERE environment_id = ? AND kind = 'postgres'`, env)
	w.Tick(ctx)
	if state, _, _ := pr7(st); state != "live" {
		t.Fatalf("not live: %s", state)
	}
	return env
}

func TestPreviewSendersGateSecrets(t *testing.T) {
	st, w, gh := sendersWorld(t)
	ctx := context.Background()
	pr := app.PR{Action: "opened", State: "open", Number: 7, Title: "x", Branch: "search", Sha: "bad0001", Repo: "acme/api", HeadRepo: "acme/api", Sender: "mallory"}
	if err := w.App.PullRequest(ctx, pr); err != nil {
		t.Fatal(err)
	}
	w.Tick(ctx)
	var envs int
	st.DB.QueryRow(`SELECT count(*) FROM environments WHERE name = 'pr-7'`).Scan(&envs)
	if state, env, _ := pr7(st); state != "held" || env != "" || envs != 0 {
		t.Fatalf("an unlisted PR: %s env %q, %d environments", state, env, envs)
	}
	if len(gh.bodies) != 1 || gh.bodies[0] != table(7, "| Preview | ⏸️ Preview held: the last change came from someone not on this project's preview list. A push from someone on it makes the preview. |") {
		t.Fatalf("%q", gh.bodies)
	}
	if err := w.App.RenewPreviews(ctx, "acme/api", "search", "bad0002", "mallory"); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := pr7(st); state != "held" {
		t.Fatalf("an unlisted push released it: %s", state)
	}

	if err := w.App.RenewPreviews(ctx, "acme/api", "search", "good001", "Owner"); err != nil {
		t.Fatal(err)
	}
	env := makeLive(t, st, w)
	var src, commit string
	st.DB.QueryRow(`SELECT agent_box FROM secret_sources WHERE environment_id = ?`, env).Scan(&src)
	st.DB.QueryRow(`SELECT d.commit_sha FROM deploys d JOIN services s ON s.id = d.service_id WHERE s.environment_id = ? AND s.name = 'api'`, env).Scan(&commit)
	if src != "vb" || commit != "good001" {
		t.Fatalf("a listed push: source %q, api built at %q", src, commit)
	}

	pr.Action, pr.Sha = "synchronize", "bad0003"
	if err := w.App.PullRequest(ctx, pr); err != nil {
		t.Fatal(err)
	}
	var live int
	st.DB.QueryRow(`SELECT count(*) FROM services WHERE environment_id = ? AND deleted_at IS NULL`, env).Scan(&live)
	if state, _, sha := pr7(st); state != "held" || sha != "good001" || live != 0 {
		t.Fatalf("an unlisted synchronize: %s at %s, %d services left", state, sha, live)
	}
	pr.Action, pr.State = "closed", "closed"
	w.App.PullRequest(ctx, pr)
	w.Tick(ctx)
	if state, _, _ := pr7(st); state != "closed" {
		t.Fatalf("a held PR closed: %s", state)
	}
}

func TestPreviewSenderPushBuildsNothing(t *testing.T) {
	st, w, _ := sendersWorld(t)
	ctx := context.Background()
	if err := w.App.PullRequest(ctx, app.PR{Action: "opened", State: "open", Number: 7, Title: "x", Branch: "search", Sha: "good001", Repo: "acme/api", HeadRepo: "acme/api", Sender: "dependabot[bot]"}); err != nil {
		t.Fatal(err)
	}
	makeLive(t, st, w)
	if err := w.App.RenewPreviews(ctx, "acme/api", "search", "bad0004", "mallory"); err != nil {
		t.Fatal(err)
	}
	if ids, err := w.App.PushVia(ctx, "GitHub", "d-1", "acme/api", "search", "bad0004", "mallory", app.PushInfo{}); err != nil || len(ids) != 0 {
		t.Fatalf("queued %v: %v", ids, err)
	}
	var bad int
	st.DB.QueryRow(`SELECT count(*) FROM deploys WHERE commit_sha = 'bad0004'`).Scan(&bad)
	if state, _, _ := pr7(st); state != "held" || bad != 0 {
		t.Fatalf("%s, %d deploys of the unlisted commit", state, bad)
	}
}
