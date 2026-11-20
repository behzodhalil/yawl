package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/behzodhalil/yawl/internal/server/vault"
)

type fakeIdP struct {
	srv       *httptest.Server
	key       *rsa.PrivateKey
	email     string
	verified  any
	nonce     string
	sub       string
	slash     string
	discovery int
}

func newIdP(t *testing.T) *fakeIdP {
	p := &fakeIdP{verified: true, sub: "u1"}
	p.key, _ = rsa.GenerateKey(rand.Reader, 2048)
	mux := http.NewServeMux()
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		p.discovery++
		json.NewEncoder(w).Encode(map[string]any{"issuer": p.srv.URL + p.slash, "authorization_endpoint": p.srv.URL + "/authorize",
			"token_endpoint": p.srv.URL + "/token", "jwks_uri": p.srv.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") != "good-code" || r.Form.Get("code_verifier") == "" {
			http.Error(w, "bad code", 400)
			return
		}
		sig, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
		claims := map[string]any{"iss": p.srv.URL + p.slash, "aud": "yawl-client", "sub": p.sub, "email": p.email,
			"nonce": p.nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
		if p.verified != nil {
			claims["email_verified"] = p.verified
		}
		tok, _ := jwt.Signed(sig).Claims(claims).Serialize()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": tok})
	})
	return p
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func ssoSignIn(t *testing.T, srv *httptest.Server, idp *fakeIdP, state func(string) string) *http.Response {
	t.Helper()
	res, err := noRedirect.Get(srv.URL + "/auth/sso/start")
	if err != nil || res.StatusCode != 302 {
		t.Fatalf("start: %v %v", res, err)
	}
	loc, _ := url.Parse(res.Header.Get("Location"))
	if !strings.HasPrefix(loc.String(), idp.srv.URL+"/authorize") || loc.Query().Get("code_challenge") == "" || loc.Query().Get("redirect_uri") != "http://panel.test/auth/sso/callback" {
		t.Fatalf("%s", loc)
	}
	idp.nonce = loc.Query().Get("nonce")
	req, _ := http.NewRequest("GET", srv.URL+"/auth/sso/callback?code=good-code&state="+url.QueryEscape(state(loc.Query().Get("state"))), nil)
	for _, c := range res.Cookies() {
		req.AddCookie(c)
	}
	back, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return back
}

func TestSSO(t *testing.T) {
	_, tok, a := setupApp(t)
	v, _ := vault.LoadOrCreate(t.TempDir())
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", Vault: v}))
	defer srv.Close()
	idp := newIdP(t)
	cfg := `{"name":"Acme Google","issuer":"` + idp.srv.URL + `","client_id":"yawl-client","client_secret":"cs-123","only":false}`
	if res, b := call(t, srv, tok, "PUT", "/api/settings/sso", cfg); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if _, b := call(t, srv, "", "GET", "/api/session/sso", ""); !strings.Contains(string(b), `"name":"Acme Google"`) || strings.Contains(string(b), "cs-123") {
		t.Fatalf("%s", b)
	}
	if _, b := call(t, srv, tok, "GET", "/api/settings/sso", ""); strings.Contains(string(b), "cs-123") {
		t.Fatalf("the secret shown: %s", b)
	}
	memberToken(t, srv, tok, "ana@acme.uz", "developer")
	flow := func(email string, verified any, state func(string) string) *http.Response {
		idp.email, idp.verified = email, verified
		return ssoSignIn(t, srv, idp, state)
	}
	same := func(s string) string { return s }
	session := func(r *http.Response) bool {
		for _, c := range r.Cookies() {
			if c.Name == "yawl_session" && c.Value != "" {
				return true
			}
		}
		return false
	}
	res := flow("Ana@acme.uz", true, same)
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !session(res) || !strings.Contains(string(body), `<meta http-equiv="refresh" content="0;url=/">`) {
		t.Fatalf("a member: %d %v %s", res.StatusCode, res.Header, body)
	}
	for name, r := range map[string]*http.Response{
		"not a member": flow("eve@evil.example", true, same),
		"unverified":   flow("ana@acme.uz", false, same),
		"forged state": flow("ana@acme.uz", true, func(string) string { return "forged" }),
	} {
		if session(r) || !strings.HasPrefix(r.Header.Get("Location"), "/sign-in?sso=") {
			t.Errorf("%s: %d %v", name, r.StatusCode, r.Header)
		}
	}
	if r := flow("ana@acme.uz", "true", same); !session(r) {
		t.Fatalf("a string true: %v", r.Header)
	}
	if r := flow("ana@acme.uz", nil, same); session(r) {
		t.Fatal("no email_verified signed in")
	}
	idp.sub = "someone-else"
	if r := flow("ana@acme.uz", true, same); session(r) || r.Header.Get("Location") != "/sign-in?sso=other" {
		t.Fatalf("another subject: %v", r.Header)
	}
	idp.sub = "u1"
	testDB.Exec(`UPDATE owner SET totp_secret = 'JBSWY3DPEHPK3PXP' WHERE email = 'ana@acme.uz'`)
	if r := flow("ana@acme.uz", true, same); session(r) || !strings.HasPrefix(r.Header.Get("Location"), "/sign-in?sso=code#t=") {
		t.Fatalf("two-step: %v", r.Header)
	}
	testDB.Exec(`UPDATE owner SET totp_secret = NULL WHERE email = 'ana@acme.uz'`)
	if idp.discovery > 2 {
		t.Fatalf("discovery read %d times", idp.discovery)
	}
	if _, b := call(t, srv, tok, "GET", "/api/settings/sso", ""); !strings.Contains(string(b), `"callback":"http://panel.test/auth/sso/callback"`) {
		t.Fatalf("%s", b)
	}
	ctx := context.Background()
	inv, _, _ := a.Invite(ctx, "admin", "own_admin")
	if _, err := a.AcceptInvite(ctx, inv, "boss@acme.uz", "boss-password-123"); err != nil {
		t.Fatal(err)
	}
	a.NewSession(ctx, "own_developer")
	a.NewSession(ctx, "own_admin")
	if res, b := call(t, srv, tok, "PUT", "/api/settings/sso", strings.Replace(cfg, `"only":false`, `"only":true`, 1)); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	testDB.Exec(`UPDATE owner SET role = 'viewer' WHERE email = 'admin@acme.uz'`)
	var n, mine int
	testDB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE owner_id = 'own_developer'`).Scan(&n)
	testDB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE owner_id = 'own_admin'`).Scan(&mine)
	if n != 0 || mine != 1 {
		t.Fatalf("sessions kept: ana %d, the Admin who turned it on %d", n, mine)
	}
	res, b := call(t, srv, "", "POST", "/api/session", `{"email":"ana@acme.uz","password":"any-password-123"}`)
	if res.StatusCode != 403 || !strings.Contains(string(b), "Acme Google") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b2 := call(t, srv, "", "POST", "/api/session", `{"email":"boss@acme.uz","password":"wrong-password-123"}`); res.StatusCode != 403 || string(b2) != string(b) {
		t.Fatalf("the break-glass account: %d %s", res.StatusCode, b2)
	}
	if res, _ := call(t, srv, "", "POST", "/api/session", `{"email":"boss@acme.uz","password":"boss-password-123"}`); res.StatusCode != 200 {
		t.Fatalf("the oldest Admin: %d", res.StatusCode)
	}
	for i := 0; i < 10; i++ {
		res, _ = call(t, srv, "", "POST", "/api/session", `{"email":"ana@acme.uz","password":"any-password-123"}`)
	}
	if res.StatusCode != 429 {
		t.Fatalf("refusals not counted: %d", res.StatusCode)
	}
}

func TestSSOIssuerHTTPS(t *testing.T) {
	_, tok, a := setupApp(t)
	v, _ := vault.LoadOrCreate(t.TempDir())
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", Vault: v}))
	defer srv.Close()
	cfg := `{"name":"Acme","issuer":"http://idp.example.com","client_id":"c","client_secret":"cs","only":false}`
	if res, b := call(t, srv, tok, "PUT", "/api/settings/sso", cfg); res.StatusCode != 400 || !strings.Contains(string(b), "https://") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestSSOIssuerSlash(t *testing.T) {
	_, tok, a := setupApp(t)
	v, _ := vault.LoadOrCreate(t.TempDir())
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", Vault: v}))
	defer srv.Close()
	idp := newIdP(t)
	idp.slash = "/"
	cfg := `{"name":"Auth0","issuer":"` + idp.srv.URL + `/","client_id":"yawl-client","client_secret":"cs","only":false}`
	if res, b := call(t, srv, tok, "PUT", "/api/settings/sso", cfg); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestSSOOnlyNeedsASignIn(t *testing.T) {
	_, tok, a := setupApp(t)
	v, _ := vault.LoadOrCreate(t.TempDir())
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", Vault: v}))
	defer srv.Close()
	idp := newIdP(t)
	cfg := `{"name":"Acme Google","issuer":"` + idp.srv.URL + `","client_id":"yawl-client","client_secret":"cs-123","only":false}`
	if res, b := call(t, srv, tok, "PUT", "/api/settings/sso", cfg); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	on := strings.Replace(cfg, `"only":false`, `"only":true`, 1)
	if res, b := call(t, srv, tok, "PUT", "/api/settings/sso", on); res.StatusCode != 400 || !strings.Contains(string(b), "Sign in with SSO once") {
		t.Fatalf("before a sign-in: %d %s", res.StatusCode, b)
	}
	if _, b := call(t, srv, tok, "GET", "/api/settings/sso", ""); !strings.Contains(string(b), `"works":false`) || !strings.Contains(string(b), `"only":false`) {
		t.Fatalf("%s", b)
	}
	memberToken(t, srv, tok, "ana@acme.uz", "developer")
	idp.email, idp.verified = "eve@evil.example", true
	ssoSignIn(t, srv, idp, func(s string) string { return s })
	if _, b := call(t, srv, tok, "GET", "/api/settings/sso", ""); !strings.Contains(string(b), `"works":false`) {
		t.Fatalf("after a refused sign-in: %s", b)
	}
	idp.email = "ana@acme.uz"
	if r := ssoSignIn(t, srv, idp, func(s string) string { return s }); r.StatusCode != 200 {
		t.Fatalf("sign-in: %d %v", r.StatusCode, r.Header)
	}
	if _, b := call(t, srv, tok, "GET", "/api/settings/sso", ""); !strings.Contains(string(b), `"works":true`) {
		t.Fatalf("%s", b)
	}
	if res, b := call(t, srv, tok, "PUT", "/api/settings/sso", on); res.StatusCode != 204 {
		t.Fatalf("after a sign-in: %d %s", res.StatusCode, b)
	}
	other := newIdP(t)
	if res, b := call(t, srv, tok, "PUT", "/api/settings/sso", strings.Replace(on, idp.srv.URL, other.srv.URL, 1)); res.StatusCode != 400 || !strings.Contains(string(b), "Sign in with SSO once") {
		t.Fatalf("a new issuer: %d %s", res.StatusCode, b)
	}
	testDB.Exec(`UPDATE settings SET value = '' WHERE key = 'sso.works'`)
	if res, b := call(t, srv, tok, "PUT", "/api/settings/sso", strings.Replace(on, "Acme Google", "Acme", 1)); res.StatusCode != 204 {
		t.Fatalf("a rename: %d %s", res.StatusCode, b)
	}
}
