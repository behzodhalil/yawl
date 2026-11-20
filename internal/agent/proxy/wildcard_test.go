package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

type recAdmin struct {
	mu      sync.Mutex
	calls   []string
	code    int
	deleted bool
}

func (a *recAdmin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, _ := io.ReadAll(r.Body)
	a.calls = append(a.calls, r.Method+" "+r.URL.Path+" "+string(b))
	switch {
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/id/") && a.deleted:
	case (r.Method == http.MethodPatch || r.Method == http.MethodDelete) && strings.HasPrefix(r.URL.Path, "/id/"):
		http.Error(w, "unknown object ID", http.StatusNotFound)
	case r.URL.Path == "/config/apps/tls" && a.code != 0:
		http.Error(w, `loading module 'dns.providers.cloudflare': unknown module`, a.code)
	}
}

func startRecAdmin(t *testing.T) (string, *recAdmin) {
	t.Helper()
	dir, _ := os.MkdirTemp("/tmp", "yw") // socket paths must stay short on macOS
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "admin.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	a := &recAdmin{}
	srv := &http.Server{Handler: a}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return sock, a
}

func TestEnsureWildcard(t *testing.T) {
	sock, a := startRecAdmin(t)
	c := NewCaddy(nil, sock)
	ctx := context.Background()
	if err := c.EnsureWildcard(ctx); err != nil || len(a.calls) != 1 || a.calls[0] != "DELETE /id/wildcard null" {
		t.Fatalf("off and never on: %v %v", err, a.calls)
	}
	a.calls = nil
	c.Wildcard, c.DNSToken = "*.app.useyawl.com", "cf-secret"
	if err := c.EnsureWildcard(ctx); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(a.calls, "\n")
	for _, want := range []string{
		`POST /config/apps/tls {"automation":{"policies":[{"issuers":[{"challenges":{"dns":{"provider":{"api_token":"{env.CLOUDFLARE_API_TOKEN}","name":"cloudflare"}}},"module":"acme"}],"subjects":["*.app.useyawl.com"]}]}}`,
		`POST /config/apps/http/servers/https/automatic_https/prefer_wildcard true`,
		`POST /config/apps/http/servers/https/routes {"@id":"wildcard","match":[{"host":["*.app.useyawl.com"]}]}`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %s in\n%s", want, all)
		}
	}
	if strings.Contains(all, "cf-secret") {
		t.Fatal("the token reached Caddy's config")
	}
	a.code = 400
	if err := c.EnsureWildcard(ctx); err == nil || !strings.Contains(err.Error(), "unknown module") {
		t.Fatalf("%v", err)
	}
	a.code, a.calls, a.deleted = 0, nil, true
	c.Wildcard = ""
	if err := c.EnsureWildcard(ctx); err != nil || strings.Join(a.calls, ",") != "DELETE /id/wildcard null,DELETE /config/apps/tls null,POST /config/apps/http/servers/https/automatic_https/prefer_wildcard false" {
		t.Fatalf("%v %v", err, a.calls)
	}
}

func TestCaddySpec(t *testing.T) {
	c := NewCaddy(nil, "/run/yawl/caddy-admin.sock")
	s := c.spec("/run/yawl")
	if s.Image != "caddy:2" || len(s.Env) != 0 {
		t.Fatalf("%+v", s)
	}
	c.Image, c.DNSToken = "registry.test/caddy-cloudflare:2.10", "cf-secret"
	if s := c.spec("/run/yawl"); s.Image != "registry.test/caddy-cloudflare:2.10" || len(s.Env) != 0 {
		t.Fatalf("a token without a wildcard: %+v", s)
	}
	c.Wildcard = "*.app.useyawl.com"
	s = c.spec("/run/yawl")
	if !slices.Equal(s.Env, []string{"CLOUDFLARE_API_TOKEN=cf-secret"}) || slices.ContainsFunc(s.Cmd, func(a string) bool { return strings.Contains(a, "cf-secret") }) {
		t.Fatalf("%+v", s)
	}
}
