package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/gitlab"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/spec"
)

func fakeUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "glpat-good" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"username":"owner"}`)
	}))
	t.Cleanup(srv.Close)
	old := gitlab.APIBase
	gitlab.APIBase = func(string) string { return srv.URL }
	t.Cleanup(func() { gitlab.APIBase = old })
}

func TestGitLabSettings(t *testing.T) {
	fakeUser(t)
	srv, tok := setup(t)
	if res, b := call(t, srv, tok, "PUT", "/api/settings/gitlab/gitlab.com", `{"token":"glpat-bad"}`); res.StatusCode != 400 || !strings.Contains(string(b), "GitLab refused the token: 401") {
		t.Fatalf("bad token: %d %s", res.StatusCode, b)
	}
	res, b := call(t, srv, tok, "PUT", "/api/settings/gitlab/gitlab.com", `{"token":"glpat-good"}`)
	var out struct{ Host, WebhookURL, Secret string }
	json.Unmarshal(b, &out)
	if res.StatusCode != 200 || out.Host != "gitlab.com" || out.Secret == "" {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), `"webhook_url":"http://panel.test/gitlab/webhook"`) {
		t.Fatalf("%s", b)
	}
	res, b = call(t, srv, tok, "GET", "/api/settings/gitlab", "")
	if res.StatusCode != 200 || string(b) != `[{"host":"gitlab.com","webhook_url":"http://panel.test/gitlab/webhook"}]`+"\n" {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	dev := memberToken(t, srv, tok, "dev@acme.uz", "developer")
	if res, _ := call(t, srv, dev, "PUT", "/api/settings/gitlab/gitlab.com", `{"token":"glpat-good"}`); res.StatusCode != 403 {
		t.Fatalf("a Developer: %d", res.StatusCode)
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/settings/gitlab/gitlab.com", ""); res.StatusCode != 204 {
		t.Fatalf("delete %d", res.StatusCode)
	}
	if _, b := call(t, srv, tok, "GET", "/api/settings/gitlab", ""); string(b) != "[]\n" {
		t.Fatalf("%s", b)
	}
}

type glBox struct {
	srv    *httptest.Server
	secret string
	st     *store.Store
}

func glSetup(t *testing.T) *glBox {
	fakeUser(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	testDB = st.DB
	ctx := context.Background()
	st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_1", Name: "hel1", PublicIp: "1.2.3.4", CreatedAt: 1, UpdatedAt: 1})
	v, _ := vault.LoadOrCreate(t.TempDir())
	a := app.New(st)
	f, err := spec.Parse([]byte("project: acme\nenvironment: production\nserver: hel1\npreview: true\nservices:\n" +
		"  - {name: shop, kind: web, repo: https://gitlab.com/team/sub/shop, branch: main, port: 8080}\n" +
		"  - {name: other, kind: web, repo: https://gitlab.com/team/shop, branch: main, port: 8080}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(ctx, f, "owner", false); err != nil {
		t.Fatal(err)
	}
	secret, err := gitlab.Connect(ctx, st.Q, v, "gitlab.com", "glpat-good")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/gitlab/", GitLabHook(a, v))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &glBox{srv: srv, secret: secret, st: st}
}

func (b *glBox) hook(t *testing.T, event, token, uuid, body string) int {
	req, _ := http.NewRequest("POST", b.srv.URL+"/gitlab/webhook", strings.NewReader(body))
	req.Header.Set("X-Gitlab-Event", event)
	req.Header.Set("X-Gitlab-Event-UUID", uuid)
	if token != "" {
		req.Header.Set("X-Gitlab-Token", token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

const glPush = `{"object_kind":"push","ref":"refs/heads/main","checkout_sha":"c0ffee","user_username":"behzod","project":{"path_with_namespace":"team/sub/shop"}}`

func TestGitLabPush(t *testing.T) {
	b := glSetup(t)
	if code := b.hook(t, "Push Hook", "wrong", "u1", glPush); code != 401 || deploys(t) != 0 {
		t.Fatalf("wrong token: %d", code)
	}
	if code := b.hook(t, "Push Hook", b.secret, "u1", glPush); code != 202 || deploys(t) != 1 {
		t.Fatalf("push: %d deploys %d", code, deploys(t))
	}
	var svc, actor string
	testDB.QueryRow(`SELECT s.name FROM deploys d JOIN services s ON s.id = d.service_id`).Scan(&svc)
	testDB.QueryRow(`SELECT actor FROM events WHERE kind = 'deploy.queued'`).Scan(&actor)
	if svc != "shop" || actor != "GitLab push by behzod" {
		t.Fatalf("%s %q", svc, actor)
	}
	if b.hook(t, "Push Hook", b.secret, "u1", glPush); deploys(t) != 1 {
		t.Fatal("a redelivery deployed again")
	}
}

func TestGitLabMergeRequest(t *testing.T) {
	b := glSetup(t)
	mr := func(src int) string {
		return `{"object_kind":"merge_request","project":{"id":7,"path_with_namespace":"team/sub/shop"},"object_attributes":{"iid":12,"action":"open","state":"opened",` +
			`"title":"Faster checkout","source_branch":"fast","source_project_id":` + map[int]string{7: "7", 9: "9"}[src] + `,"target_project_id":7,"last_commit":{"id":"beef"}}}`
	}
	if code := b.hook(t, "Merge Request Hook", b.secret, "m1", mr(7)); code != 202 {
		t.Fatalf("%d", code)
	}
	var repo, state string
	testDB.QueryRow(`SELECT repo, state FROM previews WHERE number = 12`).Scan(&repo, &state)
	if repo != "gitlab.com/team/sub/shop" || state != "waiting" {
		t.Fatalf("%q %q", repo, state)
	}
	testDB.Exec(`DELETE FROM previews`)
	b.hook(t, "Merge Request Hook", b.secret, "m2", mr(9))
	testDB.QueryRow(`SELECT state FROM previews WHERE number = 12`).Scan(&state)
	if state != "fork" {
		t.Fatalf("fork: %q", state)
	}
	testDB.Exec(`DELETE FROM previews`)
	testDB.Exec(`INSERT INTO settings (key, value) SELECT 'previews.senders.' || project_id, 'owner' FROM environments WHERE preview = 1`)
	for user, want := range map[string]string{"mallory": "held", "owner": "waiting"} {
		testDB.Exec(`DELETE FROM previews`)
		b.hook(t, "Merge Request Hook", b.secret, "m-"+user, strings.Replace(mr(7), `"project":`, `"user":{"username":"`+user+`"},"project":`, 1))
		testDB.QueryRow(`SELECT state FROM previews WHERE number = 12`).Scan(&state)
		if state != want {
			t.Fatalf("%s: %q", user, state)
		}
	}
}

func TestCommitStatusNote(t *testing.T) {
	srv, tok := setup(t)
	if _, b := call(t, srv, tok, "GET", "/api/settings/commit-status", ""); string(b) != `{"github":"","gitlab":""}`+"\n" {
		t.Fatalf("%s", b)
	}
	testDB.Exec(`INSERT INTO settings (key, value) VALUES ('commitstatus.refused.github', 'acme/api: github: 422')`)
	if _, b := call(t, srv, tok, "GET", "/api/settings/commit-status", ""); string(b) != `{"github":"acme/api: github: 422","gitlab":""}`+"\n" {
		t.Fatalf("%s", b)
	}
}

func TestGitLabHookLimits(t *testing.T) {
	b := glSetup(t)
	big := `{"pad":"` + strings.Repeat("x", 26<<20) + `"}`
	if code := b.hook(t, "Push Hook", b.secret, "big", big); code != 413 {
		t.Fatalf("26 MiB: %d", code)
	}
	for range cap(glHooks) {
		glHooks <- struct{}{}
	}
	defer func() {
		for range cap(glHooks) {
			<-glHooks
		}
	}()
	if code := b.hook(t, "Push Hook", b.secret, "u9", glPush); code != 503 || deploys(t) != 0 {
		t.Fatalf("busy: %d deploys %d", code, deploys(t))
	}
}

func TestGitLabHookURL(t *testing.T) {
	for public, want := range map[string]string{
		"https://panel.acme.uz": "https://panel.acme.uz/gitlab/webhook",
		"http://10.0.0.5:8080/": "http://10.0.0.5:8080/gitlab/webhook",
		"http://127.0.0.1:8080": "",
		"http://:8080":          "",
		"http://localhost:8080": "",
		"":                      "",
	} {
		if got := (&handler{d: Deps{PublicURL: public}}).hookURL(); got != want {
			t.Errorf("%q: %q, want %q", public, got, want)
		}
	}
}

func TestGitLabForgetInUse(t *testing.T) {
	fakeUser(t)
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: shop, kind: web, repo: \"https://gitlab.com/team/shop\"}\n")
	call(t, srv, tok, "PUT", "/api/settings/gitlab/gitlab.com", `{"token":"glpat-good"}`)
	res, b := call(t, srv, tok, "DELETE", "/api/settings/gitlab/gitlab.com", "")
	if res.StatusCode != 200 || !strings.Contains(string(b), "acme/production/shop") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestGitLabTest(t *testing.T) {
	status := 200
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/user" || r.Header.Get("PRIVATE-TOKEN") != "glpat-good" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(status)
	}))
	defer api.Close()
	old := gitlab.APIBase
	gitlab.APIBase = func(string) string { return api.URL }
	t.Cleanup(func() { gitlab.APIBase = old })
	srv, tok := setup(t)
	if res, b := call(t, srv, tok, "PUT", "/api/settings/gitlab/gitlab.com", `{"token":"glpat-good"}`); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/settings/gitlab/gitlab.com/test", ""); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	status = 401
	if res, b := call(t, srv, tok, "POST", "/api/settings/gitlab/gitlab.com/test", ""); res.StatusCode != 502 || !strings.Contains(string(b), "401") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}
