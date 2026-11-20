package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

var built = fstest.MapFS{
	"dist/README":      {Data: []byte("placeholder")},
	"dist/index.html":  {Data: []byte(`<!doctype html><div id="root"></div>`)},
	"dist/assets/a.js": {Data: []byte("console.log(1)")},
}

func get(t *testing.T, h http.Handler, method, path string) (*http.Response, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	res := rec.Result()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

// mounted is the server's own arrangement: the API and GitHub own their prefixes, the panel the rest.
func mounted(panel http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", http.NotFoundHandler())
	mux.Handle("/github/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "github", http.StatusMethodNotAllowed) }))
	mux.Handle("/agent", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "agent", http.StatusUpgradeRequired) }))
	mux.Handle("/", panel)
	return mux
}

func TestPanelFallback(t *testing.T) {
	h := mounted(handler(built))
	for _, p := range []string{"/", "/p/acme/production", "/p/acme/production/s/api/deploys/dep_1", "/settings/telegram"} {
		res, b := get(t, h, "GET", p)
		if res.StatusCode != 200 || !strings.Contains(b, `<div id="root">`) || res.Header.Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s: %d %q %q", p, res.StatusCode, b, res.Header.Get("Cache-Control"))
		}
	}
	res, b := get(t, h, "GET", "/assets/a.js")
	if res.StatusCode != 200 || b != "console.log(1)" || res.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset: %d %q %v", res.StatusCode, b, res.Header)
	}
	for p, want := range map[string]int{"/assets/missing.js": 404, "/favicon.ico": 404, "/api/nope": 404, "/github/webhook": 405, "/agent": 426} {
		res, b := get(t, h, "GET", p)
		if res.StatusCode != want || strings.Contains(b, "root") {
			t.Fatalf("%s: %d %q", p, res.StatusCode, b)
		}
	}
	if res, _ := get(t, h, "POST", "/p/acme"); res.StatusCode != 405 {
		t.Fatalf("POST to the panel: %d", res.StatusCode)
	}
	if _, b := get(t, h, "GET", "/README"); strings.Contains(b, "placeholder") {
		t.Fatalf("the placeholder is served: %q", b)
	}
}

func TestPanelNotBuilt(t *testing.T) {
	res, b := get(t, handler(fstest.MapFS{"dist/README": {Data: []byte("x")}}), "GET", "/")
	if res.StatusCode != 200 || strings.TrimSpace(b) != "The dashboard is not built. Run make web." {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
}

func TestEmbedHasDist(t *testing.T) {
	if Handler() == nil {
		t.Fatal("no handler")
	}
}
