package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const wantCSP = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

func TestHeaders(t *testing.T) {
	inner := http.NewServeMux()
	inner.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("page")) })
	inner.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("[]")) })
	inner.HandleFunc("/agent", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(426) })
	h := Headers(inner)
	for _, p := range []string{"/", "/api/status", "/nope/x.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Header().Get("Content-Security-Policy") != wantCSP || rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("%s: %v", p, rec.Header())
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/agent", nil))
	if rec.Header().Get("Content-Security-Policy") != "" || rec.Code != 426 {
		t.Fatalf("agent: %d %v", rec.Code, rec.Header())
	}
}
