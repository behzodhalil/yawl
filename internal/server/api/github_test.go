package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/github"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

var ghKey, _ = rsa.GenerateKey(rand.Reader, 2048)
var ghPEM = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(ghKey)}))

type ghBox struct {
	srv      *httptest.Server
	tok      string
	st       *store.Store
	v        *vault.Vault
	a        *app.App
	gh       *github.Client
	noFollow *http.Client
}

func ghSetup(t *testing.T, publicURL string) *ghBox {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	testDB = st.DB
	v, _ := vault.LoadOrCreate(t.TempDir())
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/app-manifests/code-1/conversions" {
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]any{"id": 4242, "slug": "yawl-ab12", "pem": ghPEM, "webhook_secret": "whsec"})
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /app/installations":
			json.NewEncoder(w).Encode([]map[string]any{{"id": 7}})
			return
		case "POST /app/installations/7/access_tokens":
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]any{"token": "inst-7"})
			return
		case "GET /installation/repositories":
			if r.Header.Get("Authorization") != "Bearer inst-7" {
				http.Error(w, "bad token", 401)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"repositories": []map[string]any{
				{"full_name": "acme/old", "html_url": "https://github.com/acme/old", "private": false, "pushed_at": "2026-01-02T03:04:05Z", "default_branch": "master"},
				{"full_name": "acme/empty", "html_url": "https://github.com/acme/empty", "private": true, "pushed_at": nil, "default_branch": "main"},
				{"full_name": "acme/api", "html_url": "https://github.com/acme/api", "private": true, "pushed_at": "2026-11-20T10:00:00Z", "default_branch": "main"},
			}})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(fake.Close)
	gh := &github.Client{API: fake.URL, Web: "https://github.example", HTTP: fake.Client(), Now: time.Now}
	a := app.New(st)
	tok, _ := a.CreateToken(context.Background(), "laptop")
	mux := http.NewServeMux()
	h := GitHub(a, v, gh, publicURL)
	mux.Handle("/github/", h)
	mux.Handle("POST /api/github/connect", h)
	mux.Handle("GET /api/github", h)
	mux.Handle("GET /api/github/repos", h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	nf := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &ghBox{srv: srv, tok: tok, st: st, v: v, a: a, gh: gh, noFollow: nf}
}

func (b *ghBox) get(t *testing.T, path string) (*http.Response, string) {
	t.Helper()
	res, err := b.noFollow.Get(b.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := readAll(res)
	return res, string(body)
}

func TestConnectFlow(t *testing.T) {
	b := ghSetup(t, "https://yawl.example.com")
	if res, _ := call(t, b.srv, "", "POST", "/api/github/connect", ""); res.StatusCode != 401 {
		t.Fatalf("connect without a token: %d", res.StatusCode)
	}
	res, body := call(t, b.srv, b.tok, "POST", "/api/github/connect", "")
	var out struct{ URL string }
	json.Unmarshal(body, &out)
	state := regexp.MustCompile(`^https://yawl\.example\.com/github/setup\?state=([0-9a-f]{32})$`).FindStringSubmatch(out.URL)
	if res.StatusCode != 200 || state == nil {
		t.Fatalf("connect: %d %s", res.StatusCode, body)
	}

	res, page := b.get(t, "/github/setup?state="+state[1])
	if res.StatusCode != 200 || !strings.Contains(page, `action="https://github.example/settings/apps/new?state=`+state[1]+`"`) ||
		!strings.Contains(html.UnescapeString(page), `"url":"https://yawl.example.com/github/webhook"`) {
		t.Fatalf("setup page %d:\n%s", res.StatusCode, page)
	}
	sum := sha256.Sum256([]byte(submitJS))
	if want := "default-src 'none'; script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'; form-action https://github.example; base-uri 'none'; frame-ancestors 'none'"; res.Header.Get("Content-Security-Policy") != want ||
		!strings.Contains(page, "<script>"+submitJS+"</script>") {
		t.Fatalf("setup page CSP %q", res.Header.Get("Content-Security-Policy"))
	}
	if res, _ := b.get(t, "/github/setup?state=0123456789abcdef0123456789abcdef"); res.StatusCode != 403 {
		t.Fatalf("setup with a wrong state: %d", res.StatusCode)
	}

	res, _ = b.get(t, "/github/callback?code=code-1&state="+state[1])
	if res.StatusCode != 302 || res.Header.Get("Location") != "https://github.example/apps/yawl-ab12/installations/new" {
		t.Fatalf("callback %d -> %q", res.StatusCode, res.Header.Get("Location"))
	}
	if res, body := b.get(t, "/github/callback?code=code-1&state="+state[1]); res.StatusCode != 403 ||
		!strings.Contains(body, "This link has expired. Run yawl github connect again.") {
		t.Fatalf("second use of the state: %d %s", res.StatusCode, body)
	}
	got, ok, err := github.Load(context.Background(), b.st.Q, b.v)
	if err != nil || !ok || got.ID != 4242 || got.Slug != "yawl-ab12" || got.WebhookSecret != "whsec" {
		t.Fatalf("Load = %+v %v %v", got, ok, err)
	}
	raw, _ := b.st.Q.GetSetting(context.Background(), "github.app")
	pemBody := strings.Split(ghPEM, "\n")[1]
	if !strings.HasPrefix(raw, "v1:") || strings.Contains(raw, pemBody) || strings.Contains(raw, "whsec") {
		t.Fatalf("github.app stored as %.60q", raw)
	}

	res, body = call(t, b.srv, b.tok, "POST", "/api/github/connect", "")
	if res.StatusCode != 200 || !strings.Contains(string(body), `"install_url":"https://github.example/apps/yawl-ab12/installations/new"`) {
		t.Fatalf("connect when connected: %d %s", res.StatusCode, body)
	}
	if res, body := b.get(t, "/github/installed"); res.StatusCode != 200 || body != "GitHub is connected to Yawl. You can close this tab.\n" {
		t.Fatalf("installed: %d %q", res.StatusCode, body)
	}
}

func TestGitHubStatus(t *testing.T) {
	b := ghSetup(t, "https://yawl.example.com")
	if res, body := call(t, b.srv, b.tok, "GET", "/api/github", ""); res.StatusCode != 200 || strings.TrimSpace(string(body)) != `{"connected":false}` {
		t.Fatalf("before: %d %s", res.StatusCode, body)
	}
	b.connected(t)
	res, body := call(t, b.srv, b.tok, "GET", "/api/github", "")
	if res.StatusCode != 200 || !strings.Contains(string(body), `"connected":true`) || !strings.Contains(string(body), `"slug":"yawl-ab12"`) ||
		strings.Contains(string(body), "whsec") || strings.Contains(string(body), "PRIVATE KEY") {
		t.Fatalf("after: %d %s", res.StatusCode, body)
	}
}

func TestGitHubRepos(t *testing.T) {
	b := ghSetup(t, "https://yawl.example.com")
	if res, body := call(t, b.srv, b.tok, "GET", "/api/github/repos", ""); res.StatusCode != 200 || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("before connect: %d %s", res.StatusCode, body)
	}
	b.connected(t)
	res, body := call(t, b.srv, b.tok, "GET", "/api/github/repos", "")
	var got []github.Repo
	json.Unmarshal(body, &got)
	want := []github.Repo{
		{FullName: "acme/api", URL: "https://github.com/acme/api", Private: true, PushedAt: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC).UnixMilli(), DefaultBranch: "main"},
		{FullName: "acme/old", URL: "https://github.com/acme/old", PushedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).UnixMilli(), DefaultBranch: "master"},
		{FullName: "acme/empty", URL: "https://github.com/acme/empty", Private: true, DefaultBranch: "main"},
	}
	if res.StatusCode != 200 || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("repos: %d %s", res.StatusCode, body)
	}
}

func TestConnectStateExpires(t *testing.T) {
	b := ghSetup(t, "https://yawl.example.com")
	_, body := call(t, b.srv, b.tok, "POST", "/api/github/connect", "")
	var out struct{ URL string }
	json.Unmarshal(body, &out)
	b.a.Now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	if res, _ := b.get(t, strings.TrimPrefix(out.URL, "https://yawl.example.com")); res.StatusCode != 403 {
		t.Fatalf("setup after 11 minutes: %d", res.StatusCode)
	}
}

func TestConnectNeedsPublicURL(t *testing.T) {
	for _, u := range []string{"", "http://127.0.0.1:7000", "https://localhost"} {
		b := ghSetup(t, u)
		res, body := call(t, b.srv, b.tok, "POST", "/api/github/connect", "")
		if res.StatusCode != 409 || !strings.Contains(string(body), "Set --public-url to an https address GitHub can reach first.") {
			t.Fatalf("%q: %d %s", u, res.StatusCode, body)
		}
	}
}

func (b *ghBox) connected(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := github.Save(ctx, b.st.Q, b.v, github.App{ID: 4242, Slug: "yawl-ab12", PEM: ghPEM, WebhookSecret: "whsec"}); err != nil {
		t.Fatal(err)
	}
	b.st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_1", Name: "hel1", PublicIp: "1.2.3.4", CreatedAt: 1, UpdatedAt: 1})
	b.st.Q.UpsertProject(ctx, db.UpsertProjectParams{ID: "prj_1", Name: "acme", CreatedAt: 1, UpdatedAt: 1})
	b.st.Q.UpsertEnvironment(ctx, db.UpsertEnvironmentParams{ID: "env_1", ProjectID: "prj_1", Name: "production", ServerID: "srv_1", CreatedAt: 1, UpdatedAt: 1})
	if _, err := b.st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_1", EnvironmentID: "env_1", Name: "api", Kind: "web",
		Repo: "https://github.com/acme/api", Branch: "main", Port: 10000, HealthPath: "/", HealthTimeoutMs: 60000, DrainTimeoutMs: 30000,
		AutoDeploy: 1, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
}

func pushBody(ref string, deleted bool) string {
	return fmt.Sprintf(`{"ref":%q,"after":"c0ffee","deleted":%v,"repository":{"full_name":"Acme/API"},"pusher":{"name":"behzod"}}`, ref, deleted)
}

func sign(secret, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func (b *ghBox) hook(t *testing.T, event, sig, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", b.srv.URL+"/github/webhook", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", fmt.Sprintf("dlv-%d", time.Now().UnixNano()))
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := readAll(res)
	return res.StatusCode, string(out)
}

func deploys(t *testing.T) int {
	var n int
	testDB.QueryRow(`SELECT COUNT(*) FROM deploys`).Scan(&n)
	return n
}

func TestWebhookPushSigned(t *testing.T) {
	b := ghSetup(t, "https://yawl.example.com")
	b.connected(t)
	body := pushBody("refs/heads/main", false)
	code, out := b.hook(t, "push", sign("whsec", body), body)
	if code != 202 || !strings.Contains(out, `"deploys":["dep_`) || deploys(t) != 1 {
		t.Fatalf("%d %s deploys=%d", code, out, deploys(t))
	}
	var commit, actor string
	testDB.QueryRow(`SELECT commit_sha FROM deploys`).Scan(&commit)
	testDB.QueryRow(`SELECT actor FROM events WHERE kind = 'deploy.queued'`).Scan(&actor)
	if commit != "c0ffee" || actor != "GitHub push by behzod" {
		t.Fatalf("commit %q actor %q", commit, actor)
	}
}

func TestWebhookPing(t *testing.T) {
	b := ghSetup(t, "https://yawl.example.com")
	b.connected(t)
	if code, _ := b.hook(t, "ping", sign("whsec", `{"zen":"x"}`), `{"zen":"x"}`); code != 200 {
		t.Fatalf("ping: %d", code)
	}
}

func TestPushIgnored(t *testing.T) {
	b := ghSetup(t, "https://yawl.example.com")
	b.connected(t)
	for _, body := range []string{pushBody("refs/heads/feature", false), pushBody("refs/tags/v1", false), pushBody("refs/heads/main", true)} {
		if code, out := b.hook(t, "push", sign("whsec", body), body); code != 202 {
			t.Fatalf("%s: %d %s", body, code, out)
		}
	}
	other := `{"action":"opened"}`
	if code, _ := b.hook(t, "pull_request", sign("whsec", other), other); code != 202 {
		t.Fatalf("pull_request: %d", code)
	}
	testDB.Exec(`UPDATE services SET auto_deploy = 0`)
	main := pushBody("refs/heads/main", false)
	b.hook(t, "push", sign("whsec", main), main)
	if n := deploys(t); n != 0 {
		t.Fatalf("%d deploys queued from pushes that must not deploy", n)
	}
}

func TestWebhookRejects(t *testing.T) {
	b := ghSetup(t, "https://yawl.example.com")
	body := pushBody("refs/heads/main", false)
	if code, _ := b.hook(t, "push", sign("whsec", body), body); code != 404 {
		t.Fatalf("not connected: %d", code)
	}
	b.connected(t)
	if code, _ := b.hook(t, "push", "", body); code != 401 {
		t.Fatalf("no signature: %d", code)
	}
	if code, _ := b.hook(t, "push", sign("wrong", body), body); code != 401 {
		t.Fatalf("wrong secret: %d", code)
	}
	big := `{"pad":"` + strings.Repeat("x", 26<<20) + `"}`
	if code, _ := b.hook(t, "push", sign("whsec", big), big); code != 413 {
		t.Fatalf("26 MiB: %d", code)
	}
	if n := deploys(t); n != 0 {
		t.Fatalf("%d deploys after rejected deliveries", n)
	}
}

func TestWebhookBusy(t *testing.T) {
	b := ghSetup(t, "https://yawl.example.com")
	b.connected(t)
	h := newGH(b.a, b.v, b.gh, "https://yawl.example.com")
	for range cap(h.hooks) {
		h.hooks <- struct{}{}
	}
	srv := httptest.NewServer(h.routes())
	t.Cleanup(srv.Close)
	body := pushBody("refs/heads/main", false)
	req, _ := http.NewRequest("POST", srv.URL+"/github/webhook", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sign("whsec", body))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 503 || deploys(t) != 0 {
		t.Fatalf("status %d, deploys %d; want 503 and none", res.StatusCode, deploys(t))
	}
}

func TestPushWatchManyCommits(t *testing.T) {
	b := ghSetup(t, "https://yawl.example.com")
	b.connected(t)
	testDB.Exec(`UPDATE services SET watch_json = '["web/**"]'`)
	commits := strings.TrimSuffix(strings.Repeat(`{"modified":["docs/a.md"]},`, 25), ",")
	body := `{"ref":"refs/heads/main","after":"c0ffee","repository":{"full_name":"Acme/API"},"pusher":{"name":"behzod"},"head_commit":{"message":"docs"},"commits":[` + commits + `]}`
	if code, out := b.hook(t, "push", sign("whsec", body), body); code != 202 || deploys(t) != 0 {
		t.Fatalf("%d %s deploys=%d", code, out, deploys(t))
	}
}

func TestConnectAudited(t *testing.T) {
	b := ghSetup(t, "https://yawl.example.com")
	if res, body := call(t, b.srv, b.tok, "POST", "/api/github/connect", ""); res.StatusCode != 200 {
		t.Fatalf("connect: %d %s", res.StatusCode, body)
	}
	if got := audits(t); len(got) != 1 || got[0] != "API token laptop: started connecting GitHub" {
		t.Fatalf("%q", got)
	}
}
