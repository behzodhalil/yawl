package api

import (
	"context"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestPreviewSendersAPI(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	P := "/api/projects/acme/preview-senders"
	if res, b := call(t, srv, tok, "PUT", P, `{"senders":["Owner"," dependabot[bot] ","owner",""]}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if _, b := call(t, srv, tok, "GET", P, ""); !strings.Contains(string(b), `{"senders":["owner","dependabot[bot]"]}`) {
		t.Fatalf("%s", b)
	}
	if res, b := call(t, srv, tok, "PUT", P, `{"senders":["a b"]}`); res.StatusCode != 400 || !strings.Contains(string(b), "not a forge login") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	dev := memberToken(t, srv, tok, "dev9@acme.uz", "developer")
	if res, _ := call(t, srv, dev, "PUT", P, `{"senders":[]}`); res.StatusCode != 403 {
		t.Fatalf("a Developer changed it: %d", res.StatusCode)
	}
	if res, _ := call(t, srv, dev, "GET", P, ""); res.StatusCode != 200 {
		t.Fatalf("%d", res.StatusCode)
	}
	if res, _ := call(t, srv, tok, "GET", "/api/projects/nope/preview-senders", ""); res.StatusCode != 404 {
		t.Fatalf("%d", res.StatusCode)
	}
}

func TestPreviewSendersWebhook(t *testing.T) {
	b := previewBox(t)
	testDB.Exec(`INSERT INTO settings (key, value) VALUES ('previews.senders.prj_1', 'owner')`)
	for n, sender := range map[int]string{7: "mallory", 8: "owner"} {
		body := strings.Replace(prBody("opened", n, "acme/api"), `"repository"`, `"sender":{"login":"`+sender+`"},"repository"`, 1)
		if code, out := b.hook(t, "pull_request", sign("whsec", body), body); code != 202 {
			t.Fatalf("%d %s", code, out)
		}
	}
	var held, waiting string
	testDB.QueryRow(`SELECT state FROM previews WHERE number = 7`).Scan(&held)
	testDB.QueryRow(`SELECT state FROM previews WHERE number = 8`).Scan(&waiting)
	if held != "held" || waiting != "waiting" {
		t.Fatalf("mallory's %q, owner's %q", held, waiting)
	}

	testDB.Exec(`INSERT INTO environments (id, project_id, name, server_id, preview_of, created_at, updated_at) VALUES ('env_pr9', 'prj_1', 'pr-9', 'srv_1', 'env_1', 1, 1)`)
	if _, err := b.st.Q.UpsertService(context.Background(), db.UpsertServiceParams{ID: "svc_pr9", EnvironmentID: "env_pr9", Name: "api", Kind: "web",
		Repo: "https://github.com/acme/api", Branch: "search", Port: 10001, HealthPath: "/", HealthTimeoutMs: 60000, DrainTimeoutMs: 30000,
		AutoDeploy: 1, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.Exec(`INSERT INTO previews (id, project_id, source_env_id, number, repo, title, branch, head_sha, state, env_id, last_push_at, created_at, updated_at)
		VALUES ('pre_9', 'prj_1', 'env_1', 9, 'acme/api', 't', 'search', 'good', 'live', 'env_pr9', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	body := `{"ref":"refs/heads/search","after":"bad0004","deleted":false,"repository":{"full_name":"Acme/API"},"pusher":{"name":"mallory"},"sender":{"login":"mallory"}}`
	if code, out := b.hook(t, "push", sign("whsec", body), body); code != 202 || deploys(t) != 0 {
		t.Fatalf("%d %s, deploys %d", code, out, deploys(t))
	}
	var state string
	testDB.QueryRow(`SELECT state FROM previews WHERE id = 'pre_9'`).Scan(&state)
	if state != "held" {
		t.Fatal(state)
	}
}
