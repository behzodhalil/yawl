package build

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func repoOf(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	r, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := r.Worktree()
	w.AddGlob(".")
	if _, err := w.Commit("v1", &git.CommitOptions{All: true, Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInspectNode(t *testing.T) {
	dir := repoOf(t, map[string]string{
		"package.json": `{"engines":{"node":"18"},"scripts":{"start":"node index.js"}}`,
		".nvmrc":       "20\n",
		"index.js":     "const port = process.env.PORT\nconst db = process.env.DATABASE_URL\napp.get(\"/healthz\", ok)\napp.get('/status', ok)\n",
	})
	r, err := Inspect(context.Background(), Request{Repo: dir})
	if err != nil {
		t.Fatal(err)
	}
	if r.Dockerfile || r.Language != "node 20" || !r.ReadsPort || r.Port != 0 || r.HealthPath != "/healthz" || len(r.Commit) != 40 {
		t.Fatalf("%+v", r)
	}
	if !reflect.DeepEqual(r.Missing, []string{"DATABASE_URL"}) {
		t.Fatalf("missing %v", r.Missing)
	}
	if !reflect.DeepEqual(r.Warnings, []string{"package.json asks for Node 18; the build uses Node 20."}) {
		t.Fatalf("warnings %v", r.Warnings)
	}
	r, _ = Inspect(context.Background(), Request{Repo: dir, Keys: []string{"DATABASE_URL"}})
	if len(r.Missing) != 0 || r.Missing == nil {
		t.Fatalf("with the key set: %#v", r.Missing)
	}
}

func TestInspectDockerfile(t *testing.T) {
	dir := repoOf(t, map[string]string{
		"Dockerfile": "FROM golang:1.23\nEXPOSE 8080\nCMD [\"/app\"]\n",
		"main.go":    "package main\nfunc main() { http.ListenAndServe(\":8080\", nil) }\n",
	})
	r, err := Inspect(context.Background(), Request{Repo: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Dockerfile || r.Language != "" || r.ReadsPort || r.Port != 8080 || r.HealthPath != "/" {
		t.Fatalf("%+v", r)
	}
}

func TestInspectPython(t *testing.T) {
	dir := repoOf(t, map[string]string{"requirements.txt": "flask\n", "app.py": "import os\nos.environ.get(\"SECRET_KEY\")\n@app.route('/up')\n"})
	r, _ := Inspect(context.Background(), Request{Repo: dir})
	if r.Language != "python 3.12" || r.HealthPath != "/up" || !reflect.DeepEqual(r.Missing, []string{"SECRET_KEY"}) {
		t.Fatalf("%+v", r)
	}
}

func TestInspectCloneFails(t *testing.T) {
	_, err := Inspect(context.Background(), Request{Repo: "http://127.0.0.1:1/acme/api.git", Token: "ghs_secretvalue123"})
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1:1/acme/api.git") || strings.Contains(err.Error(), "ghs_secretvalue123") {
		t.Fatalf("%v", err)
	}
}

func TestInspectStopsAtItsLimit(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	old := inspectLimit
	inspectLimit = 300 * time.Millisecond
	defer func() { inspectLimit = old }()
	start := time.Now()
	if _, err := Inspect(context.Background(), Request{Repo: "http://" + l.Addr().String() + "/x.git"}); err == nil {
		t.Fatal("a silent remote was read")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestInspectClonesShallow(t *testing.T) {
	if o := cloneOptions(Request{Repo: "https://x/y", Depth: 1}); o.Depth != 1 {
		t.Fatalf("depth %d", o.Depth)
	}
}

func TestCommittedEnvIsNotMissing(t *testing.T) {
	files := map[string]string{
		"package.json": `{"scripts":{"start":"node index.js"}}`,
		".env":         "# Lovable\nVITE_SUPABASE_URL=\"https://x.supabase.co\"\nVITE_EMPTY=\n",
		"src/main.ts":  "import.meta.env.VITE_SUPABASE_URL; import.meta.env.VITE_EMPTY\n",
	}
	r, err := Inspect(context.Background(), Request{Repo: repoOf(t, files)})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Missing, []string{"VITE_EMPTY"}) {
		t.Fatalf("missing %v", r.Missing)
	}
	if got := Warnings(tree(t, files), nil, "production", "generate"); !reflect.DeepEqual(got, []string{"The code reads VITE_EMPTY, which is not set in production."}) {
		t.Fatalf("warnings %q", got)
	}
}
