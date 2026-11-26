package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/plan"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

const file = `
project: acme
environment: production
server: hel1
services:
  - {name: api, kind: web, repo: "https://x/api"}
  - {name: web, kind: web, repo: "https://x/web"}
`

func parse(t *testing.T, s string) spec.File {
	f, err := spec.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestApplyIdempotent(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	if _, err := a.Apply(ctx, parse(t, file), "owner", false); err != nil {
		t.Fatal(err)
	}
	ch, err := a.Apply(ctx, parse(t, file), "owner", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range ch {
		if c.Op != "unchanged" {
			t.Fatalf("second apply changed %+v", c)
		}
	}
}

func TestPlanDoesNotWrite(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	ch, err := a.Apply(ctx, parse(t, file), "owner", true)
	if err != nil {
		t.Fatal(err)
	}
	if ch[1].Op != "create" || ch[1].Name != "web" {
		t.Fatalf("plan %+v", ch)
	}
	if _, err := st.Q.GetServiceByName(ctx, dbName("acme", "production", "web")); err == nil {
		t.Fatal("dry run wrote a service")
	}
}

func TestApplyUnknownServer(t *testing.T) {
	st, _ := seed(t)
	_, err := New(st).Apply(context.Background(), parse(t, strings.Replace(file, "hel1", "nope", 1)), "owner", false)
	if err == nil || !strings.Contains(err.Error(), `server "nope" has not joined`) {
		t.Fatalf("got %v", err)
	}
}

func TestApplyRefusesMovingEnvironment(t *testing.T) {
	st, _ := seed(t)
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, created_at, updated_at) VALUES ('srv_2','hk1','5.6.7.8','','',1,1)`)
	_, err := New(st).Apply(ctx, parse(t, strings.Replace(file, "hel1", "hk1", 1)), "owner", false)
	if err == nil || !strings.Contains(err.Error(), "lives on hel1") {
		t.Fatalf("got %v", err)
	}
}

func TestApplyStoresAutoDeploy(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	f := parse(t, strings.Replace(file, `repo: "https://x/web"}`, `repo: "https://x/web", auto_deploy: false}`, 1))
	if _, err := a.Apply(ctx, f, "owner", false); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Q.ListAutoDeployServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Repo != "https://x/api" || rows[0].Branch != "main" {
		t.Fatalf("auto-deploy services = %+v", rows)
	}
}

func TestApplyRefusesVersionChange(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	db := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: acme-db, kind: postgres, version: %d}\n"
	if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(db, 17)), "owner", false); err != nil {
		t.Fatal(err)
	}
	if ch, err := a.Apply(ctx, parse(t, fmt.Sprintf(db, 17)), "owner", false); err != nil || ch[len(ch)-1].Op != "unchanged" {
		t.Fatalf("reapply: %+v %v", ch, err)
	}
	_, err := a.Apply(ctx, parse(t, fmt.Sprintf(db, 16)), "owner", true)
	if err == nil || err.Error() != "acme-db: changing a database's version is not in version 1." {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyDomains(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	f := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\", domains: [%s]}\n"
	if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(f, "a.uz, b.uz")), "owner", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(f, "b.uz")), "owner", false); err != nil {
		t.Fatal(err)
	}
	ds, err := st.Q.ListDomains(ctx, "svc_1")
	if err != nil || len(ds) != 1 || ds[0].Host != "b.uz" {
		t.Fatalf("domains = %+v, %v", ds, err)
	}
}

func TestApplyDomainTaken(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	if _, err := a.Apply(ctx, parse(t, "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\", domains: [a.uz]}\n"), "owner", false); err != nil {
		t.Fatal(err)
	}
	_, err := a.Apply(ctx, parse(t, "project: acme\nenvironment: staging\nserver: hel1\nservices:\n  - {name: shop, kind: web, repo: \"https://x/y\", domains: [a.uz]}\n"), "owner", false)
	if err == nil || err.Error() != "a.uz is already a domain of acme-api." {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyDomainPaths(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_2", Name: "fsn1", PublicIp: "5.6.7.8", CreatedAt: 1, UpdatedAt: 1})
	f := "project: acme\nenvironment: %s\nserver: %s\nservices:\n  - {name: %s, kind: web, repo: \"https://x/y\", domains: [%s]}\n"
	if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(f, "production", "hel1", "api", "acme.uz, acme.uz/v1")), "owner", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(f, "production", "hel1", "api", "acme.uz, acme.uz/v1/beta")), "owner", false); err != nil {
		t.Fatalf("a service changing its own path: %v", err)
	}
	if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(f, "staging", "hel1", "docs", "acme.uz/docs")), "owner", false); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ env, server, domains, want string }{
		{"edge", "fsn1", "acme.uz/blog", "shop: acme.uz is served from hel1 by acme-api; a host reaches one server, so its paths must be there too."},
		{"edge", "hel1", "acme.uz/docs/v2", "shop: acme.uz/docs/v2 overlaps acme.uz/docs of acme-docs; one host cannot have a path inside another."},
	} {
		if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(f, c.env, c.server, "shop", c.domains)), "owner", false); err == nil || err.Error() != c.want {
			t.Errorf("%s: %v", c.domains, err)
		}
	}
}

func TestApplyOutsidePortTaken(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	db := "project: acme\nenvironment: %s\nserver: hel1\nservices:\n  - {name: %s, kind: postgres, outside_port: 15432, allow: [10.0.0.1/32]}\n"
	if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(db, "production", "acme-db")), "owner", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(db, "production", "acme-db")), "owner", false); err != nil {
		t.Fatalf("re-applying the same database: %v", err)
	}
	_, err := a.Apply(ctx, parse(t, fmt.Sprintf(db, "staging", "other-db")), "owner", true)
	if err == nil || err.Error() != "other-db: outside_port 15432 is already used by acme-acme-db on hel1." {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyKeepsLimits(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	y := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/api\", cpu: 0.5, memory: 512MB}\n"
	if _, err := a.Apply(ctx, parse(t, y), "owner", false); err != nil {
		t.Fatal(err)
	}
	f, _ := a.CurrentFile(ctx, "acme", "production")
	for _, s := range f.Services {
		if s.Name == "api" && (s.CPU != 0.5 || s.Memory != 512e6) {
			t.Fatalf("%+v", s)
		}
	}
	ch, _ := a.Apply(ctx, parse(t, y), "owner", false)
	for _, c := range ch {
		if c.Name == "api" && c.Op != "unchanged" {
			t.Fatalf("%+v", c)
		}
	}
}

func TestApplyWarnsOvercommit(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	y := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/api\", memory: 5GB}\n  - {name: web, kind: web, repo: \"https://x/web\", memory: 5GB}\n"
	warn := func(ch []plan.Change) string {
		for _, c := range ch {
			if c.Op == "warn" {
				return c.Kind + " " + c.Name + " " + c.Detail
			}
		}
		return ""
	}
	ch, err := a.Apply(ctx, parse(t, y), "owner", true)
	if err != nil || warn(ch) != "" {
		t.Fatalf("no metrics yet: %v %q", err, warn(ch))
	}
	srv, _ := st.Q.GetServerByName(ctx, "hel1")
	st.DB.Exec(`INSERT INTO metric_minutes (server_id, service_id, at, mem_total) VALUES (?, '', 1, 7800000000)`, srv.ID)
	ch, err = a.Apply(ctx, parse(t, y), "owner", true)
	if err != nil || warn(ch) != "server hel1 memory limits add up to 10.0 GB; it has 7.8 GB" {
		t.Fatalf("%v %q", err, warn(ch))
	}
}

func TestApplyWarnsCountsStoredServices(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	srv, _ := st.Q.GetServerByName(ctx, "hel1")
	st.DB.Exec(`INSERT INTO metric_minutes (server_id, service_id, at, mem_total) VALUES (?, '', 1, 7800000000)`, srv.ID)
	head := "project: acme\nenvironment: production\nserver: hel1\nservices:\n"
	if _, err := a.Apply(ctx, parse(t, head+"  - {name: jobs, kind: worker, repo: \"https://x/jobs\", memory: 4GB}\n"), "owner", false); err != nil {
		t.Fatal(err)
	}
	ch, err := a.Apply(ctx, parse(t, head+"  - {name: api, kind: web, repo: \"https://x/api\", memory: 5GB}\n"), "owner", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range ch {
		if c.Op == "warn" && c.Detail == "memory limits add up to 9.0 GB; it has 7.8 GB" {
			return
		}
	}
	t.Fatalf("%+v", ch)
}

func TestApplyKeepsImage(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	y := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: grafana, kind: web, image: grafana/grafana:11}\n"
	if _, err := a.Apply(ctx, parse(t, y), "owner", false); err != nil {
		t.Fatal(err)
	}
	f, _ := a.CurrentFile(ctx, "acme", "production")
	var got string
	for _, s := range f.Services {
		if s.Name == "grafana" {
			got = s.Image
		}
	}
	if got != "grafana/grafana:11" {
		t.Fatalf("%q", got)
	}
	ch, _ := a.Apply(ctx, parse(t, y), "owner", false)
	for _, c := range ch {
		if c.Name == "grafana" && c.Op != "unchanged" {
			t.Fatalf("%+v", c)
		}
	}
}

func TestCurrentFileKeepsPreview(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	y := "project: acme\nenvironment: staging\nserver: hel1\npreview: true\nservices:\n  - {name: api, kind: web, repo: \"https://x/api\"}\n"
	if _, err := a.Apply(ctx, parse(t, y), "owner", false); err != nil {
		t.Fatal(err)
	}
	f, err := a.CurrentFile(ctx, "acme", "staging")
	if err != nil || !f.Preview {
		t.Fatalf("preview %v %v", f.Preview, err)
	}
	if _, err := a.Apply(ctx, f, "owner", false); err != nil {
		t.Fatal(err)
	}
	if f, _ = a.CurrentFile(ctx, "acme", "staging"); !f.Preview {
		t.Fatal("a save of the stored file turned previews off")
	}
}

func TestApplyStoresBackupFlag(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	v := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: cache, kind: valkey%s}\n"
	backupOf := func() int64 {
		var b int64
		st.DB.QueryRowContext(ctx, "SELECT backup FROM services WHERE name = 'cache'").Scan(&b)
		return b
	}
	if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(v, "")), "owner", false); err != nil || backupOf() != 0 {
		t.Fatalf("without: %d %v", backupOf(), err)
	}
	for _, same := range []string{"", ", backup: false"} {
		if ch, err := a.Apply(ctx, parse(t, fmt.Sprintf(v, same)), "owner", false); err != nil || ch[len(ch)-1].Op != "unchanged" {
			t.Fatalf("reapply %q: %+v %v", same, ch, err)
		}
	}
	if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(v, ", backup: true")), "owner", false); err != nil || backupOf() != 1 {
		t.Fatalf("with: %d %v", backupOf(), err)
	}
	since := func() string {
		var id string
		st.DB.QueryRowContext(ctx, "SELECT id FROM services WHERE name = 'cache'").Scan(&id)
		v, _ := st.Q.GetSetting(ctx, "backup.since."+id)
		return v
	}
	if since() == "" {
		t.Fatal("turning backup on noted no time")
	}
	if ch, _ := a.Apply(ctx, parse(t, fmt.Sprintf(v, ", backup: true")), "owner", false); ch[len(ch)-1].Op != "unchanged" {
		t.Fatalf("reapply with backup: %+v", ch)
	}
	if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(v, ", backup: true, version: 8")), "owner", false); err != nil {
		t.Fatal(err)
	}
	ch := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: events, kind: clickhouse, backup: true}\n"
	if _, err := a.Apply(ctx, parse(t, ch), "owner", false); err != nil {
		t.Fatal(err)
	}
	f, err := a.CurrentFile(ctx, "acme", "production")
	if i := slices.IndexFunc(f.Services, func(s spec.Service) bool { return s.Name == "events" }); err != nil || i < 0 || !f.Services[i].BackupOn() {
		t.Fatalf("clickhouse read back: %v", err)
	}
}

func TestApplyStoresPITR(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	f := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: db, kind: postgres, pitr: true}\n"
	if _, err := a.Apply(ctx, parse(t, f), "owner", false); err != nil {
		t.Fatal(err)
	}
	var p int
	st.DB.QueryRowContext(ctx, "SELECT pitr FROM services WHERE name = 'db'").Scan(&p)
	if ch, _ := a.Apply(ctx, parse(t, f), "owner", false); p != 1 || ch[len(ch)-1].Op != "unchanged" {
		t.Fatalf("pitr %d, reapply %+v", p, ch)
	}
}

func TestApplyStoresBuilder(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	f := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: shop, kind: web, repo: https://github.com/acme/shop, port: 8080, builder: railpack}\n"
	if _, err := a.Apply(ctx, parse(t, f), "owner", false); err != nil {
		t.Fatal(err)
	}
	var b string
	st.DB.QueryRowContext(ctx, "SELECT builder FROM services WHERE name = 'shop'").Scan(&b)
	if ch, _ := a.Apply(ctx, parse(t, f), "owner", false); b != "railpack" || ch[len(ch)-1].Op != "unchanged" {
		t.Fatalf("builder %q, reapply %+v", b, ch)
	}
}

func TestApplyStoresRoot(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	f := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: site, kind: web, repo: https://github.com/acme/shop, port: 8080, root: landing}\n"
	if _, err := a.Apply(ctx, parse(t, f), "owner", false); err != nil {
		t.Fatal(err)
	}
	var r string
	st.DB.QueryRowContext(ctx, "SELECT root FROM services WHERE name = 'site'").Scan(&r)
	if ch, _ := a.Apply(ctx, parse(t, f), "owner", false); r != "landing" || ch[len(ch)-1].Op != "unchanged" {
		t.Fatalf("root %q, reapply %+v", r, ch)
	}
	if ch, _ := a.Apply(ctx, parse(t, strings.Replace(f, "root: landing", "root: apps/web", 1)), "owner", false); ch[len(ch)-1].Op != "update" || !strings.Contains(ch[len(ch)-1].Detail, "root landing -> apps/web") {
		t.Fatalf("root change %+v", ch)
	}
}

func TestApplyStoresSteps(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	f := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: site, kind: web, repo: https://github.com/acme/shop, port: 8080, steps: {install: npm ci, build: npm run build, output: dist}}\n"
	if _, err := a.Apply(ctx, parse(t, f), "owner", false); err != nil {
		t.Fatal(err)
	}
	var js string
	st.DB.QueryRowContext(ctx, "SELECT steps_json FROM services WHERE name = 'site'").Scan(&js)
	if got := StepsOf(js); got != (spec.Steps{Install: "npm ci", Build: "npm run build", Output: "dist"}) {
		t.Fatalf("stored %q", js)
	}
	if ch, _ := a.Apply(ctx, parse(t, f), "owner", false); ch[len(ch)-1].Op != "unchanged" {
		t.Fatalf("reapply %+v", ch)
	}
	if ch, _ := a.Apply(ctx, parse(t, strings.Replace(f, "output: dist", "output: build", 1)), "owner", false); !strings.Contains(ch[len(ch)-1].Detail, "steps.output dist -> build") {
		t.Fatalf("output change %+v", ch)
	}
}

func TestApplyTurnsPITROn(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	f := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: db, kind: postgres}\n"
	a.Apply(ctx, parse(t, f), "owner", false)
	ch, err := a.Apply(ctx, parse(t, strings.Replace(f, "kind: postgres}", "kind: postgres, pitr: true}", 1)), "owner", false)
	var p int
	st.DB.QueryRowContext(ctx, "SELECT pitr FROM services WHERE name = 'db'").Scan(&p)
	if err != nil || p != 1 || ch[len(ch)-1].Op != "update" || !strings.Contains(ch[len(ch)-1].Detail, "pitr") {
		t.Fatalf("pitr %d %v %+v", p, err, ch)
	}
}

func TestApplyReplicas(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	three := strings.Replace(file, `repo: "https://x/api"}`, `repo: "https://x/api", replicas: 3}`, 1)
	if _, err := a.Apply(ctx, parse(t, three), "owner", false); err != nil {
		t.Fatal(err)
	}
	var n int
	st.DB.QueryRow(`SELECT replicas FROM services WHERE name = 'api'`).Scan(&n)
	ch, err := a.Apply(ctx, parse(t, three), "owner", false)
	if err != nil || n != 3 {
		t.Fatalf("replicas %d %v", n, err)
	}
	for _, c := range ch {
		if c.Op != "unchanged" {
			t.Fatalf("second apply changed %+v", c)
		}
	}
}

func TestApplyTags(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	tagged := strings.Replace(file, `repo: "https://x/api"}`, `repo: "https://x/api", tags: [backend, eu]}`, 1)
	if _, err := a.Apply(ctx, parse(t, tagged), "owner", false); err != nil {
		t.Fatal(err)
	}
	ch, err := a.Apply(ctx, parse(t, tagged), "owner", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range ch {
		if c.Op != "unchanged" {
			t.Fatalf("second apply changed %+v", c)
		}
	}
	cur, _ := a.CurrentFile(ctx, "acme", "production")
	if cur.Services[0].Name != "api" || strings.Join(cur.Services[0].Tags, ",") != "backend,eu" {
		t.Fatalf("%+v", cur.Services[0])
	}
}

func TestApplyKeep(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	f := file + "  - {name: db, kind: postgres, keep: {daily: 14, monthly: 6}}\n"
	if _, err := a.Apply(ctx, parse(t, f), "owner", false); err != nil {
		t.Fatal(err)
	}
	ch, _ := a.Apply(ctx, parse(t, f), "owner", false)
	for _, c := range ch {
		if c.Op != "unchanged" {
			t.Fatalf("second apply changed %+v", c)
		}
	}
	var js string
	st.DB.QueryRow(`SELECT keep_json FROM services WHERE name = 'db'`).Scan(&js)
	if js != `{"daily":14,"weekly":4,"monthly":6}` {
		t.Fatalf("%s", js)
	}
}

func TestPlanSaysKeepDeletes(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	if _, err := a.Apply(ctx, parse(t, file+"  - {name: db, kind: postgres}\n"), "owner", false); err != nil {
		t.Fatal(err)
	}
	var id string
	st.DB.QueryRow(`SELECT id FROM services WHERE name = 'db'`).Scan(&id)
	for d := range 10 {
		b := fmt.Sprint("bak_", d)
		st.Q.InsertBackup(ctx, db.InsertBackupParams{ID: b, ServiceID: id, ObjectKey: b, CreatedAt: a.Now().AddDate(0, 0, -d-1).UnixMilli(), UpdatedAt: 1})
		st.Q.FinishBackup(ctx, db.FinishBackupParams{State: "ok", UpdatedAt: 1, ID: b})
	}
	ch, err := a.Apply(ctx, parse(t, file+"  - {name: db, kind: postgres, keep: {daily: 3, weekly: 0}}\n"), "owner", true)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(ch, func(c plan.Change) bool { return c.Op == "warn" && c.Name == "db" })
	if i < 0 || ch[i].Detail != "keep 3 daily: the next good backup deletes 8 of its 10 backups" {
		t.Fatalf("%+v", ch)
	}
	if ch, _ := a.Apply(ctx, parse(t, file+"  - {name: db, kind: postgres}\n"), "owner", true); slices.ContainsFunc(ch, func(c plan.Change) bool { return c.Op == "warn" }) {
		t.Fatalf("an unchanged keep warned: %+v", ch)
	}
}

func TestApplyMovesDomainBetweenServices(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	two := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\", domains: [%s]}\n  - {name: shop, kind: web, repo: \"https://x/y\", domains: [%s]}\n"
	if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(two, "a.uz", "")), "owner", false); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{fmt.Sprintf(two, "", "a.uz"), fmt.Sprintf(two, "a.uz", "")} {
		if _, err := a.Apply(ctx, parse(t, f), "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	if o, err := st.Q.DomainOwner(ctx, "a.uz"); err != nil || o.Service != "api" {
		t.Fatalf("%+v %v", o, err)
	}
	if v, _ := st.Q.GetSetting(ctx, RemovalKey("srv_1")); strings.Contains(v, "a.uz") {
		t.Fatalf("queued for removal: %s", v)
	}
}

func TestApplyRefusesSameDockerNames(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	if _, err := a.Apply(ctx, parse(t, "project: a-b\nenvironment: c\nserver: hel1\nservices:\n  - {name: db, kind: postgres}\n"), "owner", false); err != nil {
		t.Fatal(err)
	}
	_, err := a.Apply(ctx, parse(t, "project: a\nenvironment: b-c\nserver: hel1\nservices:\n  - {name: db, kind: postgres}\n"), "owner", false)
	if err == nil || err.Error() != "a/b-c would share Docker names with a-b/c; pick another name." {
		t.Fatal(err)
	}
	if _, err := a.Apply(ctx, parse(t, "project: a-b\nenvironment: x\nserver: hel1\nservices:\n  - {name: c-db, kind: postgres}\n"), "owner", false); err != nil {
		t.Fatal(err)
	}
	_, err = a.Apply(ctx, parse(t, "project: a-b\nenvironment: x-c\nserver: hel1\nservices:\n  - {name: db, kind: postgres}\n"), "owner", false)
	if err == nil || err.Error() != "a-b/x-c/db would share Docker names with a-b/x/c-db; pick another name." {
		t.Fatal(err)
	}
}

func TestApplyStoresNoPreview(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	f := "project: acme\nenvironment: production\nserver: hel1\npreview: true\nservices:\n  - {name: site, kind: web, repo: https://github.com/acme/shop, port: 8080}\n  - {name: bot, kind: worker, repo: https://github.com/acme/shop, preview: false}\n"
	if _, err := a.Apply(ctx, parse(t, f), "owner", false); err != nil {
		t.Fatal(err)
	}
	var n int
	st.DB.QueryRowContext(ctx, "SELECT no_preview FROM services WHERE name = 'bot'").Scan(&n)
	if ch, _ := a.Apply(ctx, parse(t, f), "owner", false); n != 1 || ch[len(ch)-1].Op != "unchanged" {
		t.Fatalf("no_preview %d, reapply %+v", n, ch)
	}
	if ch, _ := a.Apply(ctx, parse(t, strings.Replace(f, "kind: web,", "kind: web, preview: true,", 1)), "owner", false); ch[len(ch)-2].Op != "unchanged" {
		t.Fatalf("explicit true %+v", ch)
	}
	if ch, _ := a.Apply(ctx, parse(t, strings.Replace(f, ", preview: false}", "}", 1)), "owner", false); ch[len(ch)-1].Op != "update" || !strings.Contains(ch[len(ch)-1].Detail, "preview false -> true") {
		t.Fatalf("turned on %+v", ch)
	}
}

func TestApplyStampsOnlyChangesThatWaitForADeploy(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	at := func(name string) int64 {
		s, err := st.Q.GetServiceByName(ctx, dbName("acme", "production", name))
		if err != nil {
			t.Fatal(err)
		}
		return s.SettingsAt
	}
	if _, err := a.Apply(ctx, parse(t, file), "owner", false); err != nil {
		t.Fatal(err)
	}
	if at("web") != 0 {
		t.Fatal("a new service has nothing waiting")
	}
	api := at("api")
	domains := strings.Replace(file, `repo: "https://x/web"}`, `repo: "https://x/web", domains: [web.uz], auto_deploy: false}`, 1)
	if _, err := a.Apply(ctx, parse(t, domains), "owner", false); err != nil {
		t.Fatal(err)
	}
	if at("web") != 0 {
		t.Fatal("domains and auto-deploy apply at once, nothing waits")
	}
	port := strings.Replace(domains, `domains: [web.uz]`, `domains: [web.uz], port: 9000`, 1)
	if _, err := a.Apply(ctx, parse(t, port), "owner", false); err != nil {
		t.Fatal(err)
	}
	if at("web") == 0 || at("api") != api {
		t.Fatalf("only web's port waits for a deploy: web %d, api %d", at("web"), at("api"))
	}
}
