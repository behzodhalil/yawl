package spec

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	f, err := Parse([]byte(`
project: acme
environment: production
server: hel1
services:
  - name: api
    kind: web
    repo: https://github.com/acme/api
`))
	if err != nil {
		t.Fatal(err)
	}
	s := f.Services[0]
	if s.Port != 10000 || s.HealthPath != "/" || s.HealthTimeout != 60*time.Second ||
		s.DrainTimeout != 30*time.Second || s.Branch != "main" {
		t.Fatalf("defaults not applied: %+v", s)
	}
}

func TestRejects(t *testing.T) {
	base := "project: acme\nenvironment: production\nserver: hel1\nservices:\n"
	cases := []struct{ name, yaml, field string }{
		{"empty project", "project: \nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: https://x/y}\n", `"project"`},
		{"bad name", base + "  - {name: Api_1, kind: web, repo: https://x/y}\n", `"name"`},
		{"duplicate", base + "  - {name: api, kind: web, repo: https://x/y}\n  - {name: api, kind: web, repo: https://x/y}\n", `"name"`},
		{"bad kind", base + "  - {name: api, kind: lambda, repo: https://x/y}\n", `"kind"`},
		{"worker port", base + "  - {name: w, kind: worker, port: 9000, repo: https://x/y}\n", `"port"`},
		{"health path", base + "  - {name: api, kind: web, health_path: health, repo: https://x/y}\n", `"health_path"`},
		{"repo", base + "  - {name: api, kind: web, repo: ftp://x/y}\n", `"repo"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.yaml))
			if err == nil || !strings.Contains(err.Error(), c.field) {
				t.Fatalf("want error naming %s, got %v", c.field, err)
			}
		})
	}
}

func TestAutoDeployDefault(t *testing.T) {
	f, err := Parse([]byte("project: acme\nenvironment: production\nserver: hel1\nservices:\n" +
		"  - {name: api, kind: web, repo: https://github.com/acme/api}\n" +
		"  - {name: jobs, kind: worker, repo: https://github.com/acme/api, auto_deploy: false}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !f.Services[0].AutoDeploy || f.Services[1].AutoDeploy {
		t.Fatalf("auto_deploy = %v, %v; want true, false", f.Services[0].AutoDeploy, f.Services[1].AutoDeploy)
	}
}

func TestGitHubRepo(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/Acme/API":     "acme/api",
		"https://github.com/acme/api.git": "acme/api",
		"https://github.com/acme/api/":    "acme/api",
		"git@github.com:acme/api.git":     "acme/api",
		"file:///x":                       "",
		"https://gitlab.com/a/b":          "",
		"https://github.com/acme":         "",
		"https://github.com/a/b/c":        "",
		"https://github.com.evil.io/a/b":  "",
	} {
		got, ok := GitHubRepo(in)
		if got != want || ok != (want != "") {
			t.Errorf("GitHubRepo(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

const pgBase = "project: acme\nenvironment: production\nserver: hel1\nservices:\n"

func TestPostgresDefaults(t *testing.T) {
	f, err := Parse([]byte(pgBase + "  - {name: acme-db, kind: postgres}\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := f.Services[0]
	if s.Version != 17 || s.BackupAt != "03:00" || s.Port != 0 || s.Branch != "" || s.Repo != "" {
		t.Fatalf("postgres defaults: %+v", s)
	}
}

func TestPostgres18(t *testing.T) {
	f, err := Parse([]byte(pgBase + "  - {name: acme-db, kind: postgres, version: 18}\n"))
	if err != nil || f.Services[0].Version != 18 {
		t.Fatalf("%v %+v", err, f)
	}
}

func TestRoot(t *testing.T) {
	app := "  - {name: site, kind: web, repo: https://github.com/a/b, port: 80, root: %s}\n"
	for _, ok := range []string{"landing", "apps/web", "telegram-bot", "a.b/c_d"} {
		if _, err := Parse([]byte(pgBase + fmt.Sprintf(app, ok))); err != nil {
			t.Errorf("root %s: %v", ok, err)
		}
	}
	for _, bad := range []string{"/landing", "../x", "a/../b", "./x", "a//b", "a/", "'a b'"} {
		if _, err := Parse([]byte(pgBase + fmt.Sprintf(app, bad))); err == nil || !strings.Contains(err.Error(), `"root"`) {
			t.Errorf("root %s: %v", bad, err)
		}
	}
	if _, err := Parse([]byte(pgBase + "  - {name: site, kind: web, image: nginx, port: 80, root: landing}\n")); err == nil || !strings.Contains(err.Error(), `"root" is for apps built from a repo`) {
		t.Errorf("root with an image: %v", err)
	}
}

func TestPostgresRejects(t *testing.T) {
	for svc, field := range map[string]string{
		"{name: db, kind: postgres, version: 15}":                       "version",
		`{name: db, kind: postgres, backup_at: "3am"}`:                  "backup_at",
		`{name: db, kind: postgres, backup_at: "24:00"}`:                "backup_at",
		"{name: db, kind: postgres, repo: https://github.com/a/b}":      "repo",
		"{name: db, kind: postgres, port: 5432}":                        "port",
		"{name: api, kind: web, repo: https://x/y, version: 17}":        "version",
		`{name: api, kind: web, repo: https://x/y, backup_at: "03:00"}`: "backup_at",
	} {
		_, err := Parse([]byte(pgBase + "  - " + svc + "\n"))
		if err == nil || !strings.Contains(err.Error(), `"`+field+`"`) {
			t.Errorf("%s: err = %v, want one naming %q", svc, err, field)
		}
	}
}

const head = "project: acme\nenvironment: production\nserver: hel1\nservices:\n"

func TestReleaseDefaults(t *testing.T) {
	f, err := Parse([]byte(head + "  - {name: api, kind: web, repo: https://x/y, release: [/app, migrate]}\n  - {name: web, kind: web, repo: https://x/y}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Services[0].ReleaseTimeout != 10*time.Minute || f.Services[1].ReleaseTimeout != 0 {
		t.Fatalf("release_timeout = %v, %v", f.Services[0].ReleaseTimeout, f.Services[1].ReleaseTimeout)
	}
}

func TestCommandOnWebAndWorker(t *testing.T) {
	f, err := Parse([]byte(head + "  - {name: api, kind: web, image: acme/app:1, port: 8000, command: [uvicorn, app.main:app]}\n" +
		"  - {name: jobs, kind: worker, image: acme/app:1, command: [python, -m, app.worker]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.Services[0].Command, []string{"uvicorn", "app.main:app"}) || len(f.Services[1].Command) != 3 {
		t.Fatalf("command = %q, %q", f.Services[0].Command, f.Services[1].Command)
	}
}

func TestDomainsRejects(t *testing.T) {
	cases := map[string]string{
		"upper":    "  - {name: api, kind: web, repo: https://x/y, domains: [Shop.acme.uz]}\n",
		"sslip":    "  - {name: api, kind: web, repo: https://x/y, domains: [x.sslip.io]}\n",
		"dash":     "  - {name: api, kind: web, repo: https://x/y, domains: [-a.uz]}\n",
		"repeated": "  - {name: api, kind: web, repo: https://x/y, domains: [a.uz]}\n  - {name: b, kind: web, repo: https://x/y, domains: [a.uz]}\n",
		"worker":   "  - {name: w, kind: worker, repo: https://x/y, domains: [a.uz]}\n",
		"postgres": "  - {name: db, kind: postgres, domains: [a.uz]}\n",
	}
	for name, y := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(head + y))
			if err == nil || !strings.Contains(err.Error(), `"domains"`) {
				t.Fatalf("want error naming \"domains\", got %v", err)
			}
		})
	}
}

func TestOutsideAllow(t *testing.T) {
	cases := []struct{ name, yaml, want string }{
		{"no allow", "  - {name: db, kind: postgres, outside_port: 15432}\n", `"allow"`},
		{"host address", "  - {name: db, kind: postgres, outside_port: 15432, allow: [10.0.0.1]}\n", "allow entries are CIDRs like 10.0.0.1/32"},
		{"low port", "  - {name: db, kind: postgres, outside_port: 80, allow: [10.0.0.1/32]}\n", `"outside_port"`},
		{"web", "  - {name: api, kind: web, repo: https://x/y, outside_port: 15432, allow: [10.0.0.1/32]}\n", `"outside_port"`},
		// Docker's proxy hides a v6 client behind the gateway, so a v6 entry never matches
		{"ipv6", "  - {name: db, kind: postgres, outside_port: 15432, allow: [\"2001:db8::/32\"]}\n", "allow entries are IPv4 CIDRs"},
		{"mapped", "  - {name: db, kind: postgres, outside_port: 15432, allow: [\"::ffff:10.0.0.0/104\"]}\n", "allow entries are IPv4 CIDRs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(head + c.yaml))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error with %s, got %v", c.want, err)
			}
		})
	}
	if _, err := Parse([]byte(head + "  - {name: db, kind: postgres, outside_port: 15432, allow: [203.0.113.0/24]}\n")); err != nil {
		t.Fatal(err)
	}
}

func TestOutsidePortTwiceInAFile(t *testing.T) {
	_, err := Parse([]byte(head + "  - {name: a, kind: postgres, outside_port: 15432, allow: [10.0.0.1/32]}\n  - {name: b, kind: postgres, outside_port: 15432, allow: [10.0.0.1/32]}\n"))
	if err == nil || !strings.Contains(err.Error(), `"outside_port" 15432 appears twice`) {
		t.Fatalf("err = %v", err)
	}
}

func TestCronDefaults(t *testing.T) {
	f, err := Parse([]byte("project: acme\nenvironment: production\nserver: hel1\nservices:\n" +
		"  - {name: report, kind: cron, repo: https://github.com/acme/api, schedule: \"0 3 * * *\", command: [node, report.js]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := f.Services[0]
	if s.Timeout != time.Hour || s.Timezone != "" || s.Branch != "main" || s.Port != 0 || s.HealthPath != "" {
		t.Fatalf("defaults: %+v", s)
	}
}

func TestCronValidation(t *testing.T) {
	base := "project: acme\nenvironment: production\nserver: hel1\nservices:\n"
	job := func(extra string) string {
		return base + "  - {name: report, kind: cron, repo: https://x/y, command: [run]" + extra + "}\n"
	}
	cases := []struct{ yaml, want string }{
		{job(`, schedule: "* * * *"`), `report: "schedule" must be five fields (minute hour day month weekday), got "* * * *"`},
		{job(`, schedule: "60 * * * *"`), `report: "schedule" minute "60" is not a number from 0 to 59`},
		{job(`, schedule: "0 3 * * *", timezone: Mars/Base`), `report: "timezone" "Mars/Base" is not a time zone`},
		{base + "  - {name: report, kind: cron, repo: https://x/y, schedule: \"0 3 * * *\"}\n", `report: a cron job needs a "command"`},
		{job(`, schedule: "0 3 * * *", timeout: 30s`), `report: "timeout" must be between 1m and 24h`},
		{job(`, schedule: "0 3 * * *", timeout: 25h`), `report: "timeout" must be between 1m and 24h`},
		{job(`, schedule: "0 3 * * *", port: 8080`), `report: a cron job has no "port"`},
		{job(`, schedule: "0 3 * * *", health_path: /up`), `report: a cron job has no "health_path"`},
		{job(`, schedule: "0 3 * * *", release: [migrate]`), `report: a cron job has no "release"`},
		{base + "  - {name: api, kind: web, repo: https://x/y, schedule: \"0 3 * * *\"}\n", `api: only a cron job has "schedule"`},
		{base + "  - {name: api, kind: worker, repo: https://x/y, timeout: 5m}\n", `api: only a cron job has "timeout"`},
		{base + "  - {name: db, kind: postgres, command: [x]}\n", `db: "command" is for a web service, a worker or a cron job`},
		{job(`, schedule: "0 3 * * *", outside_port: 15432, allow: [10.0.0.1/32]`), `report: only a database has "outside_port" and "allow"`},
		{job(`, schedule: "0 3 * * *", allow: [10.0.0.1/32]`), `report: only a database has "outside_port" and "allow"`},
		{job(`, schedule: "0 3 * * *", pitr: true`), `report: "pitr" is for Postgres only`},
		{job(`, schedule: "0 3 * * *", backup: true`), `report: only a database has "backup"`},
	}
	for _, c := range cases {
		if _, err := Parse([]byte(c.yaml)); err == nil || err.Error() != c.want {
			t.Errorf("%s\ngot  %v\nwant %s", c.yaml, err, c.want)
		}
	}
	if _, err := Parse([]byte(job(`, schedule: "0 3 * * *", timezone: Asia/Tashkent, timeout: 2h`))); err != nil {
		t.Fatal(err)
	}
}

func TestVolumesValidation(t *testing.T) {
	base := "project: acme\nenvironment: production\nserver: hel1\nservices:\n"
	svc := func(extra string) string {
		return base + "  - {name: api, kind: web, repo: https://x/y" + extra + "}\n"
	}
	cases := []struct{ yaml, want string }{
		{svc(`, volumes: [{name: Up, path: /app/uploads}]`), `api: "volumes" name "Up" must be lowercase letters, digits and dashes`},
		{svc(`, volumes: [{name: up, path: app/uploads}]`), `api: "volumes" path "app/uploads" must be absolute`},
		{svc(`, volumes: [{name: up, path: /a}, {name: up, path: /b}]`), `api: "volumes" name "up" appears twice`},
		{svc(`, volumes: [{name: a, path: /a}, {name: b, path: /b}, {name: c, path: /c}, {name: d, path: /d}, {name: e, path: /e}]`), `api: at most 4 volumes`},
		{base + "  - {name: db, kind: postgres, volumes: [{name: up, path: /a}]}\n", `db: only a web service or a worker has "volumes"`},
		// Docker refuses these at every start
		{svc(`, volumes: [{name: a, path: /data}, {name: b, path: /data/}]`), `api: "volumes" path "/data" appears twice`},
		{svc(`, volumes: [{name: a, path: /}]`), `api: "volumes" path cannot be /`},
		{svc(`, volumes: [{name: a, path: /app/..}]`), `api: "volumes" path cannot be /`},
	}
	for _, c := range cases {
		if _, err := Parse([]byte(c.yaml)); err == nil || err.Error() != c.want {
			t.Errorf("%s\ngot  %v\nwant %s", c.yaml, err, c.want)
		}
	}
	f, err := Parse([]byte(svc(`, volumes: [{name: uploads, path: /app/uploads}]`)))
	if err != nil {
		t.Fatal(err)
	}
	if s := f.Services[0]; len(s.Volumes) != 1 || s.BackupAt != "03:30" {
		t.Fatalf("%+v", s)
	}
	if f, _ := Parse([]byte(svc(""))); f.Services[0].BackupAt != "" {
		t.Fatal("a service without volumes got a backup time")
	}
}

func TestPRNameReserved(t *testing.T) {
	_, err := Parse([]byte("project: acme\nenvironment: pr-12\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n"))
	if err == nil || err.Error() != "pr-12 is the name of a preview environment; pick another name." {
		t.Fatal(err)
	}
	f, err := Parse([]byte("project: acme\nenvironment: staging\nserver: hel1\npreview: true\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n"))
	if err != nil || !f.Preview {
		t.Fatal(f.Preview, err)
	}
}

func TestPreviewDomain(t *testing.T) {
	head := "project: acme\nenvironment: staging\nserver: hel1\n"
	svc := "services:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n"
	f, err := Parse([]byte(head + "preview: true\npreview_domain: preview.acme.uz\n" + svc))
	if err != nil || f.PreviewDomain != "preview.acme.uz" {
		t.Fatal(f.PreviewDomain, err)
	}
	for in, want := range map[string]string{
		"preview_domain: preview.acme.uz\n":                 `"preview_domain" needs "preview: true"`,
		"preview: true\npreview_domain: '*.acme.uz'\n":      `"preview_domain" "*.acme.uz" must be a lowercase domain like preview.acme.uz`,
		"preview: true\npreview_domain: Preview.acme.uz\n":  `"preview_domain" "Preview.acme.uz" must be a lowercase domain like preview.acme.uz`,
		"preview: true\npreview_domain: 1-2-3-4.sslip.io\n": `"preview_domain" "1-2-3-4.sslip.io" must be a lowercase domain like preview.acme.uz`,
	} {
		if _, err := Parse([]byte(head + in + svc)); err == nil || err.Error() != want {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestServicePreviewFalse(t *testing.T) {
	head := "project: acme\nenvironment: production\nserver: hel1\n"
	svc := "services:\n  - {name: bot, kind: worker, repo: \"https://x/y\", preview: false}\n"
	if _, err := Parse([]byte(head + svc)); err == nil || err.Error() != `bot: "preview: false" leaves a service out of previews; this environment has no "preview: true"` {
		t.Fatal(err)
	}
	f, err := Parse([]byte(head + "preview: true\n" + svc))
	if err != nil || f.Services[0].PreviewOn() {
		t.Fatal(err)
	}
	b, _ := Marshal(f)
	if !strings.Contains(string(b), "preview: false") {
		t.Fatal(string(b))
	}
	if g, err := Parse(b); err != nil || g.Services[0].PreviewOn() {
		t.Fatal(err)
	}
}

func TestSteps(t *testing.T) {
	app := "  - {name: %s, kind: %s, repo: https://github.com/a/b, port: 80, steps: %s}\n"
	f, err := Parse([]byte(pgBase + fmt.Sprintf(app, "site", "web", "{install: npm ci, build: npm run build, output: dist}")))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Services[0].Steps; got != (Steps{Install: "npm ci", Build: "npm run build", Output: "dist"}) {
		t.Fatalf("steps = %+v", got)
	}
	for _, c := range []struct{ name, kind, steps, want string }{
		{"a", "web", `{build: "a\nb"}`, `"steps.build" is one line`},
		{"b", "web", "{start: " + strings.Repeat("x", maxStepLen+1) + "}", `"steps.start" is one line`},
		{"c", "worker", "{output: dist}", `"steps.output" serves a static site, so it is for a web service`},
		{"d", "web", "{output: ../dist}", `"steps.output" is the folder`},
		{"e", "web", "{output: dist, start: npm start}", `set one`},
	} {
		if _, err := Parse([]byte(pgBase + fmt.Sprintf(app, c.name, c.kind, c.steps))); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.steps, err)
		}
	}
	if _, err := Parse([]byte(pgBase + "  - {name: site, kind: web, image: nginx, port: 80, steps: {build: make}}\n")); err == nil || !strings.Contains(err.Error(), `"steps" is for apps built from a repo`) {
		t.Errorf("image with steps: %v", err)
	}
}
