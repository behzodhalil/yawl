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
	"time"
)

func TestCLIUpdateWaitsForTheNewVersion(t *testing.T) {
	current, gets := "1.1.0", 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/update":
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), `"version":"1.2.0"`) {
				http.Error(w, "bad body "+string(b), 400)
				return
			}
			w.WriteHeader(202)
			io.WriteString(w, `{"version":"1.2.0"}`)
		case "GET /api/update":
			if gets++; gets == 2 {
				current = "1.2.0"
			}
			io.WriteString(w, `{"current":"`+current+`"}`)
		}
	}))
	defer srv.Close()
	t.Setenv("YAWL_CONFIG", filepath.Join(t.TempDir(), "c.json"))
	out := &bytes.Buffer{}
	SetIO(out, strings.NewReader(""))
	Run(context.Background(), "login", []string{srv.URL, "--token", "tok"})
	updatePoll = time.Millisecond
	if err := Run(context.Background(), "update", []string{"--version", "v1.2.0"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Updating to v1.2.0. The panel restarts; apps keep running.") || !strings.Contains(out.String(), "The panel runs v1.2.0.") {
		t.Fatalf("%q", out.String())
	}
}
