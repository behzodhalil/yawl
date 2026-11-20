package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func apiCLI(t *testing.T, h http.Handler) *bytes.Buffer {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	t.Setenv("YAWL_CONFIG", filepath.Join(dir, "config.json"))
	out := &bytes.Buffer{}
	stdout, stdin = out, strings.NewReader("")
	Run(context.Background(), "login", []string{srv.URL, "--token", "tok"})
	out.Reset()
	os.WriteFile(filepath.Join(dir, "yawl.yaml"), []byte("project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n"), 0o644)
	t.Chdir(dir)
	return out
}

func TestCLIDomains(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/services/acme/production/api/domains", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"host":"shop.acme.uz","dns":"wrong","dns_detail":"shop.acme.uz points to 1.2.3.4. Set an A record: shop.acme.uz → 5.6.7.8.","cert":"pending"}]`)
	})
	out := apiCLI(t, mux)
	if err := Run(context.Background(), "domains", []string{"api"}); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "shop.acme.uz") || !strings.Contains(s, "wrong") || !strings.Contains(s, "Set an A record") {
		t.Fatalf("output:\n%s", s)
	}
}
