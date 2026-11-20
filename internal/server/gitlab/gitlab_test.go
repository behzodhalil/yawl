package gitlab

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

type world struct {
	st    *store.Store
	v     *vault.Vault
	calls []string
	notes map[string]string
}

func fakeGitLab(t *testing.T) *world {
	w := &world{notes: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.calls = append(w.calls, r.Method+" "+r.URL.EscapedPath()+" "+r.Header.Get("PRIVATE-TOKEN"))
		if r.Header.Get("PRIVATE-TOKEN") != "glpat-good" {
			http.Error(rw, `{"message":"401 Unauthorized"}`, http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/api/v4/user":
			io.WriteString(rw, `{"username":"owner"}`)
		case strings.HasSuffix(r.URL.Path, "/notes") && r.Method == "POST":
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			w.notes["77"] = b["body"]
			io.WriteString(rw, `{"id":77}`)
		case strings.HasSuffix(r.URL.Path, "/notes/77") && r.Method == "PUT":
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			w.notes["77"] = b["body"]
			io.WriteString(rw, `{"id":77}`)
		default:
			http.NotFound(rw, r)
		}
	}))
	t.Cleanup(srv.Close)
	old := APIBase
	APIBase = func(string) string { return srv.URL }
	t.Cleanup(func() { APIBase = old })
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	w.st = st
	w.v, _ = vault.LoadOrCreate(t.TempDir())
	return w
}

func TestConnect(t *testing.T) {
	w := fakeGitLab(t)
	ctx := context.Background()
	if _, err := Connect(ctx, w.st.Q, w.v, "gitlab.com", "glpat-bad"); err == nil || !strings.Contains(err.Error(), "GitLab refused the token: 401") || strings.Contains(err.Error(), "glpat-bad") {
		t.Fatalf("%v", err)
	}
	if _, err := Connect(ctx, w.st.Q, w.v, "https://gitlab.com/", "glpat-good"); err == nil {
		t.Fatal("a URL was taken as a host")
	}
	secret, err := Connect(ctx, w.st.Q, w.v, "gitlab.com", "glpat-good")
	if err != nil || len(secret) < 32 {
		t.Fatalf("%q %v", secret, err)
	}
	var raw string
	w.st.DB.QueryRow(`SELECT group_concat(value) FROM settings`).Scan(&raw)
	if strings.Contains(raw, "glpat-good") || strings.Contains(raw, secret) {
		t.Fatal("the token or the secret is in settings in the clear")
	}
	hosts, _ := Load(ctx, w.st.Q, w.v)
	if len(hosts) != 1 || hosts[0].Name != "gitlab.com" || hosts[0].Token != "glpat-good" || hosts[0].Secret != secret {
		t.Fatalf("%+v", hosts)
	}
	if h, ok := Match(hosts, secret); !ok || h.Name != "gitlab.com" {
		t.Fatal("the secret does not match its host")
	}
	if _, ok := Match(hosts, "nope"); ok {
		t.Fatal("a wrong secret matched")
	}
	if _, ok := Match(hosts, ""); ok {
		t.Fatal("no secret matched")
	}
	Forget(ctx, w.st.Q, "gitlab.com")
	if hosts, _ := Load(ctx, w.st.Q, w.v); len(hosts) != 0 {
		t.Fatalf("%+v", hosts)
	}
}

func TestTokens(t *testing.T) {
	w := fakeGitLab(t)
	ctx := context.Background()
	Connect(ctx, w.st.Q, w.v, "gitlab.com", "glpat-good")
	tk := Tokens{Q: w.st.Q, V: w.v}
	for repo, want := range map[string]string{"https://gitlab.com/team/sub/shop.git": "glpat-good", "https://git.other.uz/a/b": "", "https://github.com/acme/shop": ""} {
		if got, err := tk.Token(ctx, repo); err != nil || got != want {
			t.Errorf("%s: %q %v", repo, got, err)
		}
	}
	if _, err := tk.Token(ctx, "git@gitlab.com:team/sub/shop.git"); err == nil || !strings.Contains(err.Error(), "https://gitlab.com/team/sub/shop") {
		t.Fatalf("git@: %v", err)
	}
	if got, err := tk.Token(ctx, "git@git.other.uz:a/b.git"); got != "" || err != nil {
		t.Fatalf("an unconnected host: %q %v", got, err)
	}
}

func TestComment(t *testing.T) {
	w := fakeGitLab(t)
	ctx := context.Background()
	Connect(ctx, w.st.Q, w.v, "gitlab.com", "glpat-good")
	c := Comments{Q: w.st.Q, V: w.v}
	id, err := c.Comment(ctx, "gitlab.com/team/sub/shop", 12, 0, "Preview: https://a")
	if err != nil || id != 77 {
		t.Fatalf("%d %v", id, err)
	}
	if _, err := c.Comment(ctx, "gitlab.com/team/sub/shop", 12, 77, "Preview: https://b"); err != nil || w.notes["77"] != "Preview: https://b" {
		t.Fatalf("%v %v", err, w.notes)
	}
	if !strings.Contains(strings.Join(w.calls, "\n"), "POST /api/v4/projects/team%2Fsub%2Fshop/merge_requests/12/notes glpat-good") {
		t.Fatalf("%v", w.calls)
	}
}

func TestNoRedirect(t *testing.T) {
	var leaked string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = r.Header.Get("PRIVATE-TOKEN") }))
	defer other.Close()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/login", http.StatusFound)
	}))
	defer front.Close()
	w := fakeGitLab(t)
	APIBase = func(string) string { return front.URL }
	if _, err := Connect(context.Background(), w.st.Q, w.v, "gitlab.acme.uz", "glpat-good"); err == nil || leaked != "" {
		t.Fatalf("%v, leaked %q", err, leaked)
	}
}

func TestReconnectKeepsSecret(t *testing.T) {
	w := fakeGitLab(t)
	ctx := context.Background()
	first, _ := Connect(ctx, w.st.Q, w.v, "gitlab.com", "glpat-good")
	again, err := Connect(ctx, w.st.Q, w.v, "gitlab.com", "glpat-good")
	hosts, _ := Load(ctx, w.st.Q, w.v)
	if err != nil || again != "" || hosts[0].Secret != first {
		t.Fatalf("%q %v %+v", again, err, hosts)
	}
}

func TestStatus(t *testing.T) {
	var got map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/user" {
			io.WriteString(rw, `{}`)
			return
		}
		path = r.Method + " " + r.URL.EscapedPath()
		json.NewDecoder(r.Body).Decode(&got)
		rw.WriteHeader(201)
	}))
	defer srv.Close()
	w := fakeGitLab(t)
	APIBase = func(string) string { return srv.URL }
	ctx := context.Background()
	Connect(ctx, w.st.Q, w.v, "gitlab.com", "glpat-good")
	err := Statuses{Q: w.st.Q, V: w.v}.Status(ctx, "gitlab.com/team/sub/shop", "c0ffee", "failure", "yawl/production/shop", "Health check failed", "https://panel/x")
	if err != nil || path != "POST /api/v4/projects/team%2Fsub%2Fshop/statuses/c0ffee" || got["state"] != "failed" || got["name"] != "yawl/production/shop" || got["target_url"] != "https://panel/x" {
		t.Fatalf("%v %s %v", err, path, got)
	}
	for in, want := range map[string]string{"success": "success", "pending": "pending", "error": "canceled"} {
		Statuses{Q: w.st.Q, V: w.v}.Status(ctx, "gitlab.com/a/b", "c", in, "n", "d", "t")
		if got["state"] != want {
			t.Errorf("%s: %v", in, got["state"])
		}
	}
}
