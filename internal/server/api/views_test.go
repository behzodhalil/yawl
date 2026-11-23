package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

func mustApply(t *testing.T, a *app.App, y string) {
	t.Helper()
	f, err := spec.Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(context.Background(), f, "test", false); err != nil {
		t.Fatal(err)
	}
}

func TestProjectsTree(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, "project: zeta\nenvironment: staging\nserver: hel1\nservices:\n  - {name: web, kind: web, repo: \"https://x/w\"}\n  - {name: api, kind: web, repo: \"https://x/a\"}\n")
	mustApply(t, a, "project: zeta\nenvironment: production\nserver: hel1\nservices: []\n")
	mustApply(t, a, "project: acme\nenvironment: production\nserver: hel1\nservices: []\n")
	res, b := call(t, srv, tok, "GET", "/api/projects", "")
	var out []ProjectView
	if json.Unmarshal(b, &out); res.StatusCode != 200 || len(out) != 2 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if out[0].Name != "acme" || out[1].Name != "zeta" || out[1].Envs[0].Name != "production" || out[1].Envs[1].Name != "staging" {
		t.Fatalf("order: %s", b)
	}
	st := out[1].Envs[1]
	if st.Server != "hel1" || len(st.Services) != 2 || st.Services[0].Service != "api" || st.Services[1].Service != "web" || st.Services[0].Health != "ok" {
		t.Fatalf("services: %s", b)
	}
	if len(out[0].Envs[0].Services) != 0 || out[0].Envs[0].Services == nil {
		t.Fatalf("an empty environment must list [] services: %s", b)
	}
}

const full = `project: acme
environment: production
server: hel1
services:
  - name: api
    kind: web
    repo: "https://github.com/acme/api"
    branch: dev
    port: 8080
    health_path: /up
    health_timeout: 90s
    drain_timeout: 45s
    auto_deploy: false
    release: ["/app/migrate", "up"]
    release_timeout: 3m
    domains: [api.acme.uz]
  - name: db
    kind: postgres
    version: 16
    backup_at: "03:30"
    outside_port: 15432
    allow: ["203.0.113.4/32"]
`

func TestServiceView(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, full)
	res, b := call(t, srv, tok, "GET", "/api/services/acme/production/api", "")
	var v ServiceView
	if json.Unmarshal(b, &v); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	want := ServiceForm{Name: "api", Kind: "web", Repo: "https://github.com/acme/api", Branch: "dev", Port: 8080, HealthPath: "/up",
		HealthTimeoutSec: 90, DrainTimeoutSec: 45, AutoDeploy: false, Release: []string{"/app/migrate", "up"}, ReleaseTimeoutSec: 180,
		Domains: []string{"api.acme.uz"}, Allow: []string{}, Command: []string{}, Volumes: []spec.Volume{}, Watch: []string{}, Replicas: 1}
	if !reflect.DeepEqual(v.Form, want) {
		t.Fatalf("form\n got %+v\nwant %+v", v.Form, want)
	}
	if v.Server != "hel1" || v.URL != "https://api-production.1-2-3-4.sslip.io" || v.Health != "ok" ||
		len(v.Domains) != 1 || v.Domains[0].Host != "api.acme.uz" || v.Domains[0].DNSState != "pending" {
		t.Fatalf("view %s", b)
	}
	a.St.Q.SetDomainState(context.Background(), db.SetDomainStateParams{DnsState: "ok", CertState: "issued", ID: domainID(t, a, "api.acme.uz")})
	if _, b = call(t, srv, tok, "GET", "/api/services/acme/production/api", ""); !strings.Contains(string(b), `"url":"https://api.acme.uz"`) {
		t.Fatalf("view %s", b)
	}
	for _, p := range []string{"/api/status", "/api/projects"} {
		if _, b := call(t, srv, tok, "GET", p, ""); !strings.Contains(string(b), `"url":"https://api.acme.uz"`) {
			t.Fatalf("%s %s", p, b)
		}
	}
	_, b = call(t, srv, tok, "GET", "/api/services/acme/production/db", "")
	v = ServiceView{}
	json.Unmarshal(b, &v)
	if v.Form.Version != 16 || v.Form.BackupAt != "03:30" || v.Form.OutsidePort != 15432 || !reflect.DeepEqual(v.Form.Allow, []string{"203.0.113.4/32"}) || v.URL != "" {
		t.Fatalf("db form %s", b)
	}
	if res, _ := call(t, srv, tok, "GET", "/api/services/acme/production/nope", ""); res.StatusCode != 404 {
		t.Fatalf("missing service: %d", res.StatusCode)
	}
}

func TestServiceViewUsedBy(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, full)
	mustApply(t, a, "project: acme\nenvironment: staging\nserver: hel1\nservices:\n  - {name: web, kind: web, repo: \"https://x/w\"}\n  - {name: jobs, kind: worker, repo: \"https://x/j\"}\n  - {name: db, kind: postgres, version: 16}\n")
	mustApply(t, a, "project: zeta\nenvironment: production\nserver: hel1\nservices:\n  - {name: app, kind: web, repo: \"https://x/a\"}\n")
	ref := func(project, env, service, ref string) {
		s, err := a.St.Q.GetServiceByName(context.Background(), db.GetServiceByNameParams{Project: project, Env: env, Service: service})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.St.DB.Exec(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, ref, created_at, updated_at)
			VALUES (?, ?, ?, 'DATABASE_URL', '', '', ?, 1, 1)`, "var_"+project+env+service, s.EnvironmentID, s.ID, ref); err != nil {
			t.Fatal(err)
		}
	}
	ref("acme", "production", "api", "db.url")
	ref("acme", "staging", "web", "production/db.url")
	ref("acme", "staging", "jobs", "db.url")
	ref("zeta", "production", "app", "production/db.url")
	_, b := call(t, srv, tok, "GET", "/api/services/acme/production/db", "")
	var v ServiceView
	json.Unmarshal(b, &v)
	if want := []UsedByView{{Env: "production", Service: "api"}, {Env: "staging", Service: "web"}}; !reflect.DeepEqual(v.UsedBy, want) {
		t.Fatalf("%s", b)
	}
	if _, b = call(t, srv, tok, "GET", "/api/services/acme/production/api", ""); strings.Contains(string(b), "used_by") {
		t.Fatalf("an app has no users: %s", b)
	}
}

func TestDeployList(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, full)
	svc, _ := a.St.Q.GetServiceByName(context.Background(), db.GetServiceByNameParams{Project: "acme", Env: "production", Service: "api"})
	first, _ := a.Deploy(context.Background(), svc.ID, "aaa", "test")
	_, b := call(t, srv, tok, "GET", "/api/services/acme/production/api/deploys?limit=5", "")
	var ds []DeployRow
	if json.Unmarshal(b, &ds); len(ds) != 1 || ds[0].ID != first || ds[0].State != "queued" || ds[0].Commit != "aaa" || ds[0].CreatedAt == 0 {
		t.Fatalf("%s", b)
	}
	if res, _ := call(t, srv, tok, "GET", "/api/services/acme/production/api/deploys?limit=101", ""); res.StatusCode != 400 {
		t.Fatalf("limit 101: %d", res.StatusCode)
	}
}

func TestDeployWarnings(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, full)
	ctx := context.Background()
	svc, _ := a.St.Q.GetServiceByName(ctx, db.GetServiceByNameParams{Project: "acme", Env: "production", Service: "api"})
	id, _ := a.Deploy(ctx, svc.ID, "", "test")
	for i, w := range []string{"The code reads B, which is not set in production.", "The code reads A, which is not set in production."} {
		app.Event(ctx, a.St.Q, int64(10+i), "deploy.warning", id, "system", map[string]string{"service": svc.ID, "text": w})
	}
	_, b := call(t, srv, tok, "GET", "/api/deploys/"+id, "")
	var v DeployView
	json.Unmarshal(b, &v)
	if len(v.Warnings) != 2 || !strings.Contains(v.Warnings[0], "reads B") || !strings.Contains(v.Warnings[1], "reads A") || v.CreatedAt == 0 || v.Rollback {
		t.Fatalf("%s", b)
	}
}

func TestServersList(t *testing.T) {
	srv, tok, _ := setupApp(t)
	_, b := call(t, srv, tok, "GET", "/api/servers", "")
	var out []ServerView
	if json.Unmarshal(b, &out); len(out) != 1 || out[0].Name != "hel1" || out[0].PublicIP != "1.2.3.4" {
		t.Fatalf("%s", b)
	}
	if strings.Contains(string(b), "agent_pubkey") || strings.Contains(string(b), "cert_serial") {
		t.Fatalf("leaks agent keys: %s", b)
	}
}

func TestJoinTokenCommand(t *testing.T) {
	srv, tok, _ := setupApp(t)
	res, b := call(t, srv, tok, "POST", "/api/join-token", "")
	var out struct{ Command string }
	json.Unmarshal(b, &out)
	parts := strings.Fields(out.Command)
	if res.StatusCode != 200 || len(parts) != 5 || parts[0] != "yawl" || parts[3] != "http://panel.test" {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var n int
	testDB.QueryRow(`SELECT COUNT(*) FROM join_tokens WHERE used_at IS NULL`).Scan(&n)
	if n != 1 {
		t.Fatal("no join token stored")
	}
}

func TestDeployRowsSayWhoAndHowLong(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, full)
	ctx := context.Background()
	svc, _ := a.St.Q.GetServiceByName(ctx, db.GetServiceByNameParams{Project: "acme", Env: "production", Service: "api"})
	old, _ := a.Deploy(ctx, svc.ID, "aaa", "dilnoza")
	bad, _ := a.Deploy(ctx, svc.ID, "bbb", "dilnoza")
	cur, _ := a.Deploy(ctx, svc.ID, "ccc", "owner")
	a.St.DB.Exec(`UPDATE deploys SET state = 'live', image = 'img:1', subject = 'Show totals', started_at = 1000, finished_at = 3000, created_at = 10 WHERE id = ?`, old)
	a.St.DB.Exec(`UPDATE deploys SET state = 'failed', created_at = 20 WHERE id = ?`, bad)
	a.St.DB.Exec(`UPDATE deploys SET state = 'live', image = 'img:3', created_at = 30 WHERE id = ?`, cur)
	a.St.DB.Exec(`UPDATE services SET current_deploy_id = ? WHERE id = ?`, cur, svc.ID)
	_, b := call(t, srv, tok, "GET", "/api/services/acme/production/api/deploys", "")
	var ds []DeployRow
	json.Unmarshal(b, &ds)
	by := map[string]DeployRow{}
	for _, d := range ds {
		by[d.ID] = d
	}
	if d := by[cur]; !d.Live || d.CanRollBack || d.By != "owner" {
		t.Fatalf("current: %s", b)
	}
	if d := by[old]; d.Live || !d.CanRollBack || d.TookMs != 2000 || d.By != "dilnoza" || d.Subject != "Show totals" {
		t.Fatalf("older live: %s", b)
	}
	if d := by[bad]; d.Live || d.CanRollBack || d.TookMs != 0 || d.AutoRolledBack {
		t.Fatalf("failed: %s", b)
	}
	a.St.DB.Exec(`UPDATE deploys SET auto_rolled_back = 1 WHERE id = ?`, old)
	_, b = call(t, srv, tok, "GET", "/api/services/acme/production/api/deploys", "")
	json.Unmarshal(b, &ds)
	for _, d := range ds {
		if d.ID == old && (!d.AutoRolledBack || d.CanRollBack) {
			t.Fatalf("rolled away from: %s", b)
		}
	}
}

func TestDeployViewHasStepTimes(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, full)
	ctx := context.Background()
	svc, _ := a.St.Q.GetServiceByName(ctx, db.GetServiceByNameParams{Project: "acme", Env: "production", Service: "api"})
	id, _ := a.Deploy(ctx, svc.ID, "", "dilnoza")
	a.St.Q.AddDeployStep(ctx, db.AddDeployStepParams{ID: "st_1", DeployID: id, Step: "build", StartedAt: 1000})
	a.St.Q.FinishDeployStep(ctx, db.FinishDeployStepParams{State: "ok", FinishedAt: sql.NullInt64{Int64: 113000, Valid: true}, DeployID: id, Step: "build"})
	a.St.Q.AddDeployStep(ctx, db.AddDeployStepParams{ID: "st_2", DeployID: id, Step: "start", StartedAt: 114000})
	_, b := call(t, srv, tok, "GET", "/api/deploys/"+id, "")
	var v DeployView
	json.Unmarshal(b, &v)
	if len(v.Steps) != 2 || v.Steps[0].StartedAt != 1000 || v.Steps[0].FinishedAt != 113000 || v.Steps[1].FinishedAt != 0 || v.By != "dilnoza" {
		t.Fatalf("%s", b)
	}
}

func domainID(t *testing.T, a *app.App, host string) string {
	t.Helper()
	o, err := a.St.Q.DomainOwner(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	ds, _ := a.St.Q.ListDomains(context.Background(), o.ServiceID)
	for _, d := range ds {
		if d.Host == host {
			return d.ID
		}
	}
	t.Fatalf("no domain %s", host)
	return ""
}

func TestOfflineServerFlagsEnvAndService(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, full)
	_, b := call(t, srv, tok, "GET", "/api/projects", "")
	if strings.Contains(string(b), "server_offline") {
		t.Fatalf("online server flagged: %s", b)
	}
	if _, err := a.St.DB.Exec(`UPDATE servers SET offline = 1, last_heartbeat = 1700000000000`); err != nil {
		t.Fatal(err)
	}
	_, b = call(t, srv, tok, "GET", "/api/projects", "")
	var out []ProjectView
	json.Unmarshal(b, &out)
	if e := out[0].Envs[0]; !e.ServerOffline || e.ServerLastSeen != 1700000000000 {
		t.Fatalf("env %s", b)
	}
	_, b = call(t, srv, tok, "GET", "/api/services/acme/production/api", "")
	var v ServiceView
	json.Unmarshal(b, &v)
	if !v.ServerOffline || v.ServerLastSeen != 1700000000000 {
		t.Fatalf("service %s", b)
	}
}
