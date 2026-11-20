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

func ghCLI(t *testing.T, reply string) *bytes.Buffer {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/github/connect" || r.Header.Get("Authorization") != "Bearer tok" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("YAWL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	out := &bytes.Buffer{}
	stdout, stdin = out, strings.NewReader("")
	Run(context.Background(), "login", []string{srv.URL, "--token", "tok"})
	out.Reset()
	return out
}

func TestCLIGitHubConnect(t *testing.T) {
	out := ghCLI(t, `{"url":"https://yawl.example.com/github/setup?state=abc"}`)
	if err := Run(context.Background(), "github", []string{"connect"}); err != nil {
		t.Fatal(err)
	}
	if want := "Open this link in your browser within 10 minutes:\nhttps://yawl.example.com/github/setup?state=abc\n"; out.String() != want {
		t.Fatalf("out %q", out)
	}
}

func TestCLIGitHubConnected(t *testing.T) {
	out := ghCLI(t, `{"connected":true,"install_url":"https://github.com/apps/yawl-ab12/installations/new"}`)
	if err := Run(context.Background(), "github", []string{"connect"}); err != nil {
		t.Fatal(err)
	}
	if want := "GitHub is connected. To give Yawl more repositories: https://github.com/apps/yawl-ab12/installations/new\n"; out.String() != want {
		t.Fatalf("out %q", out)
	}
}
