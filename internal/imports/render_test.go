package imports

import (
	"slices"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/compose"
	"github.com/behzodhalil/yawl/internal/spec"
)

func byName(r compose.Result) map[string]spec.Service {
	m := map[string]spec.Service{}
	for _, s := range r.Services {
		m[s.Name] = s
	}
	return m
}

func hasNotes(t *testing.T, r compose.Result, want ...string) {
	t.Helper()
	for _, w := range want {
		if !slices.Contains(r.Warnings, w) {
			t.Errorf("no note %q in\n%s", w, strings.Join(r.Warnings, "\n"))
		}
	}
}

func varOf(r compose.Result, service, key string) (compose.Var, bool) {
	for _, v := range r.Vars {
		if v.Service == service && v.Key == key {
			return v, true
		}
	}
	return compose.Var{}, false
}

const renderYAML = `
services:
  - type: web
    name: shop
    runtime: node
    repo: https://github.com/acme/shop
    plan: starter
    region: frankfurt
    buildCommand: npm ci && npm run build
    startCommand: npm run start -- --port $PORT
    preDeployCommand: npx prisma migrate deploy
    healthCheckPath: /healthz
    numInstances: 2
    domains: [shop.acme.uz, "*.acme.uz"]
    rootDir: web
    dockerfilePath: ./docker/Dockerfile
    disk:
      name: uploads
      mountPath: /var/data
      sizeGB: 10
    envVars:
      - key: NODE_ENV
        value: production
      - key: WORKERS
        value: 4
      - key: DATABASE_URL
        fromDatabase:
          name: shop-db
          property: connectionString
      - key: DB_USER
        fromDatabase:
          name: shop-db
          property: user
      - key: REDIS_URL
        fromService:
          type: keyvalue
          name: cache
          property: connectionString
      - key: API_HOST
        fromService:
          type: pserv
          name: api
          property: hostport
      - key: SESSION_SECRET
        generateValue: true
      - key: STRIPE_KEY
        sync: false
      - fromGroup: shared
  - type: pserv
    name: api
    runtime: image
    image:
      url: ghcr.io/acme/api:2
      creds:
        fromRegistryCreds:
          name: ghcr
    dockerCommand: ./api --listen :8080
    envVars:
      - fromGroup: shared
  - type: worker
    name: jobs
    runtime: docker
    startCommand: bundle exec sidekiq -q 'default,2'
    numInstances: 14
  - type: cron
    name: nightly
    runtime: python
    schedule: "0 3 * * *"
    startCommand: python manage.py cleanup
  - type: keyvalue
    name: cache
    ipAllowList: []
  - type: web
    name: docs
    runtime: static
    staticPublishPath: ./dist
databases:
  - name: shop-db
    postgresMajorVersion: "16"
    databaseName: shop
    user: shop
    plan: basic-256mb
  - name: old-db
    postgresMajorVersion: "15"
envVarGroups:
  - name: shared
    envVars:
      - key: LOG_LEVEL
        value: info
      - key: SIGNING_KEY
        generateValue: true
`

func TestRender(t *testing.T) {
	r, err := Render([]byte(renderYAML))
	if err != nil {
		t.Fatal(err)
	}
	by := byName(r)
	shop := by["shop"]
	if shop.Kind != spec.KindWeb || shop.Port != 10000 || shop.Image != "" || shop.HealthPath != "/healthz" || shop.Replicas != 0 || shop.Root != "web" || !slices.Equal(shop.Watch, []string{"web/**"}) {
		t.Fatalf("shop %+v", shop)
	}
	if !slices.Equal(shop.Command, []string{"sh", "-c", "npm run start -- --port $PORT"}) || !slices.Equal(shop.Release, []string{"sh", "-c", "npx prisma migrate deploy"}) {
		t.Fatalf("shop commands %q %q", shop.Command, shop.Release)
	}
	if !slices.Equal(shop.Domains, []string{"shop.acme.uz"}) || !slices.Equal(shop.Volumes, []spec.Volume{{Name: "uploads", Path: "/var/data"}}) {
		t.Fatalf("shop %v %v", shop.Domains, shop.Volumes)
	}
	if a := by["api"]; a.Kind != spec.KindWorker || a.Image != "ghcr.io/acme/api:2" || !slices.Equal(a.Command, []string{"sh", "-c", "./api --listen :8080"}) {
		t.Fatalf("api %+v", a)
	}
	if j := by["jobs"]; j.Kind != spec.KindWorker || j.Replicas != 10 || !slices.Equal(j.Command, []string{"sh", "-c", "bundle exec sidekiq -q 'default,2'"}) {
		t.Fatalf("jobs %+v", j)
	}
	if c := by["nightly"]; c.Kind != spec.KindCron || c.Schedule != "0 3 * * *" || c.Timezone != "UTC" || len(c.Command) != 3 {
		t.Fatalf("nightly %+v", c)
	}
	if c := by["cache"]; c.Kind != spec.KindValkey || c.Version != 8 {
		t.Fatalf("cache %+v", c)
	}
	if d := by["shop-db"]; d.Kind != spec.KindPostgres || d.Version != 16 {
		t.Fatalf("shop-db %+v", d)
	}
	if d := by["old-db"]; d.Kind != spec.KindPostgres || d.Version != 0 {
		t.Fatalf("old-db %+v", d)
	}
	if _, ok := by["docs"]; ok {
		t.Fatal("a static site was carried")
	}
	for _, want := range []compose.Var{
		{Service: "shop", Key: "NODE_ENV", Value: "production"},
		{Service: "shop", Key: "WORKERS", Value: "4"},
		{Service: "shop", Key: "DATABASE_URL", Ref: "shop-db.url"},
		{Service: "shop", Key: "REDIS_URL", Ref: "cache.url"},
		{Service: "shop", Key: "LOG_LEVEL", Value: "info"},
		{Service: "api", Key: "LOG_LEVEL", Value: "info"},
	} {
		if !slices.Contains(r.Vars, want) {
			t.Errorf("no var %+v in %+v", want, r.Vars)
		}
	}
	sess, _ := varOf(r, "shop", "SESSION_SECRET")
	k1, _ := varOf(r, "shop", "SIGNING_KEY")
	k2, _ := varOf(r, "api", "SIGNING_KEY")
	if !sess.GeneratedSecret || len(sess.Value) != 32 || !k1.GeneratedSecret || k1.Value != k2.Value || k1.Value == sess.Value {
		t.Fatalf("generated %+v %+v %+v", sess, k1, k2)
	}
	for _, k := range []string{"STRIPE_KEY", "DB_USER", "API_HOST"} {
		if _, ok := varOf(r, "shop", k); ok {
			t.Errorf("%s was set", k)
		}
	}
	hasNotes(t, r,
		"shop: buildCommand is not carried; Yawl's builder or Railpack decides how to build.",
		"shop: plan is not carried; Yawl runs on your server.",
		"shop: region is not carried; Yawl runs on your server.",
		"shop: builds ./docker/Dockerfile; Yawl builds the Dockerfile at the repository's root.",
		"jobs: runs 14 copies; Yawl runs at most 10.",
		"shop: has a disk, so it runs one copy.",
		"shop: domain *.acme.uz is not carried; Yawl takes plain host names.",
		"shop: volume uploads at /var/data starts empty; copy the data from Render's disk uploads into it once it runs.",
		"shop: STRIPE_KEY is kept on Render; set it with yawl env set shop STRIPE_KEY.",
		"shop: DB_USER is shop-db's user and is not carried; shop-db.url carries the whole address.",
		"shop: API_HOST is api's hostport and is not carried; Yawl services reach each other by name, api.",
		"api: a private service becomes a worker; other services reach it by name.",
		"api: the registry login is not carried; add it in Integrations, Registries.",
		"docs: a static site is not carried; add it to yawl.yaml as a web service.",
		"shop-db: the data is not moved; restore a dump into shop-db once it runs (yawl db import).",
		"shop-db: databaseName is not carried; Yawl sets the database up itself.",
		"old-db: Postgres 15 becomes 17; Yawl runs 16, 17 and 18.",
		"cache: the data is not moved; restore a dump into cache once it runs (yawl db import).",
	)
	if strings.Contains(strings.Join(r.Warnings, "\n"), sess.Value) {
		t.Fatal("a generated value is in the notes")
	}
}

func TestRenderProjects(t *testing.T) {
	r, err := Render([]byte("projects:\n  - name: acme\n    environments:\n      - name: production\n        services:\n          - {type: worker, name: jobs, runtime: docker}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if by := byName(r); by["jobs"].Kind != spec.KindWorker {
		t.Fatalf("%+v", r.Services)
	}
	hasNotes(t, r, "only project acme, environment production is read; import another by moving its services to the top of render.yaml.")
}

func TestSamplesValidate(t *testing.T) {
	read := map[string]func() (compose.Result, error){
		"render": func() (compose.Result, error) { return Render([]byte(renderYAML)) },
		"fly":    func() (compose.Result, error) { return Fly([]byte(flyTOML)) },
		"caprover": func() (compose.Result, error) {
			return CapRover([]byte(`{"schemaVersion": 2, "dockerfilePath": "./Dockerfile"}`), "shop")
		},
		"kamal": func() (compose.Result, error) { return Kamal([]byte(kamalYAML)) },
		"dokku": func() (compose.Result, error) { return Dokku([]byte(dokkuExport), []byte(dokkuStorage), "shop") },
	}
	for name, f := range read {
		r, err := f()
		if err != nil {
			t.Fatal(name, err)
		}
		file := spec.File{Project: "acme", Environment: "production", Server: "hel1", Services: r.Services}
		for i, s := range file.Services {
			if s.Image == "" && !spec.IsDatabase(s.Kind) {
				file.Services[i].Repo = "https://github.com/acme/shop"
			}
		}
		if err := spec.Validate(&file); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
