package compose

import (
	"encoding/base64"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/spec"
)

const shop = `
services:
  app:
    build: .
    ports: ["8080:3000"]
    environment:
      DATABASE_URL: postgres://app:pw@db:5432/app
      REDIS_URL: redis://cache:6379
      SECRET: abc
    volumes: [uploads:/app/uploads, ./src:/src]
    depends_on: [db]
  db:
    image: postgres:16
    environment: {POSTGRES_PASSWORD: pw}
    volumes: [pgdata:/var/lib/postgresql/data]
  cache:
    image: redis:7
  worker:
    build: {context: ., dockerfile: Dockerfile.worker}
    depends_on: [db]
  mail:
    image: axllent/mailpit
volumes: {uploads: {}, pgdata: {}}
`

func TestConvertShop(t *testing.T) {
	r, err := Convert([]byte(shop), nil)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]spec.Service{}
	for _, s := range r.Services {
		by[s.Name] = s
	}
	if a := by["app"]; a.Kind != spec.KindWeb || a.Port != 3000 || a.Image != "" || !slices.Equal(a.Volumes, []spec.Volume{{Name: "uploads", Path: "/app/uploads"}}) {
		t.Fatalf("app %+v", a)
	}
	if d := by["db"]; d.Kind != spec.KindPostgres || d.Version != 16 {
		t.Fatalf("db %+v", d)
	}
	if c := by["cache"]; c.Kind != spec.KindValkey || c.Version != 8 || c.Backup == nil || !*c.Backup {
		t.Fatalf("cache %+v", c)
	}
	if w := by["worker"]; w.Kind != spec.KindWorker || w.Image != "" {
		t.Fatalf("worker %+v", w)
	}
	if m := by["mail"]; m.Kind != spec.KindWorker || m.Image != "axllent/mailpit" {
		t.Fatalf("mail %+v", m)
	}
	want := []Var{{Service: "app", Key: "DATABASE_URL", Ref: "db.url"}, {Service: "app", Key: "REDIS_URL", Ref: "cache.url"}, {Service: "app", Key: "SECRET", Value: "abc"}}
	if !slices.Equal(r.Vars, want) {
		t.Fatalf("vars %+v", r.Vars)
	}
	for _, w := range []string{
		"app: bind mount ./src:/src is skipped; a server's folders are not this machine's.",
		"worker: builds Dockerfile.worker; Yawl builds the Dockerfile at the top of its root.",
		"db: POSTGRES_PASSWORD is not carried; Yawl makes the database's own password.",
		"db: the data in volume pgdata is not moved; restore a dump into db once it runs.",
		"depends_on is ignored; Yawl starts each service on its own.",
	} {
		if !slices.Contains(r.Warnings, w) {
			t.Errorf("no warning %q in\n%s", w, strings.Join(r.Warnings, "\n"))
		}
	}
	if k, v, ok := Database("clickhouse/clickhouse-server:24.8"); !ok || k != spec.KindClickHouse || v != 25 {
		t.Fatalf("clickhouse: %s %d", k, v)
	}
	n := 0
	for _, w := range r.Warnings {
		if strings.HasPrefix(w, "depends_on") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("depends_on named %d times", n)
	}
}

func TestConvertCommand(t *testing.T) {
	f := `
services:
  web:
    image: ghcr.io/acme/app:1
    ports: ["3000"]
    command: npm run start -- --port 3000
  jobs:
    image: ghcr.io/acme/app:1
    command: [python, -m, app.worker]
  both:
    image: ghcr.io/acme/app:1
    entrypoint: [tini, --]
    command: [node, worker.js]
  only:
    image: ghcr.io/acme/app:1
    entrypoint: /start.sh
  mixed:
    image: ghcr.io/acme/app:1
    entrypoint: /start.sh --wait
    command: [node, "it's.js"]
  plain:
    image: ghcr.io/acme/app:1
  db:
    image: postgres:17
    command: postgres -c max_connections=200
`
	r, err := Convert([]byte(f), nil)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string][]string{}
	for _, s := range r.Services {
		by[s.Name] = s.Command
	}
	for name, want := range map[string][]string{
		"web":   {"sh", "-c", "npm run start -- --port 3000"},
		"jobs":  {"python", "-m", "app.worker"},
		"both":  {"tini", "--", "node", "worker.js"},
		"only":  {"sh", "-c", "/start.sh"},
		"mixed": {"sh", "-c", `/start.sh --wait node 'it'\''s.js'`},
		"plain": nil,
		"db":    nil,
	} {
		if !slices.Equal(by[name], want) {
			t.Errorf("%s: %q, want %q", name, by[name], want)
		}
	}
	if !slices.Contains(r.Warnings, "command is ignored; Yawl runs the image's own command.") {
		t.Errorf("the database's command went without a note:\n%s", strings.Join(r.Warnings, "\n"))
	}
	if slices.Contains(r.Warnings, "entrypoint is ignored; Yawl runs the image's own command.") {
		t.Errorf("an app's entrypoint is noted as ignored:\n%s", strings.Join(r.Warnings, "\n"))
	}
	if !slices.Contains(r.Warnings, "both: entrypoint runs as the start of command; an ENTRYPOINT in the image itself still runs first.") {
		t.Errorf("no entrypoint note:\n%s", strings.Join(r.Warnings, "\n"))
	}
}

func TestConvertForms(t *testing.T) {
	f := `
services:
  api:
    image: ghcr.io/acme/api:1
    ports:
      - target: 8000
        published: 80
      - "9000"
    environment:
      - MODE=prod
      - TOKEN=${TOKEN}
    env_file: api.env
    healthcheck: {test: [CMD, curl, localhost]}
  store:
    image: bitnami/mariadb:11
`
	r, err := Convert([]byte(f), func(name string) ([]byte, error) {
		if name != "api.env" {
			return nil, errors.New("no such file")
		}
		return []byte("# a comment\nLEVEL=debug\n"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Services[0].Port != 8000 || r.Services[0].Kind != spec.KindWeb || r.Services[1].Kind != spec.KindMariaDB || r.Services[1].Version != 11 {
		t.Fatalf("%+v", r.Services)
	}
	want := []Var{{Service: "api", Key: "LEVEL", Value: "debug"}, {Service: "api", Key: "MODE", Value: "prod"}, {Service: "api", Key: "TOKEN", Value: "${TOKEN}"}}
	if !slices.Equal(r.Vars, want) {
		t.Fatalf("vars %+v", r.Vars)
	}
	for _, w := range []string{
		"api: publishes 2 ports; Yawl serves the first, 8000.",
		"api: TOKEN uses ${TOKEN}, set as written; give it its value with yawl env set.",
		"healthcheck is ignored; Yawl checks a web service at its health path.",
	} {
		if !slices.Contains(r.Warnings, w) {
			t.Errorf("no warning %q in\n%s", w, strings.Join(r.Warnings, "\n"))
		}
	}
	if _, err := Convert([]byte("services: {a: {}}"), nil); err == nil || err.Error() != "a: no image and no build; nothing to run" {
		t.Fatalf("%v", err)
	}
}

func TestConvertReviewFixes(t *testing.T) {
	f := `
services:
  app:
    build: .
    environment:
      DATABASE_URL: postgres://app:realpass@${DB_HOST}/x
      PG_URL: postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@db:5432/app
      DB_HOST: db
      REDIS: cache:6379
      DB_PASSWORD: s3cret
      PORT: "3000"
      node_env: production
  db:
    image: postgres:17
    environment: {POSTGRES_PASSWORD: s3cret}
  cache:
    image: redis:7
`
	r, err := Convert([]byte(f), nil)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(r.Warnings, "\n")
	if strings.Contains(all, "realpass") || strings.Contains(all, "s3cret") {
		t.Fatalf("a value in the notes:\n%s", all)
	}
	want := []Var{{Service: "app", Key: "DATABASE_URL", Value: "postgres://app:realpass@${DB_HOST}/x"}, {Service: "app", Key: "DB_HOST", Ref: "db.url"},
		{Service: "app", Key: "PG_URL", Ref: "db.url"}, {Service: "app", Key: "REDIS", Ref: "cache.url"}}
	if !slices.Equal(r.Vars, want) {
		t.Fatalf("vars %+v", r.Vars)
	}
	for _, w := range []string{
		"app: DATABASE_URL uses ${DB_HOST}, set as written; give it its value with yawl env set.",
		"app: DB_PASSWORD is the Compose password of db and is left out; db.url carries Yawl's.",
		"app: PORT is left out; Yawl sets it to the service's port.",
		"app: node_env is left out; Yawl's variable names are UPPER_SNAKE_CASE.",
		"app: DB_HOST names db; it becomes db.url, the whole address. Check the app reads a URL there.",
	} {
		if !slices.Contains(r.Warnings, w) {
			t.Errorf("no note %q in\n%s", w, all)
		}
	}
}

const coolify = `
services:
  ghost:
    image: ghost:5
    environment:
      - SERVICE_FQDN_GHOST_2368
      - API_URL=${SERVICE_URL_GHOST_2368}/api
      - DB_PASSWORD=$SERVICE_PASSWORD_MYSQL
      - SESSION_SECRET=$SERVICE_BASE64_64_GHOST
      - ADMIN_USER=$SERVICE_USER_ADMIN
  mysql:
    image: mysql:8
    environment:
      - MYSQL_PASSWORD=$SERVICE_PASSWORD_MYSQL
  helper:
    image: alpine
    environment:
      - SHARED=$SERVICE_BASE64_64_GHOST
      - KEY=$SERVICE_HEX_32_X
      - RB=$SERVICE_REALBASE64_X
      - SYM=$SERVICE_PASSWORDWITHSYMBOLS_X
`

func TestCoolifyNames(t *testing.T) {
	host := func(s string) string { return s + "-production.1-2-3-4.sslip.io" }
	r, err := ConvertWith([]byte(coolify), nil, host)
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]Var{}
	for _, v := range r.Vars {
		vars[v.Service+" "+v.Key] = v
	}
	var ghost spec.Service
	for _, s := range r.Services {
		if s.Name == "ghost" {
			ghost = s
		}
	}
	if ghost.Kind != spec.KindWeb || ghost.Port != 2368 {
		t.Fatalf("ghost %+v", ghost)
	}
	if v := vars["ghost SERVICE_FQDN_GHOST_2368"]; v.Value != "ghost-production.1-2-3-4.sslip.io" || v.GeneratedSecret {
		t.Fatalf("%+v", v)
	}
	if v := vars["ghost API_URL"]; v.Value != "https://ghost-production.1-2-3-4.sslip.io/api" {
		t.Fatalf("%+v", v)
	}
	if _, kept := vars["ghost DB_PASSWORD"]; kept {
		t.Fatal("the database's Compose password was kept")
	}
	s := vars["ghost SESSION_SECRET"]
	alnum := regexp.MustCompile(`^[A-Za-z0-9]+$`)
	if len(s.Value) != 64 || !alnum.MatchString(s.Value) || !s.GeneratedSecret || vars["helper SHARED"].Value != s.Value {
		t.Fatalf("%+v %+v", s, vars["helper SHARED"])
	}
	if u := vars["ghost ADMIN_USER"].Value; len(u) != 16 || !alnum.MatchString(u) {
		t.Fatalf("%q", u)
	}
	if k := vars["helper KEY"].Value; !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(k) {
		t.Fatalf("%q", k)
	}
	if b, err := base64.StdEncoding.DecodeString(vars["helper RB"].Value); err != nil || len(b) != 32 {
		t.Fatalf("%q %v", vars["helper RB"].Value, err)
	}
	if p := vars["helper SYM"].Value; len(p) != 32 || alnum.MatchString(p) {
		t.Fatalf("with symbols: %q", p)
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "SERVICE_") && !strings.Contains(w, "domain") {
			t.Errorf("warned about a filled name: %s", w)
		}
	}
	r, _ = ConvertWith([]byte(coolify), nil, nil)
	if !slices.ContainsFunc(r.Warnings, func(w string) bool { return strings.Contains(w, "SERVICE_URL_GHOST_2368") }) {
		t.Fatalf("%q", r.Warnings)
	}
}

func TestCoolifyNameForms(t *testing.T) {
	const f = `
services:
  web:
    image: ghost:5
    environment:
      - A=${SERVICE_PASSWORD_X:-fallback}
      - B=$SERVICE_PASSWORD_X
      - LIT=$${SERVICE_PASSWORD_X}
      - DB_USER=${SERVICE_USER_PG}
      - DB_PASS=${SERVICE_PASSWORD_PG:-x}
  pg:
    image: postgres:16
    environment:
      - POSTGRES_USER=$SERVICE_USER_PG
      - POSTGRES_PASSWORD=$SERVICE_PASSWORD_PG
`
	r, err := ConvertWith([]byte(f), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{}
	for _, v := range r.Vars {
		vars[v.Key] = v.Value
	}
	if len(vars["A"]) != 32 || vars["A"] != vars["B"] {
		t.Fatalf("A %q B %q", vars["A"], vars["B"])
	}
	if vars["LIT"] != "$${SERVICE_PASSWORD_X}" {
		t.Fatalf("LIT %q", vars["LIT"])
	}
	for _, k := range []string{"DB_USER", "DB_PASS"} {
		if _, kept := vars[k]; kept {
			t.Errorf("%s was kept", k)
		}
	}
	if !slices.ContainsFunc(r.Warnings, func(w string) bool { return strings.Contains(w, "DB_PASS") && strings.Contains(w, "pg.url") }) {
		t.Fatalf("%q", r.Warnings)
	}
}

func TestDatabaseImages(t *testing.T) {
	for image, want := range map[string]struct {
		kind    spec.Kind
		version int
	}{
		"postgres":                          {spec.KindPostgres, 17},
		"docker.io/library/postgres:16":     {spec.KindPostgres, 16},
		"postgres:18-alpine":                {spec.KindPostgres, 18},
		"pgvector/pgvector:pg16":            {spec.KindPostgres, 16},
		"pgvector/pgvector:0.8.0-pg17":      {spec.KindPostgres, 17},
		"bitnami/mariadb:11":                {spec.KindMariaDB, 11},
		"clickhouse/clickhouse-server:24.8": {spec.KindClickHouse, 25},
		"valkey/valkey:8@sha256:abc":        {spec.KindValkey, 8},
		"ghcr.io/acme/postgres:1":           {},
		"acme/postgres":                     {},
		"registry.example:5000/postgres:16": {},
		"ghcr.io/acme/redis-cache:2":        {},
	} {
		k, v, ok := Database(image)
		if k != want.kind || v != want.version || ok != (want.kind != "") {
			t.Errorf("%s: %s %d %v", image, k, v, ok)
		}
	}
	r, err := Convert([]byte("services:\n  db: {image: \"postgres:15\"}\n  vec: {image: \"pgvector/pgvector:pg16\"}\n  app: {image: ghcr.io/acme/postgres:1}\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(r.Warnings, "db: postgres:15 runs as version 17, the one Yawl runs; check the app works with it.") ||
		slices.ContainsFunc(r.Warnings, func(w string) bool { return strings.HasPrefix(w, "vec: pgvector") }) {
		t.Fatalf("%q", r.Warnings)
	}
	if i := slices.IndexFunc(r.Services, func(s spec.Service) bool { return s.Name == "app" }); r.Services[i].Kind != spec.KindWorker {
		t.Fatalf("app %+v", r.Services[i])
	}
}

func TestConvertMinors(t *testing.T) {
	f := `
services:
  My_App:
    image: ghcr.io/acme/app:1
    ports: ["53:53/udp", "8000-8001:8000-8001"]
    env_file:
      - path: app.env
        required: false
    volumes:
      - /cache
      - type: volume
        target: /tmp/x
      - pg_data:/data
  broker:
    image: eclipse-mosquitto:2
    ports: ["1883:1883"]
  main_db:
    image: mysql:8.4
    build: ./db
  api:
    build:
      context: .
      args:
        NODE_ENV: production
    environment:
      DATABASE_URL: mysql://u:p@main_db:3306/app
`
	r, err := Convert([]byte(f), func(name string) ([]byte, error) { return []byte("LEVEL=debug\n"), nil })
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]spec.Service{}
	for _, s := range r.Services {
		by[s.Name] = s
	}
	app := by["my-app"]
	if app.Kind != spec.KindWeb || app.Port != 8000 || !slices.Equal(app.Volumes, []spec.Volume{{Name: "pg-data", Path: "/data"}}) {
		t.Fatalf("my-app %+v", app)
	}
	if b := by["broker"]; b.Kind != spec.KindWorker || b.Port != 0 {
		t.Fatalf("broker %+v", b)
	}
	if by["main-db"].Kind != spec.KindMySQL {
		t.Fatalf("main-db %+v", r.Services)
	}
	for _, want := range []Var{{Service: "api", Key: "DATABASE_URL", Ref: "main-db.url"}, {Service: "my-app", Key: "LEVEL", Value: "debug"}} {
		if !slices.Contains(r.Vars, want) {
			t.Errorf("no var %+v in %+v", want, r.Vars)
		}
	}
	for _, w := range []string{
		"My_App is named my-app; Yawl's names are lowercase letters, digits and dashes.",
		"main_db is named main-db; Yawl's names are lowercase letters, digits and dashes.",
		"My_App: port 53/udp is skipped; Yawl serves HTTP.",
		"My_App: publishes the range 8000-8001; Yawl serves its first port, 8000.",
		"My_App: anonymous volume /cache is skipped; name it in the Compose file to keep its data.",
		"My_App: anonymous volume /tmp/x is skipped; name it in the Compose file to keep its data.",
		"My_App: volume pg_data is named pg-data.",
		"broker: port 1883 is not HTTP; it runs as a worker, which Yawl does not publish.",
		"main_db: build is ignored; Yawl runs its own mysql database.",
		"api: build arg NODE_ENV is not carried; set it with yawl env set api NODE_ENV --build.",
	} {
		if !slices.Contains(r.Warnings, w) {
			t.Errorf("no warning %q in\n%s", w, strings.Join(r.Warnings, "\n"))
		}
	}
	if _, err := Convert([]byte("services:\n  a_b: {image: x}\n  a-b: {image: y}\n"), nil); err == nil || err.Error() != "a-b and a_b are both a-b in Yawl; rename one" {
		t.Fatalf("%v", err)
	}
}

func TestCoolifyNameMinors(t *testing.T) {
	const f = `
services:
  app:
    image: ghcr.io/acme/app:1
    environment:
      - SERVICE_NAME
      - SERVICE_PASSWORD_APP
      - DB_HOST=$SERVICE_FQDN_DB
  db:
    image: postgres:17
`
	host := func(s string) string { return s + "-production.1-2-3-4.sslip.io" }
	r, err := ConvertWith([]byte(f), nil, host)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range r.Vars {
		if v.Key == "SERVICE_NAME" || v.Key == "DB_HOST" {
			t.Errorf("kept %+v", v)
		}
	}
	if !slices.ContainsFunc(r.Vars, func(v Var) bool { return v.Key == "SERVICE_PASSWORD_APP" && v.GeneratedSecret }) {
		t.Errorf("the generated one went: %+v", r.Vars)
	}
	for _, w := range []string{
		"app: SERVICE_NAME has no value here; set it with yawl env set.",
		"app: DB_HOST uses SERVICE_FQDN_DB, the address of database db, which has none; reference db.url instead.",
	} {
		if !slices.Contains(r.Warnings, w) {
			t.Errorf("no warning %q in\n%s", w, strings.Join(r.Warnings, "\n"))
		}
	}
}

func TestConvertBuildContext(t *testing.T) {
	f := `services:
  site:
    build: ./landing
    ports: ["8080:8080"]
  bot:
    build: {context: telegram-bot/}
  up:
    build: ../shared
  top:
    build: .
  db:
    image: postgres:17
    build: ./db
`
	r, err := Convert([]byte(f), nil)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]spec.Service{}
	for _, s := range r.Services {
		by[s.Name] = s
	}
	if by["site"].Root != "landing" || by["bot"].Root != "telegram-bot" || by["up"].Root != "" || by["top"].Root != "" || by["db"].Root != "" {
		t.Fatalf("roots %q %q %q %q", by["site"].Root, by["bot"].Root, by["up"].Root, by["top"].Root)
	}
	if !slices.Contains(r.Warnings, "up: builds from ../shared, outside the repository; Yawl builds a folder in it.") {
		t.Fatalf("warnings %q", r.Warnings)
	}
}

func TestGeneratorForPicksLongestPrefixInAnyOrder(t *testing.T) {
	saved := generators
	t.Cleanup(func() { generators = saved })
	for _, order := range []string{"declared", "reversed"} {
		generators = slices.Clone(saved)
		if order == "reversed" {
			slices.Reverse(generators)
		}
		for name, size := range map[string]int{
			"SERVICE_PASSWORD_64_APP": 64, "SERVICE_PASSWORD_APP": 32, "SERVICE_PASSWORD_64_": 32,
			"SERVICE_HEX_128_APP": 128, "SERVICE_HEX_32_APP": 32, "SERVICE_BASE64_128_APP": 128,
		} {
			if gen := generatorFor(name); gen == nil || len(gen()) != size {
				t.Errorf("%s order: %s does not make a %d-character value", order, name, size)
			}
		}
		if generatorFor("SERVICE_HEX_APP") != nil {
			t.Errorf("%s order: SERVICE_HEX_APP has a generator", order)
		}
	}
}
