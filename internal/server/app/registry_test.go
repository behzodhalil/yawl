package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRealmAllowed(t *testing.T) {
	for _, c := range []struct {
		api, realm string
		want       bool
	}{
		{"registry-1.docker.io", "auth.docker.io", true},
		{"registry.gitlab.com", "gitlab.com", true},
		{"ghcr.io", "ghcr.io", true},
		{"registry.example.com", "gitlab.example.com", true},
		{"ghcr.io", "evil.com", false},
		{"ghcr.io", "token.ghcr.io.evil.com", false},
		{"10.0.0.5:5000", "10.0.0.6:5000", false},
		{"ghcr.io", "ghcr.io:8443", false},
	} {
		if got := realmAllowed(c.api, c.realm); got != c.want {
			t.Errorf("realmAllowed(%q, %q) = %v", c.api, c.realm, got)
		}
	}
}

func TestCheckRegistry(t *testing.T) {
	var challenge string
	tokenHits, v2Hits := 0, 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		good := ok && u == "u" && p == "t"
		switch r.URL.Path {
		case "/v2/":
			v2Hits++
			if strings.HasPrefix(challenge, "Basic") && good {
				return
			}
			w.Header().Set("WWW-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
		case "/token":
			tokenHits++
			if !good || r.URL.Query().Get("service") != "reg" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Write([]byte(`{"token":"x"}`))
		}
	}))
	defer srv.Close()
	host, ctx := srv.Listener.Addr().String(), context.Background()

	challenge = `Bearer realm="https://` + host + `/token",service="reg"`
	if err := CheckRegistry(ctx, srv.Client(), host, "u", "t"); err != nil {
		t.Fatalf("bearer: %v", err)
	}
	if err := CheckRegistry(ctx, srv.Client(), host, "u", "wrong"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("wrong token: %v", err)
	}

	tokenHits = 0
	far := strings.Replace(host, "127.0.0.1", "localhost", 1)
	challenge = `Bearer realm="https://` + far + `/token",service="reg"`
	if err := CheckRegistry(ctx, srv.Client(), host, "u", "t"); err == nil || !strings.Contains(err.Error(), "asks for the login at") || !strings.Contains(err.Error(), far) || tokenHits != 0 {
		t.Fatalf("realm on another host: %v, token asked %d times", err, tokenHits)
	}
	challenge = `Bearer realm="http://` + host + `/token",service="reg"`
	if err := CheckRegistry(ctx, srv.Client(), host, "u", "t"); err == nil || !strings.Contains(err.Error(), "asks for the login at") || tokenHits != 0 {
		t.Fatalf("realm over http: %v, token asked %d times", err, tokenHits)
	}

	challenge = `Basic realm="reg"`
	if err := CheckRegistry(ctx, srv.Client(), host, "u", "t"); err != nil {
		t.Fatalf("basic: %v", err)
	}
	if err := CheckRegistry(ctx, srv.Client(), host, "u", "wrong"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("basic, wrong token: %v", err)
	}

	// "ghcr.io@" makes the URL's host the part after it: never sign in there.
	v2Hits = 0
	if err := CheckRegistry(ctx, srv.Client(), "ghcr.io@"+host, "u", "t"); err == nil || v2Hits != 0 {
		t.Fatalf("userinfo in host: %v, /v2/ asked %d times", err, v2Hits)
	}
}
