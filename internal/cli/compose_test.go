package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/behzodhalil/yawl/internal/seal"
)

const shopCompose = `services:
  app:
    build: .
    ports: ["8080:3000"]
    command: npm run start
    environment:
      DATABASE_URL: postgres://app:pw@db:5432/app
      SECRET: abc-secret-value
  db:
    image: postgres:16
`

func composeRepo(t *testing.T, yawlYAML string) string {
	return composeRepoAt(t, yawlYAML, "https://github.com/acme/shop.git")
}

func composeRepoAt(t *testing.T, yawlYAML, originURL string) string {
	dir := filepath.Join(t.TempDir(), "shop")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(shopCompose), 0o644)
	if yawlYAML != "" {
		os.WriteFile(filepath.Join(dir, "yawl.yaml"), []byte(yawlYAML), 0o644)
	}
	r, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	r.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{originURL}})
	w, _ := r.Worktree()
	w.Add("docker-compose.yml")
	if _, err := w.Commit("v1", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

type composeAPI struct {
	plan       string
	failPut    string
	agentPub   seal.PublicKey
	agentPriv  seal.PrivateKey
	recPub     seal.PublicKey
	applied    string
	puts       map[string]map[string]any
	everything bytes.Buffer
}

func composeServer(t *testing.T) *composeAPI {
	a := &composeAPI{puts: map[string]map[string]any{}}
	a.agentPub, a.agentPriv, _ = seal.GenerateKey()
	a.recPub, _, _ = seal.GenerateKey()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		a.everything.Write(body)
		switch {
		case r.URL.Path == "/api/plan":
			if a.plan == "" {
				a.plan = `[{"op":"create","kind":"service","name":"app"},{"op":"create","kind":"service","name":"db"}]`
			}
			io.WriteString(w, a.plan)
		case r.Method == "PUT" && r.URL.Path == a.failPut:
			http.Error(w, "boom", http.StatusInternalServerError)
		case r.URL.Path == "/api/apply":
			a.applied = string(body)
			io.WriteString(w, `[]`)
		case r.URL.Path == "/api/servers":
			io.WriteString(w, `[{"name":"hel1","public_ip":"1.2.3.4"}]`)
		case strings.HasSuffix(r.URL.Path, "/keys"):
			json.NewEncoder(w).Encode(map[string]string{"agent_pubkey": a.agentPub.String(), "recovery_pubkey": a.recPub.String(), "server": "hel1"})
		case r.Method == "PUT":
			var m map[string]any
			json.Unmarshal(body, &m)
			a.puts[r.URL.Path] = m
			io.WriteString(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("YAWL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	stdout, stdin = &bytes.Buffer{}, strings.NewReader("")
	Run(context.Background(), "login", []string{srv.URL, "--token", "tok"})
	return a
}

func TestComposeImportWrites(t *testing.T) {
	a := composeServer(t)
	dir := composeRepo(t, "")
	out := &bytes.Buffer{}
	stdout = out
	if err := Run(context.Background(), "compose", []string{"import", "--server", "hel1"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "yawl.yaml"))
	y := string(b)
	for _, want := range []string{"project: shop\n", "environment: production\n", "server: hel1\n", "name: app", "kind: web",
		"repo: https://github.com/acme/shop.git", "branch: master", "port: 3000", "command: [sh, -c, npm run start]", "name: db", "kind: postgres", "version: 16"} {
		if !strings.Contains(y, want) {
			t.Errorf("yawl.yaml lacks %q:\n%s", want, y)
		}
	}
	for _, want := range []string{"yawl env set app DATABASE_URL --ref db.url", "yawl env set app SECRET"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out.String(), "command is ignored") {
		t.Errorf("an app's command is noted as ignored:\n%s", out)
	}
	if strings.Contains(out.String()+y, "abc-secret-value") || a.applied != "" {
		t.Fatalf("a value was printed or written, or something applied:\n%s", out)
	}
}

func TestComposeImportAppends(t *testing.T) {
	composeServer(t)
	const old = "project: acme\nenvironment: production\nserver: hel1\n# the old api\nservices:\n  - {name: api, kind: worker, image: ghcr.io/acme/api:1}\n"
	dir := composeRepo(t, old)
	if err := Run(context.Background(), "compose", []string{"import"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "yawl.yaml"))
	if y := string(b); !strings.Contains(y, "# the old api") || !strings.Contains(y, "name: api") || !strings.Contains(y, "name: app") {
		t.Fatalf("%s", y)
	}
	clash := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: db, kind: postgres}\n"
	os.WriteFile(filepath.Join(dir, "yawl.yaml"), []byte(clash), 0o644)
	if err := Run(context.Background(), "compose", []string{"import"}); err == nil || err.Error() != "db is already in yawl.yaml; rename it in the Compose file first" {
		t.Fatalf("%v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "yawl.yaml")); string(b) != clash {
		t.Fatalf("changed:\n%s", b)
	}
}

func TestComposeImportApply(t *testing.T) {
	a := composeServer(t)
	composeRepo(t, "")
	if err := Run(context.Background(), "compose", []string{"import", "--server", "hel1", "--apply"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.applied, "name: app") {
		t.Fatalf("applied %q", a.applied)
	}
	if ref := a.puts["/api/services/shop/production/app/env/DATABASE_URL"]; ref["ref"] != "db.url" {
		t.Fatalf("ref %v", ref)
	}
	v := a.puts["/api/services/shop/production/app/env/SECRET"]
	opened, err := seal.Open(a.agentPub, a.agentPriv, v["agent_box"].(string))
	if err != nil || string(opened) != "abc-secret-value" || strings.Contains(a.everything.String(), "abc-secret-value") {
		t.Fatalf("value %q %v", opened, err)
	}
}

func TestComposeImportEmptyServicesAndCredential(t *testing.T) {
	composeServer(t)
	dir := composeRepoAt(t, "project: shop\nenvironment: production\nserver: hel1\nservices:\n", "https://me:ghp_secret123@github.com/acme/shop.git")
	if err := Run(context.Background(), "compose", []string{"import"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "yawl.yaml"))
	if y := string(b); !strings.Contains(y, "name: app") || !strings.Contains(y, "name: db") || strings.Contains(y, "ghp_secret123") || !strings.Contains(y, "repo: https://github.com/acme/shop.git") {
		t.Fatalf("%s", y)
	}
}

func TestComposeImportApplyShowsPlan(t *testing.T) {
	a := composeServer(t)
	a.plan = `[{"op":"create","kind":"service","name":"app"},{"op":"update","kind":"service","name":"api","detail":"port"}]`
	composeRepo(t, "project: shop\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: worker, image: ghcr.io/acme/api:2}\n")
	err := Run(context.Background(), "compose", []string{"import", "--apply"})
	if err == nil || err.Error() != "yawl.yaml also changes api; the import is written, review it with yawl apply" || a.applied != "" {
		t.Fatalf("%v, applied %q", err, a.applied)
	}
}

func TestComposeImportApplyPartial(t *testing.T) {
	a := composeServer(t)
	a.failPut = "/api/services/shop/production/app/env/DATABASE_URL"
	composeRepo(t, "")
	out := &bytes.Buffer{}
	stdout = out
	err := Run(context.Background(), "compose", []string{"import", "--server", "hel1", "--apply"})
	if err == nil || !strings.Contains(out.String(), "yawl env set app DATABASE_URL --ref db.url") || a.puts["/api/services/shop/production/app/env/SECRET"] == nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

const coolifyCompose = `services:
  app:
    image: ghcr.io/acme/app:1
    environment:
      - SERVICE_FQDN_APP_3000
      - PUBLIC_URL=$SERVICE_URL_APP_3000
      - TOKEN=$SERVICE_HEX_32_APP
`

func TestComposeImportCoolifyNames(t *testing.T) {
	a := composeServer(t)
	dir := composeRepo(t, "")
	os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(coolifyCompose), 0o644)
	out := &bytes.Buffer{}
	stdout = out
	if err := Run(context.Background(), "compose", []string{"import", "--server", "hel1", "--apply", "--yes"}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	open := func(k string) string {
		v := a.puts["/api/services/shop/production/app/env/"+k]
		b, err := seal.Open(a.agentPub, a.agentPriv, v["agent_box"].(string))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if u := open("PUBLIC_URL"); u != "https://app-production.1-2-3-4.sslip.io" {
		t.Fatalf("%q", u)
	}
	tok := open("TOKEN")
	if len(tok) != 32 || strings.Contains(out.String(), tok) || !strings.Contains(a.applied, "port: 3000") {
		t.Fatalf("token %q\n%s\n%s", tok, out, a.applied)
	}
	os.Remove(filepath.Join(dir, "yawl.yaml"))
	out.Reset()
	if err := Run(context.Background(), "compose", []string{"import", "--server", "hel1"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "TOKEN is generated only with --apply") {
		t.Fatalf("%s", out)
	}
}

func TestComposeImportMinors(t *testing.T) {
	composeServer(t)
	if err := Run(context.Background(), "compose", nil); err == nil || !strings.Contains(err.Error(), "[-f yawl.yaml]") || !strings.Contains(err.Error(), "[--project <name>]") {
		t.Fatalf("%v", err)
	}
	const old = "project: acme\nenvironment: production\nserver: hel1\nservices:\n- name: api   # the old api\n  kind: worker\n  image: ghcr.io/acme/api:1\n# kept last\n"
	dir := composeRepo(t, old)
	os.Rename(filepath.Join(dir, "docker-compose.yml"), filepath.Join(dir, "compose.yml"))
	if err := Run(context.Background(), "compose", []string{"import"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "yawl.yaml"))
	y := string(b)
	if !strings.HasPrefix(y, strings.TrimSuffix(old, "# kept last\n")) || !strings.Contains(y, "\n- name: app\n") || !strings.HasSuffix(y, "# kept last\n") {
		t.Fatalf("layout changed:\n%s", y)
	}
	os.WriteFile(filepath.Join(dir, "yawl.yaml"), []byte("project: acme\nenvironment: production\nserver: hel1\nservices: [] # none yet\n"), 0o644)
	if err := Run(context.Background(), "compose", []string{"import"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "yawl.yaml")); !strings.Contains(string(b), "# none yet") || !strings.Contains(string(b), "name: app") {
		t.Fatalf("comment lost:\n%s", b)
	}
}

func TestComposeImportAgainWithApply(t *testing.T) {
	a := composeServer(t)
	dir := composeRepo(t, "")
	os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(coolifyCompose), 0o644)
	if err := Run(context.Background(), "compose", []string{"import", "--server", "hel1"}); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, "yawl.yaml"))
	if err := Run(context.Background(), "compose", []string{"import", "--server", "hel1", "--apply", "--yes"}); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(filepath.Join(dir, "yawl.yaml")); string(again) != string(first) {
		t.Fatalf("changed:\n%s", again)
	}
	if _, ok := a.puts["/api/services/shop/production/app/env/TOKEN"]; !ok {
		t.Fatalf("TOKEN not set: %v", a.puts)
	}
	os.WriteFile(filepath.Join(dir, "yawl.yaml"), []byte(strings.Replace(string(first), "port: 3000", "port: 4000", 1)), 0o644)
	if err := Run(context.Background(), "compose", []string{"import"}); err == nil || !strings.Contains(err.Error(), "app is already in yawl.yaml") {
		t.Fatalf("%v", err)
	}
}
