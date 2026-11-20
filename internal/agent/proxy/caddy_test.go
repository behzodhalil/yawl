package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeAdmin struct {
	mu     sync.Mutex
	routes map[string][]map[string]any
}

func startFakeAdmin(t *testing.T) (sock string, fa *fakeAdmin) {
	t.Helper()
	dir, _ := os.MkdirTemp("/tmp", "yc") // socket paths must stay short on macOS
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock = filepath.Join(dir, "admin.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	fa = &fakeAdmin{routes: map[string][]map[string]any{}}
	srv := &http.Server{Handler: fa}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return sock, fa
}

func (fa *fakeAdmin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	switch {
	case strings.HasPrefix(r.URL.Path, "/id/"):
		id := strings.TrimPrefix(r.URL.Path, "/id/")
		for name, rs := range fa.routes {
			for i, rt := range rs {
				if rt["@id"] == id {
					if r.Method == http.MethodDelete {
						fa.routes[name] = append(rs[:i:i], rs[i+1:]...)
						return
					}
					var nr map[string]any
					json.Unmarshal(body, &nr)
					fa.routes[name][i] = nr
					return
				}
			}
		}
		http.Error(w, "unknown object ID", http.StatusNotFound)
	case strings.HasSuffix(r.URL.Path, "/routes/0") && r.Method == http.MethodPut:
		name := strings.Split(r.URL.Path, "/")[5]
		var nr map[string]any
		json.Unmarshal(body, &nr)
		fa.routes[name] = append([]map[string]any{nr}, fa.routes[name]...)
	case strings.HasSuffix(r.URL.Path, "/routes") && r.Method == http.MethodPost:
		name := strings.Split(r.URL.Path, "/")[5]
		var nr map[string]any
		json.Unmarshal(body, &nr)
		fa.routes[name] = append(fa.routes[name], nr)
	case strings.HasSuffix(r.URL.Path, "/routes") && r.Method == http.MethodGet:
		name := strings.Split(r.URL.Path, "/")[5]
		json.NewEncoder(w).Encode(fa.routes[name])
	default:
		http.NotFound(w, r)
	}
}

func TestSetRouteBody(t *testing.T) {
	sock, fa := startFakeAdmin(t)
	p := NewCaddy(nil, sock)
	ctx := context.Background()
	if err := p.SetRoute(ctx, "api-production.1-2-3-4.sslip.io", []string{"172.18.0.5:10000"}, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := p.SetRoute(ctx, "api-production.1-2-3-4.sslip.io", []string{"172.18.0.6:10000"}, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, server := range []string{"https", "http"} {
		if n := len(fa.routes[server]); n != 1 {
			t.Fatalf("%s: %d routes, want 1 (the second SetRoute replaces)", server, n)
		}
	}
	got, err := p.Routes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got["api-production.1-2-3-4.sslip.io"] != "172.18.0.6:10000" {
		t.Fatalf("routes %v", got)
	}
}

func TestSwitchFailsKeepsOldRoute(t *testing.T) {
	sock, fa := startFakeAdmin(t)
	ctx := context.Background()
	if err := NewCaddy(nil, sock).SetRoute(ctx, "h.example", []string{"10.0.0.1:1"}, Options{}); err != nil {
		t.Fatal(err)
	}
	broken := NewCaddy(nil, filepath.Join(filepath.Dir(sock), "missing.sock"))
	err := broken.SetRoute(ctx, "h.example", []string{"10.0.0.2:1"}, Options{})
	if err == nil || !strings.Contains(err.Error(), "caddy admin") {
		t.Fatalf("want a caddy admin error, got %v", err)
	}
	got, _ := NewCaddy(nil, sock).Routes(ctx)
	if got["h.example"] != "10.0.0.1:1" {
		t.Fatalf("old route lost: %v (%v)", got, fa.routes)
	}
}

func TestRouteHandle(t *testing.T) {
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	plain := js(routeHandle([]string{"10.0.0.5:8080"}, Options{}))
	if plain != `[{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.5:8080"}]}]` {
		t.Fatalf("plain %s", plain)
	}
	all := js(routeHandle([]string{"10.0.0.5:8080"}, Options{NoIndex: true, BasicUser: "ops", BasicHash: "JDJhJDEw"}))
	for _, want := range []string{`"handler":"authentication"`, `"password":"JDJhJDEw","username":"ops"`, `"algorithm":"bcrypt"`, `"hash_cache":{}`, `"X-Robots-Tag":["noindex"]`, `"handler":"reverse_proxy"`} {
		if !strings.Contains(all, want) {
			t.Errorf("lacks %s in %s", want, all)
		}
	}
	if strings.Index(all, "authentication") > strings.Index(all, "reverse_proxy") {
		t.Fatal("the password is asked after the app answers")
	}
	m := js(routeHandle([]string{"10.0.0.5:8080"}, Options{Maintenance: "shop", BasicUser: "ops", BasicHash: "x"}))
	if !strings.Contains(m, `"status_code":503`) || !strings.Contains(m, "shop is down for maintenance") || strings.Contains(m, "reverse_proxy") || strings.Contains(m, "authentication") {
		t.Fatalf("maintenance %s", m)
	}
	r := js(redirectHandle("acme.uz"))
	if r != `[{"handler":"static_response","headers":{"Cache-Control":["max-age=3600"],"Location":["https://acme.uz{http.request.uri}"]},"status_code":301}]` {
		t.Fatalf("redirect %s", r)
	}
}

func TestStatusHandle(t *testing.T) {
	b, _ := json.Marshal(routeHandle([]string{"10.0.0.5:8080"}, Options{Status: "/run/yawl/status/sp_a.html", BasicUser: "ops", BasicHash: "x"}))
	got := string(b)
	for _, want := range []string{`"handler":"file_server","root":"/run/yawl/status"`, `"uri":"/sp_a.html"`, `"Cache-Control":["public, max-age=30"]`, `default-src 'none'`} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %s in %s", want, got)
		}
	}
	if strings.Contains(got, "reverse_proxy") || strings.Contains(got, "authentication") {
		t.Fatalf("%s", got)
	}
}

func TestHandleForHTTPWithPassword(t *testing.T) {
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	o := Options{BasicUser: "ops", BasicHash: "x"}
	if h := js(handleFor("http", "a.example", []string{"10.0.0.5:80"}, o)); strings.Contains(h, "authentication") || !strings.Contains(h, `"status_code":308`) || !strings.Contains(h, "https://{http.request.host}{http.request.uri}") {
		t.Fatalf("http %s", h)
	}
	if h := js(handleFor("https", "a.example", []string{"10.0.0.5:80"}, o)); !strings.Contains(h, "authentication") {
		t.Fatalf("https %s", h)
	}
	if h := js(handleFor("http", "a.example", []string{"10.0.0.5:80"}, Options{})); !strings.Contains(h, "reverse_proxy") {
		t.Fatalf("http without a password %s", h)
	}
}

func TestSetRouteReplicas(t *testing.T) {
	sock, fa := startFakeAdmin(t)
	p := NewCaddy(nil, sock)
	if err := p.SetRoute(context.Background(), "api.example", []string{"10.0.0.1:80", "10.0.0.2:80"}, Options{NoIndex: true}); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(fa.routes["https"][0]["handle"])
	for _, want := range []string{`"dial":"10.0.0.1:80"`, `"dial":"10.0.0.2:80"`, `"selection_policy":{"policy":"round_robin"}`, `"try_duration":"5s"`, `"fail_duration":"10s"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("no %s in %s", want, b)
		}
	}
}

func TestPathRoute(t *testing.T) {
	sock, fa := startFakeAdmin(t)
	p := NewCaddy(nil, sock)
	ctx := context.Background()
	ids := func(srv string) (out []string) {
		for _, r := range fa.routes[srv] {
			out = append(out, r["@id"].(string))
		}
		return out
	}
	for _, step := range []func() error{
		func() error { return p.SetRoute(ctx, "acme.uz", []string{"10.0.0.1:80"}, Options{}) },
		func() error { return p.SetRedirect(ctx, "acme.uz") },
		func() error { return p.SetRoute(ctx, "acme.uz/api", []string{"10.0.0.2:80"}, Options{NoIndex: true}) },
		func() error { return p.SetRoute(ctx, "acme.uz/api", []string{"10.0.0.3:80"}, Options{}) },
		func() error { return p.SetRoute(ctx, "shop.example", []string{"10.0.0.4:80"}, Options{}) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(ids("https"), " "); got != "route-https-acme.uz~api route-https-acme.uz route-https-shop.example" {
		t.Fatalf("https: %s", got)
	}
	if got := strings.Join(ids("http"), " "); got != "route-http-acme.uz~api redirect-acme.uz route-http-acme.uz route-http-shop.example" {
		t.Fatalf("http: %s", got)
	}
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	r := fa.routes["https"][0]
	if m := js(r["match"]); m != `[{"host":["acme.uz"],"path":["/api","/api/*"]}]` {
		t.Fatalf("match %s", m)
	}
	h := js(r["handle"])
	if !strings.HasPrefix(h, `[{"handler":"rewrite","strip_path_prefix":"/api"}`) ||
		!strings.Contains(h, `"handler":"reverse_proxy","headers":{"request":{"set":{"X-Forwarded-Prefix":["/api"]}}},"upstreams":[{"dial":"10.0.0.3:80"}]`) {
		t.Fatalf("handle %s", h)
	}
	if h := js(fa.routes["https"][1]["handle"]); strings.Contains(h, "rewrite") || strings.Contains(h, "X-Forwarded-Prefix") {
		t.Fatalf("the host's own route got the path's handlers: %s", h)
	}
	if m := js(handleFor("https", "acme.uz/api", []string{"10.0.0.3:80"}, Options{Maintenance: "api"})); strings.Contains(m, "rewrite") || !strings.Contains(m, `"status_code":503`) {
		t.Fatalf("maintenance under a path %s", m)
	}
	if err := p.SetRedirect(ctx, "acme.uz/api"); err != nil {
		t.Fatal(err)
	}
	if r := fa.routes["http"][0]; r["@id"] != "redirect-acme.uz~api" || js(r["match"]) != `[{"host":["acme.uz"],"path":["/api","/api/*"]}]` {
		t.Fatalf("redirect %v", r)
	}
	if err := p.RemoveRoute(ctx, "acme.uz/api"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ids("https"), " ") + " | " + strings.Join(ids("http"), " "); got != "route-https-acme.uz route-https-shop.example | redirect-acme.uz route-http-acme.uz route-http-shop.example" {
		t.Fatalf("after removal: %s", got)
	}
}
