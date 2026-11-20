package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/gitea"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/spec"
)

func TestGiteaHook(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer api.Close()
	gitea.APIBase = func(string) string { return api.URL }
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
	f, _ := spec.Parse([]byte("project: acme\nenvironment: production\nserver: hel1\npreview: true\nservices:\n  - {name: shop, kind: web, repo: https://codeberg.org/team/shop, branch: main, port: 8080}\n"))
	if _, err := a.Apply(ctx, f, "owner", false); err != nil {
		t.Fatal(err)
	}
	secret, err := gitea.Connect(ctx, st.Q, v, "codeberg.org", "gt-token")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(GiteaHook(a, v))
	defer srv.Close()
	hook := func(event, sig, delivery, body string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/gitea/webhook", strings.NewReader(body))
		req.Header.Set("X-Gitea-Event", event)
		req.Header.Set("X-Gitea-Delivery", delivery)
		req.Header.Set("X-Gitea-Signature", sig)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	sign := func(body string) string {
		m := hmac.New(sha256.New, []byte(secret))
		m.Write([]byte(body))
		return hex.EncodeToString(m.Sum(nil))
	}
	push := `{"ref":"refs/heads/main","after":"c0ffee","pusher":{"login":"behzod"},"repository":{"full_name":"team/shop","html_url":"https://codeberg.org/team/shop"},
		"head_commit":{"id":"c0ffee","message":"fix","added":[],"modified":["main.go"],"removed":[]},"commits":[{"id":"c0ffee"}]}`
	count := func() (n int) { testDB.QueryRow(`SELECT COUNT(*) FROM deploys`).Scan(&n); return }
	if code := hook("push", strings.Repeat("0", 64), "d1", push); code != 401 || count() != 0 {
		t.Fatalf("a wrong signature: %d", code)
	}
	if code := hook("push", sign(push), "d1", push); code != 202 || count() != 1 {
		t.Fatalf("push: %d %d", code, count())
	}
	hook("push", sign(push), "d1", push)
	if count() != 1 {
		t.Fatal("a redelivery deployed again")
	}
	var actor string
	testDB.QueryRow(`SELECT actor FROM events WHERE kind = 'deploy.queued'`).Scan(&actor)
	if actor != "Gitea push by behzod" {
		t.Fatalf("%q", actor)
	}
	pr := `{"action":"opened","number":4,"pull_request":{"title":"New page","state":"open","head":{"ref":"page","sha":"beef","repo":{"full_name":"team/shop"}},"base":{"repo":{"full_name":"team/shop"}}},
		"repository":{"full_name":"team/shop","html_url":"https://codeberg.org/team/shop"}}`
	if code := hook("pull_request", sign(pr), "d2", pr); code != 202 {
		t.Fatalf("pull request: %d", code)
	}
	var repo string
	testDB.QueryRow(`SELECT repo FROM previews WHERE number = 4`).Scan(&repo)
	if repo != "codeberg.org/team/shop" {
		t.Fatalf("preview repo %q", repo)
	}
	testDB.Exec(`INSERT INTO settings (key, value) SELECT 'previews.senders.' || project_id, 'owner' FROM previews WHERE number = 4`)
	for n, sender := range map[string]string{"5": "mallory", "6": "owner"} {
		body := strings.Replace(strings.Replace(pr, `"number":4`, `"number":`+n, 1), `"repository"`, `"sender":{"login":"`+sender+`"},"repository"`, 1)
		if code := hook("pull_request", sign(body), "d"+n, body); code != 202 {
			t.Fatalf("%s: %d", sender, code)
		}
	}
	var held, waiting string
	testDB.QueryRow(`SELECT state FROM previews WHERE number = 5`).Scan(&held)
	testDB.QueryRow(`SELECT state FROM previews WHERE number = 6`).Scan(&waiting)
	if held != "held" || waiting != "waiting" {
		t.Fatalf("mallory's %q, owner's %q", held, waiting)
	}
}

func TestGiteaPushInfo(t *testing.T) {
	body := `{"after":"c2","total_commits":7,"head_commit":{"id":"c2","message":"docs"},
		"commits":[{"id":"c1","message":"api","added":["api/x.go"]},{"id":"c2","message":"docs","modified":["README.md"]}]}`
	info := giteaPushInfo([]byte(body))
	if info.Message != "docs" || strings.Join(info.Files, ",") != "api/x.go,README.md" || !info.Partial {
		t.Fatalf("%+v", info)
	}
	if info := giteaPushInfo([]byte(`{"total_commits":1,"commits":[{"id":"a","modified":["x"]}],"head_commit":{"id":"a","message":"m"}}`)); info.Partial {
		t.Fatalf("%+v", info)
	}
}

func TestGiteaTest(t *testing.T) {
	status := 200
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/user" || r.Header.Get("Authorization") != "token gt-token" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(status)
	}))
	defer api.Close()
	old := gitea.APIBase
	gitea.APIBase = func(string) string { return api.URL }
	t.Cleanup(func() { gitea.APIBase = old })
	srv, tok := setup(t)
	if res, b := call(t, srv, tok, "PUT", "/api/settings/gitea/codeberg.org", `{"token":"gt-token"}`); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/settings/gitea/codeberg.org/test", ""); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	status = 401
	if res, b := call(t, srv, tok, "POST", "/api/settings/gitea/codeberg.org/test", ""); res.StatusCode != 502 || !strings.Contains(string(b), "401") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}
