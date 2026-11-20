package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const shopRender = `services:
  - type: web
    name: app
    runtime: node
    buildCommand: npm ci
    startCommand: node server.js
    preDeployCommand: npm run migrate
    healthCheckPath: /healthz
    domains: [shop.acme.uz]
    envVars:
      - {key: DATABASE_URL, fromDatabase: {name: db, property: connectionString}}
      - {key: SESSION_SECRET, generateValue: true}
      - {key: STRIPE_KEY, sync: false}
      - {key: NODE_ENV, value: production}
  - type: cron
    name: nightly
    runtime: node
    schedule: "0 3 * * *"
    startCommand: node jobs/nightly.js
databases:
  - {name: db, postgresMajorVersion: "16"}
`

func TestImportRenderWrites(t *testing.T) {
	a := composeServer(t)
	dir := composeRepo(t, "")
	os.WriteFile(filepath.Join(dir, "render.yaml"), []byte(shopRender), 0o644)
	out := &bytes.Buffer{}
	stdout = out
	if err := Run(context.Background(), "import", []string{"render", "--server", "hel1"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "yawl.yaml"))
	y := string(b)
	for _, want := range []string{"project: shop\n", "name: app", "kind: web", "repo: https://github.com/acme/shop.git", "port: 10000",
		"health_path: /healthz", "release: [sh, -c, npm run migrate]", "domains: [shop.acme.uz]", "command: [sh, -c, node server.js]",
		"name: nightly", "kind: cron", "schedule: 0 3 * * *", "timezone: UTC", "name: db", "kind: postgres", "version: 16"} {
		if !strings.Contains(y, want) {
			t.Errorf("yawl.yaml lacks %q:\n%s", want, y)
		}
	}
	for _, want := range []string{"Added app (web).", "Added nightly (cron).", "Added db (postgres).",
		"Note: app: buildCommand is not carried; Yawl's builder or Railpack decides how to build.",
		"Note: app: STRIPE_KEY is kept on Render; set it with yawl env set app STRIPE_KEY.",
		"Note: app SESSION_SECRET is generated only with --apply; or set your own with yawl env set.",
		"yawl env set app DATABASE_URL --ref db.url", "yawl env set app NODE_ENV"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if a.applied != "" || a.everything.Len() != 0 {
		t.Fatalf("something was sent: %q", a.everything.String())
	}
	if err := Run(context.Background(), "import", []string{"render"}); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Join(dir, "yawl.yaml"))
	os.WriteFile(filepath.Join(dir, "yawl.yaml"), []byte(strings.Replace(string(again), "version: 16", "version: 17", 1)), 0o644)
	if err := Run(context.Background(), "import", []string{"render"}); err == nil || err.Error() != "db is already in yawl.yaml; rename it in render.yaml first" {
		t.Fatalf("%v", err)
	}
}

func TestImportDokku(t *testing.T) {
	composeServer(t)
	dir := composeRepo(t, "")
	if err := Run(context.Background(), "import", []string{"dokku"}); err == nil || !strings.HasPrefix(err.Error(), "usage: yawl import dokku") {
		t.Fatalf("%v", err)
	}
	os.WriteFile(filepath.Join(dir, "dokku.env"), []byte("export DATABASE_URL='postgres://postgres:pw@dokku-postgres-shopdb:5432/shopdb'\nexport DOKKU_APP_TYPE='herokuish'\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "storage.txt"), []byte("-----> web volume bind-mounts:\n       /var/lib/dokku/data/storage/web:/app/storage\n"), 0o644)
	out := &bytes.Buffer{}
	stdout = out
	if err := Run(context.Background(), "import", []string{"dokku", "dokku.env", "--storage", "storage.txt", "--name", "web", "--server", "hel1"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "yawl.yaml"))
	for _, want := range []string{"name: web", "port: 5000", "path: /app/storage", "name: shopdb", "kind: postgres"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("yawl.yaml lacks %q:\n%s", want, b)
		}
	}
	if !strings.Contains(out.String(), "yawl env set web DATABASE_URL --ref shopdb.url") || strings.Contains(out.String(), "DOKKU_APP_TYPE") {
		t.Fatalf("%s", out)
	}
	if err := Run(context.Background(), "import", []string{"heroku"}); err == nil || !strings.HasPrefix(err.Error(), "usage: yawl import render|fly") {
		t.Fatalf("%v", err)
	}
}
