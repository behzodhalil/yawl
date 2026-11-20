package github

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

var testKey, _ = rsa.GenerateKey(rand.Reader, 2048)

func testApp() App {
	b := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(testKey)})
	return App{ID: 4242, Slug: "yawl-ab12", PEM: string(b), WebhookSecret: "whsec"}
}

func TestManifest(t *testing.T) {
	b, err := Manifest("https://yawl.example.com", "yawl-ab12")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Name        string               `json:"name"`
		URL         string               `json:"url"`
		Hook        struct{ URL string } `json:"hook_attributes"`
		RedirectURL string               `json:"redirect_url"`
		SetupURL    string               `json:"setup_url"`
		Public      bool                 `json:"public"`
		Events      []string             `json:"default_events"`
		Permissions map[string]string    `json:"default_permissions"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "yawl-ab12" || m.Hook.URL != "https://yawl.example.com/github/webhook" ||
		m.RedirectURL != "https://yawl.example.com/github/callback" || m.SetupURL != "https://yawl.example.com/github/installed" ||
		m.Public || len(m.Events) != 2 || m.Events[0] != "push" || m.Events[1] != "pull_request" ||
		len(m.Permissions) != 4 || m.Permissions["contents"] != "read" || m.Permissions["metadata"] != "read" || m.Permissions["pull_requests"] != "write" || m.Permissions["statuses"] != "write" {
		t.Fatalf("manifest %s", b)
	}
}

func checkJWT(t *testing.T, tok string) map[string]any {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt %q", tok)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	if err := rsa.VerifyPKCS1v15(&testKey.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature: %v", err)
	}
	var hdr, claims map[string]any
	h, _ := base64.RawURLEncoding.DecodeString(parts[0])
	c, _ := base64.RawURLEncoding.DecodeString(parts[1])
	json.Unmarshal(h, &hdr)
	json.Unmarshal(c, &claims)
	if hdr["alg"] != "RS256" {
		t.Fatalf("header %v", hdr)
	}
	return claims
}

func TestJWTVerifies(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tok, err := JWT(testApp(), now)
	if err != nil {
		t.Fatal(err)
	}
	c := checkJWT(t, tok)
	if c["iss"] != "4242" || c["iat"].(float64) != float64(now.Unix()-60) || c["exp"].(float64)-c["iat"].(float64) != 600 {
		t.Fatalf("claims %v", c)
	}
}

func fakeGitHub(t *testing.T, installed bool, seen *map[string]any) *Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/acme/api/installation":
			checkJWT(t, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if !installed {
				http.Error(w, `{"message":"Not Found"}`, 404)
				return
			}
			w.Write([]byte(`{"id":77}`))
		case r.Method == "POST" && r.URL.Path == "/app/installations/77/access_tokens":
			checkJWT(t, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			json.NewDecoder(r.Body).Decode(seen)
			w.WriteHeader(201)
			w.Write([]byte(`{"token":"ghs_x","expires_at":"2030-01-01T00:00:00Z"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{API: srv.URL, Web: "https://github.example", HTTP: srv.Client(), Now: time.Now}
}

func TestInstallationTokenScoped(t *testing.T) {
	var body map[string]any
	c := fakeGitHub(t, true, &body)
	tok, err := c.InstallationToken(context.Background(), testApp(), "acme/api")
	if err != nil || tok != "ghs_x" {
		t.Fatalf("token %q, %v", tok, err)
	}
	b, _ := json.Marshal(body)
	if string(b) != `{"permissions":{"contents":"read"},"repositories":["api"]}` {
		t.Fatalf("request body %s", b)
	}
}

func TestTokenNotInstalledErr(t *testing.T) {
	c := fakeGitHub(t, false, new(map[string]any))
	if _, err := c.InstallationToken(context.Background(), testApp(), "acme/api"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("err = %v, want ErrNotInstalled", err)
	}
}

func TestVerify(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	m := hmac.New(sha256.New, []byte("whsec"))
	m.Write(body)
	good := "sha256=" + hex.EncodeToString(m.Sum(nil))
	if !Verify("whsec", body, good) {
		t.Fatal("good signature rejected")
	}
	for _, h := range []string{"", strings.TrimPrefix(good, "sha256="), "sha256=00", good[:len(good)-1] + "0"} {
		if Verify("whsec", body, h) {
			t.Fatalf("accepted %q", h)
		}
	}
	if Verify("other", body, good) || Verify("", body, good) {
		t.Fatal("accepted with the wrong secret")
	}
}

func tokensWith(t *testing.T, c *Client, connected bool) Tokens {
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	v, _ := vault.LoadOrCreate(t.TempDir())
	if connected {
		Save(context.Background(), st.Q, v, testApp())
	}
	return Tokens{C: c, Q: st.Q, V: v}
}

func TestTokenNotInstalled(t *testing.T) {
	c := fakeGitHub(t, false, new(map[string]any))
	_, err := tokensWith(t, c, true).Token(context.Background(), "https://github.com/Acme/API.git")
	want := "The Yawl GitHub App is not installed on acme/api. Install it: https://github.example/apps/yawl-ab12/installations/new"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestTokensMints(t *testing.T) {
	c := fakeGitHub(t, true, new(map[string]any))
	tok, err := tokensWith(t, c, true).Token(context.Background(), "git@github.com:acme/api.git")
	if err != nil || tok != "ghs_x" {
		t.Fatalf("token %q, %v", tok, err)
	}
}

func TestTokensNonGitHub(t *testing.T) {
	c := &Client{API: "http://127.0.0.1:1", HTTP: http.DefaultClient, Now: time.Now}
	for _, tc := range []struct {
		repo      string
		connected bool
	}{{"file:///tmp/x", true}, {"https://gitlab.com/a/b", true}, {"https://github.com/acme/api", false}} {
		tok, err := tokensWith(t, c, tc.connected).Token(context.Background(), tc.repo)
		if tok != "" || err != nil {
			t.Fatalf("%s (connected %v): %q, %v; want anonymous", tc.repo, tc.connected, tok, err)
		}
	}
}

func TestCommentCreatesThenEdits(t *testing.T) {
	var calls []string
	var perms map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/acme/api/installation":
			w.Write([]byte(`{"id":77}`))
		case r.Method == "POST" && r.URL.Path == "/app/installations/77/access_tokens":
			perms = body
			w.WriteHeader(201)
			w.Write([]byte(`{"token":"ghs_pr"}`))
		case r.Header.Get("Authorization") != "Bearer ghs_pr":
			http.Error(w, "bad token", 401)
		case r.Method == "POST" && r.URL.Path == "/repos/acme/api/issues/12/comments":
			calls = append(calls, "create "+body["body"].(string))
			w.WriteHeader(201)
			w.Write([]byte(`{"id":99}`))
		case r.Method == "PATCH" && r.URL.Path == "/repos/acme/api/issues/comments/99":
			calls = append(calls, "edit "+body["body"].(string))
			w.Write([]byte(`{"id":99}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := &Client{API: srv.URL, Web: "https://github.example", HTTP: srv.Client(), Now: time.Now}
	tk := tokensWith(t, c, true)
	cm := Comments{C: c, Q: tk.Q, V: tk.V}
	id, err := cm.Comment(context.Background(), "acme/api", 12, 0, "Preview: x")
	if err != nil || id != 99 {
		t.Fatal(id, err)
	}
	if id, err = cm.Comment(context.Background(), "acme/api", 12, 99, "Preview removed."); err != nil || id != 99 {
		t.Fatal(id, err)
	}
	b, _ := json.Marshal(perms)
	if strings.Join(calls, "|") != "create Preview: x|edit Preview removed." || string(b) != `{"permissions":{"pull_requests":"write"},"repositories":["api"]}` {
		t.Fatalf("%q %s", calls, b)
	}
}

func TestStatus(t *testing.T) {
	var got, perms map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/acme/api/installation":
			w.Write([]byte(`{"id":77}`))
		case r.Method == "POST" && r.URL.Path == "/app/installations/77/access_tokens":
			perms = body
			w.WriteHeader(201)
			w.Write([]byte(`{"token":"ghs_st"}`))
		case r.Method == "POST" && r.URL.Path == "/repos/acme/api/statuses/c0ffee" && r.Header.Get("Authorization") == "Bearer ghs_st":
			got = body
			w.WriteHeader(201)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := &Client{API: srv.URL, Web: "https://github.example", HTTP: srv.Client(), Now: time.Now}
	tk := tokensWith(t, c, true)
	err := Statuses{C: c, Q: tk.Q, V: tk.V}.Status(context.Background(), "acme/api", "c0ffee", "failure", "yawl/production/api", "Health check failed", "https://panel/x")
	b, _ := json.Marshal(perms)
	if err != nil || got["state"] != "failure" || got["context"] != "yawl/production/api" || got["description"] != "Health check failed" || got["target_url"] != "https://panel/x" ||
		string(b) != `{"permissions":{"statuses":"write"},"repositories":["api"]}` {
		t.Fatalf("%v %v %s", err, got, b)
	}
}

func TestStatusRateLimitNotRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/acme/api/installation":
			w.Write([]byte(`{"id":77}`))
		case r.Method == "POST" && r.URL.Path == "/app/installations/77/access_tokens":
			w.WriteHeader(201)
			w.Write([]byte(`{"token":"ghs_st"}`))
		default:
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(403)
			w.Write([]byte(`{"message":"API rate limit exceeded"}`))
		}
	}))
	t.Cleanup(srv.Close)
	c := &Client{API: srv.URL, Web: "https://github.example", HTTP: srv.Client(), Now: time.Now}
	tk := tokensWith(t, c, true)
	err := Statuses{C: c, Q: tk.Q, V: tk.V}.Status(context.Background(), "acme/api", "c0ffee", "success", "yawl/production/api", "Live", "https://panel/x")
	if r, ok := err.(interface{ Refused() bool }); err == nil || !ok || r.Refused() {
		t.Fatalf("%v", err)
	}
}

func TestConvertEscapesCode(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.EscapedPath()
		http.Error(w, "no", 404)
	}))
	defer srv.Close()
	c := &Client{API: srv.URL, HTTP: srv.Client(), Now: time.Now}
	c.Convert(context.Background(), "a/../../user")
	if got != "/app-manifests/a%2F..%2F..%2Fuser/conversions" {
		t.Fatalf("path %q", got)
	}
}
