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

func TestPowerCommands(t *testing.T) {
	var posts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			posts = append(posts, r.URL.Path)
			if strings.HasSuffix(r.URL.Path, "/stop") {
				w.WriteHeader(202)
				io.WriteString(w, `{"note":""}`)
				return
			}
			io.WriteString(w, `{"deploy_id":"dep_r"}`)
		case r.URL.Path == "/api/deploys/dep_r/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"id\":\"dep_r\",\"service\":\"api\",\"state\":\"live\",\"steps\":[]}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("YAWL_CONFIG", filepath.Join(dir, "config.json"))
	out := &bytes.Buffer{}
	stdout, stdin = out, strings.NewReader("")
	Run(context.Background(), "login", []string{srv.URL, "--token", "tok"})
	os.WriteFile(filepath.Join(dir, "yawl.yaml"), []byte("project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: worker, image: x}\n"), 0o644)
	t.Chdir(dir)
	out.Reset()
	for _, cmd := range []string{"restart", "stop", "start"} {
		if err := Run(context.Background(), cmd, []string{"api"}); err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
	}
	if strings.Join(posts, " ") != "/api/services/acme/production/api/restart /api/services/acme/production/api/stop /api/services/acme/production/api/start" {
		t.Fatalf("%v", posts)
	}
	if !strings.Contains(out.String(), "Stopped api.") {
		t.Fatalf("%q", out)
	}
}

func TestDeployNoCacheFlag(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			b, _ := io.ReadAll(r.Body)
			body = string(b)
			io.WriteString(w, `{"deploy_id":"dep_r"}`)
		case r.URL.Path == "/api/deploys/dep_r/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"id\":\"dep_r\",\"service\":\"api\",\"state\":\"live\",\"steps\":[]}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("YAWL_CONFIG", filepath.Join(dir, "config.json"))
	stdout, stdin = &bytes.Buffer{}, strings.NewReader("")
	Run(context.Background(), "login", []string{srv.URL, "--token", "tok"})
	os.WriteFile(filepath.Join(dir, "yawl.yaml"), []byte("project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: worker, image: x}\n"), 0o644)
	t.Chdir(dir)
	if err := Run(context.Background(), "deploy", []string{"api", "--no-cache"}); err != nil {
		t.Fatal(err)
	}
	if body != `{"commit":"","no_cache":true}` {
		t.Fatalf("%s", body)
	}
}

func TestDeployByTag(t *testing.T) {
	var posted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			name := strings.Split(r.URL.Path, "/")[5]
			posted = append(posted, name)
			io.WriteString(w, `{"deploy_id":"dep_`+name+`"}`)
		case strings.HasSuffix(r.URL.Path, "/stream"):
			name := strings.TrimPrefix(strings.Split(r.URL.Path, "/")[3], "dep_")
			io.WriteString(w, "data: {\"service\":\""+name+"\",\"state\":\"live\",\"steps\":[]}\n\n")
		case r.URL.Path == "/api/status":
			io.WriteString(w, `[{"project":"acme","env":"production","service":"site"},{"project":"acme","env":"production","service":"jobs","tags":["front"]},`+
				`{"project":"acme","env":"production","service":"web","tags":["front"]},{"project":"acme","env":"staging","service":"other","tags":["front"]}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("YAWL_CONFIG", filepath.Join(dir, "config.json"))
	stdout, stdin = &bytes.Buffer{}, strings.NewReader("")
	Run(context.Background(), "login", []string{srv.URL, "--token", "tok"})
	os.WriteFile(filepath.Join(dir, "yawl.yaml"), []byte("project: acme\nenvironment: production\nserver: hel1\nservices:\n"+
		"  - {name: web, kind: worker, image: x, tags: [front]}\n  - {name: jobs, kind: worker, image: x}\n  - {name: site, kind: worker, image: x, tags: [front]}\n"), 0o644)
	t.Chdir(dir)
	if err := Run(context.Background(), "deploy", []string{"--tag", "front"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(posted, ",") != "web,jobs" {
		t.Fatalf("%v", posted)
	}
	if err := Run(context.Background(), "deploy", []string{"--tag", "none"}); err == nil || !strings.Contains(err.Error(), `no service in acme/production has the tag "none"`) {
		t.Fatalf("%v", err)
	}
	posted = nil
	if err := Run(context.Background(), "deploy", []string{"--tag", "front", "--commit", "abc123"}); err == nil || !strings.Contains(err.Error(), "--commit names one service's commit") || len(posted) != 0 {
		t.Fatalf("%v %v", err, posted)
	}
}
