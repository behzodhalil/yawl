package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeAPI(t *testing.T, applied *bool) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unknown API token", 401)
			return
		}
		switch r.URL.Path {
		case "/api/plan", "/api/apply":
			if r.URL.Path == "/api/apply" {
				*applied = true
			}
			io.WriteString(w, `[{"op":"unchanged","kind":"service","name":"api"},{"op":"create","kind":"service","name":"web"}]`)
		case "/api/services/acme/production/api/deploys":
			io.WriteString(w, `{"deploy_id":"dep_1"}`)
		case "/api/deploys/dep_1/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			for _, st := range []string{`"steps":[{"step":"build","state":"running"}],"state":"building"`,
				`"steps":[{"step":"build","state":"ok"},{"step":"start","state":"ok"},{"step":"check","state":"failed","output":"boom"}],"state":"failed","cause":"Your app listens on 127.0.0.1:10000. Bind to 0.0.0.0."`} {
				fmt.Fprintf(w, "data: {\"id\":\"dep_1\",\"service\":\"api\",%s}\n\n", st)
			}
		case "/api/services/acme/production/api/rollback":
			io.WriteString(w, `{"deploy_id":"dep_9","to":"dep_7","note":"The database is unchanged: dep_8 ran a release command."}`)
		case "/api/deploys/dep_9/stream":
			fmt.Fprint(w, "data: {\"id\":\"dep_9\",\"service\":\"api\",\"steps\":[{\"step\":\"start\",\"state\":\"ok\"}],\"state\":\"live\"}\n\n")
		case "/api/status":
			io.WriteString(w, `[{"project":"acme","env":"production","service":"api","kind":"web","url":"https://api-production.1-2-3-4.sslip.io","state":"live"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setup(t *testing.T) (out *bytes.Buffer, applied *bool) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("YAWL_CONFIG", filepath.Join(dir, "config.json"))
	applied = new(bool)
	srv := fakeAPI(t, applied)
	out = &bytes.Buffer{}
	stdout, stdin = out, strings.NewReader("")
	if err := Run(context.Background(), "login", []string{srv.URL, "--token", "tok"}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "config.json")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %v", fi.Mode().Perm())
	}
	os.WriteFile(filepath.Join(dir, "yawl.yaml"), []byte("project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n"), 0o644)
	t.Chdir(dir)
	return out, applied
}

func TestCLIApplyPrintsPlan(t *testing.T) {
	out, applied := setup(t)
	stdin = strings.NewReader("n\n")
	if err := Run(context.Background(), "apply", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "create  service  web") || *applied {
		t.Fatalf("applied=%v out:\n%s", *applied, out)
	}
	stdin = strings.NewReader("y\n")
	Run(context.Background(), "apply", nil)
	if !*applied {
		t.Fatal("y did not apply")
	}
}

func TestCLIApplyYes(t *testing.T) {
	_, applied := setup(t)
	if err := Run(context.Background(), "apply", []string{"--yes"}); err != nil || !*applied {
		t.Fatalf("applied=%v err=%v", *applied, err)
	}
}

func TestCLIDeployFailsWithCause(t *testing.T) {
	out, _ := setup(t)
	err := Run(context.Background(), "deploy", []string{"api"})
	if err == nil || err.Error() != "deploy failed: Your app listens on 127.0.0.1:10000. Bind to 0.0.0.0." {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(out.String(), "check") || !strings.Contains(out.String(), "failed") {
		t.Fatalf("steps not shown:\n%s", out)
	}
}

func TestCLIStatus(t *testing.T) {
	out, _ := setup(t)
	if err := Run(context.Background(), "status", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "https://api-production.1-2-3-4.sslip.io") {
		t.Fatalf("out:\n%s", out)
	}
}

func TestCLIRollback(t *testing.T) {
	out, _ := setup(t)
	if err := Run(context.Background(), "rollback", []string{"api"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Rolled back acme-api to dep_7.\nThe database is unchanged: dep_8 ran a release command.\n") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestUnknownCommand(t *testing.T) {
	if err := Run(context.Background(), "frobnicate", nil); !errors.Is(err, ErrUnknown) {
		t.Fatalf("%v", err)
	}
}

func TestCLIApplyWarnIsNoChange(t *testing.T) {
	out, _ := setup(t)
	applied := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/apply" {
			applied = true
		}
		io.WriteString(w, `[{"op":"unchanged","kind":"service","name":"api"},{"op":"warn","kind":"server","name":"hel1","detail":"limits add up to more than the machine"}]`)
	}))
	defer srv.Close()
	if err := Run(context.Background(), "login", []string{srv.URL, "--token", "tok"}); err != nil {
		t.Fatal(err)
	}
	stdin = strings.NewReader("y\n")
	if err := Run(context.Background(), "apply", nil); err != nil {
		t.Fatal(err)
	}
	if applied || !strings.Contains(out.String(), "Nothing to change.") || strings.Contains(out.String(), "Apply?") {
		t.Fatalf("applied=%v out:\n%s", applied, out)
	}
}
