package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIExport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/environments/acme/staging/export" {
			io.WriteString(w, "project: acme\nenvironment: staging\n")
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("YAWL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	out := &bytes.Buffer{}
	stdout, stdin = out, strings.NewReader("")
	Run(context.Background(), "login", []string{srv.URL, "--token", "tok"})
	out.Reset()
	if err := Run(context.Background(), "export", []string{"acme/staging"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "project: acme\nenvironment: staging\n" {
		t.Fatalf("%q", out)
	}
	if err := Run(context.Background(), "export", []string{"acme"}); err == nil || !strings.Contains(err.Error(), "project/environment") {
		t.Fatalf("%v", err)
	}
}
