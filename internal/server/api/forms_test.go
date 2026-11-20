package api

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/plan"
	"github.com/behzodhalil/yawl/internal/spec"
)

const three = `project: acme
environment: production
server: hel1
services:
  - name: api
    kind: web
    repo: "https://x/api"
    drain_timeout: 45s
    auto_deploy: false
    release: ["/app", "migrate"]
    release_timeout: 3m
  - {name: web, kind: web, repo: "https://x/web"}
  - {name: db, kind: postgres}
`

func changesOf(t *testing.T, b []byte) []plan.Change {
	t.Helper()
	var out struct{ Changes []plan.Change }
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	return out.Changes
}

func TestServiceFormLeavesOthers(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, three)
	ctx := context.Background()
	before, err := a.CurrentFile(ctx, "acme", "production")
	if err != nil {
		t.Fatal(err)
	}
	_, b := call(t, srv, tok, "GET", "/api/services/acme/production/api", "")
	var v ServiceView
	json.Unmarshal(b, &v)
	v.Form.HealthPath = "/health"
	form, _ := json.Marshal(v.Form)

	res, b := call(t, srv, tok, "POST", "/api/environments/acme/production/services?dry=1", string(form))
	ch := changesOf(t, b)
	if res.StatusCode != 200 || len(ch) != 3 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	for _, c := range ch {
		switch c.Name {
		case "api":
			if c.Op != "update" || !strings.Contains(c.Detail, "health_path") || strings.Contains(c.Detail, "drain") || strings.Contains(c.Detail, "release") {
				t.Fatalf("api change: %+v", c)
			}
		default:
			if c.Op != "unchanged" {
				t.Fatalf("%s changed: %+v", c.Name, c)
			}
		}
	}
	if after, _ := a.CurrentFile(ctx, "acme", "production"); !reflect.DeepEqual(after, before) {
		t.Fatal("the dry run wrote")
	}
	if res, b := call(t, srv, tok, "POST", "/api/environments/acme/production/services", string(form)); res.StatusCode != 200 {
		t.Fatalf("apply %d %s", res.StatusCode, b)
	}
	after, _ := a.CurrentFile(ctx, "acme", "production")
	before.Services[0].HealthPath = "/health"
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("after apply\n got %+v\nwant %+v", after, before)
	}
}

func TestServiceFormNew(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, three)
	res, b := call(t, srv, tok, "POST", "/api/environments/acme/production/services", `{"name":"jobs","kind":"worker","repo":"https://x/jobs","auto_deploy":true}`)
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	ops := map[string]plan.Op{}
	for _, c := range changesOf(t, b) {
		ops[c.Name] = c.Op
	}
	if ops["jobs"] != "create" || ops["api"] != "unchanged" || ops["db"] != "unchanged" {
		t.Fatalf("%v", ops)
	}
	f, _ := a.CurrentFile(context.Background(), "acme", "production")
	if len(f.Services) != 4 || f.Services[2].Name != "jobs" || f.Services[2].Branch != "main" {
		t.Fatalf("%+v", f.Services)
	}
}

func TestServiceFormInvalid(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, three)
	res, b := call(t, srv, tok, "POST", "/api/environments/acme/production/services", `{"name":"Api","kind":"web","repo":"https://x/a"}`)
	if res.StatusCode != 400 || !strings.Contains(string(b), "lowercase letters") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, tok, "POST", "/api/environments/acme/nowhere/services", `{"name":"api","kind":"web"}`); res.StatusCode != 404 {
		t.Fatalf("missing env: %d", res.StatusCode)
	}
}

func TestProjectCreate(t *testing.T) {
	srv, tok, a := setupApp(t)
	res, b := call(t, srv, tok, "POST", "/api/projects", `{"name":"acme","server":"hel1"}`)
	if res.StatusCode != 200 || changesOf(t, b)[0].Kind != "environment" {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if _, err := a.CurrentFile(context.Background(), "acme", "production"); err != nil {
		t.Fatal(err)
	}
	if res, b := call(t, srv, tok, "POST", "/api/projects", `{"name":"acme","server":"hel1"}`); res.StatusCode != 409 || !strings.Contains(string(b), "already exists") {
		t.Fatalf("again: %d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/projects", `{"name":"shop","server":"hk1"}`); res.StatusCode != 409 || !strings.Contains(string(b), "has not joined") {
		t.Fatalf("unjoined: %d %s", res.StatusCode, b)
	}
}

func TestStagingOnly(t *testing.T) {
	srv, tok, _ := setupApp(t)
	call(t, srv, tok, "POST", "/api/projects", `{"name":"acme","server":"hel1"}`)
	if res, b := call(t, srv, tok, "POST", "/api/environments", `{"project":"acme","env":"qa","server":"hel1"}`); res.StatusCode != 400 ||
		!strings.Contains(string(b), "The dashboard makes production and staging environments.") {
		t.Fatalf("qa: %d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/environments", `{"project":"acme","env":"staging","server":"hel1"}`); res.StatusCode != 200 {
		t.Fatalf("staging: %d %s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, tok, "POST", "/api/environments", `{"project":"nope","env":"staging","server":"hel1"}`); res.StatusCode != 404 {
		t.Fatalf("unknown project: %d", res.StatusCode)
	}
}

func TestNewServiceRefusesExistingName(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, three)
	res, b := call(t, srv, tok, "POST", "/api/environments/acme/production/services?new=1&dry=1", `{"name":"api","kind":"web","repo":"https://x/other"}`)
	if res.StatusCode != 409 || !strings.Contains(string(b), "api already exists in production.") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	res, b = call(t, srv, tok, "POST", "/api/environments/acme/production/services", `{"name":"db","kind":"web","repo":"https://x/db"}`)
	if res.StatusCode != 409 || !strings.Contains(string(b), "db is a postgres service; changing a service's kind is not in version 1.") {
		t.Fatalf("kind change: %d %s", res.StatusCode, b)
	}
}

func TestServiceFormCron(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, three)
	res, b := call(t, srv, tok, "POST", "/api/environments/acme/production/services",
		`{"name":"report","kind":"cron","repo":"https://x/jobs","auto_deploy":true,"schedule":"0 3 * * *","timezone":"Asia/Tashkent","command":["node","report.js"],"timeout":7200}`)
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	_, b = call(t, srv, tok, "GET", "/api/services/acme/production/report", "")
	for _, want := range []string{`"schedule":"0 3 * * *"`, `"timezone":"Asia/Tashkent"`, `"command":["node","report.js"]`, `"timeout":7200`, `"kind":"cron"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in %s", want, b)
		}
	}
	res, b = call(t, srv, tok, "POST", "/api/environments/acme/production/services",
		`{"name":"bad","kind":"cron","repo":"https://x/jobs","schedule":"0 3 * *","command":["x"]}`)
	if res.StatusCode != 400 || !strings.Contains(string(b), `bad: "schedule" must be five fields`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestServiceFormLimits(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, three)
	_, b := call(t, srv, tok, "GET", "/api/services/acme/production/api", "")
	var v ServiceView
	json.Unmarshal(b, &v)
	v.Form.CPUCores, v.Form.Memory = 0.5, "512 MB"
	form, _ := json.Marshal(v.Form)
	if res, b := call(t, srv, tok, "POST", "/api/environments/acme/production/services", string(form)); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	_, b = call(t, srv, tok, "GET", "/api/services/acme/production/api", "")
	v = ServiceView{}
	json.Unmarshal(b, &v)
	if v.Form.CPUCores != 0.5 || v.Form.Memory != "512MB" || v.MemoryLimit != 512e6 {
		t.Fatalf("%s", b)
	}
	v.Form.Memory = "lots"
	form, _ = json.Marshal(v.Form)
	if res, b := call(t, srv, tok, "POST", "/api/environments/acme/production/services", string(form)); res.StatusCode != 400 || !strings.Contains(string(b), `"memory"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestServiceFormKeepsReplicas(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, strings.Replace(three, "    drain_timeout: 45s\n", "    drain_timeout: 45s\n    replicas: 3\n    tags: [backend, eu]\n", 1))
	_, b := call(t, srv, tok, "GET", "/api/services/acme/production/api", "")
	var v ServiceView
	json.Unmarshal(b, &v)
	form, _ := json.Marshal(v.Form)
	_, b = call(t, srv, tok, "POST", "/api/environments/acme/production/services?dry=1", string(form))
	for _, c := range changesOf(t, b) {
		if c.Op != "unchanged" {
			t.Fatalf("saving the form as it was changed %+v", c)
		}
	}
}

func TestProjectsCarryTags(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, strings.Replace(three, "    drain_timeout: 45s\n", "    drain_timeout: 45s\n    tags: [backend]\n", 1))
	_, b := call(t, srv, tok, "GET", "/api/projects", "")
	var ps []ProjectView
	json.Unmarshal(b, &ps)
	for _, s := range ps[0].Envs[0].Services {
		if s.Service == "api" && strings.Join(s.Tags, ",") == "backend" {
			return
		}
	}
	t.Fatalf("%s", b)
}

func TestServiceFormKeepsKeep(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, strings.Replace(three, "{name: db, kind: postgres}", "{name: db, kind: postgres, keep: {daily: 14, monthly: 6}}", 1))
	_, b := call(t, srv, tok, "GET", "/api/services/acme/production/db", "")
	var v ServiceView
	json.Unmarshal(b, &v)
	form, _ := json.Marshal(v.Form)
	_, b = call(t, srv, tok, "POST", "/api/environments/acme/production/services?dry=1", string(form))
	for _, c := range changesOf(t, b) {
		if c.Op != "unchanged" {
			t.Fatalf("saving the form as it was changed %+v", c)
		}
	}
}

func TestServiceFormKeepsApplyInBetween(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, three)
	_, b := call(t, srv, tok, "GET", "/api/services/acme/production/api", "")
	var v ServiceView
	json.Unmarshal(b, &v)
	v.Form.HealthPath = "/health"
	form, _ := json.Marshal(v.Form)
	f, err := spec.Parse([]byte(strings.Replace(three, `repo: "https://x/web"}`, `repo: "https://x/web", health_path: /hz}`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	testAfterRead = func() {
		done := make(chan struct{})
		go func() { a.Apply(context.Background(), f, "cli", false); close(done) }()
		select {
		case <-done:
		case <-time.After(300 * time.Millisecond):
		}
	}
	defer func() { testAfterRead = nil }()
	if res, b := call(t, srv, tok, "POST", "/api/environments/acme/production/services", string(form)); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	time.Sleep(300 * time.Millisecond)
	cur, _ := a.CurrentFile(context.Background(), "acme", "production")
	for _, s := range cur.Services {
		if s.Name == "web" && s.HealthPath != "/hz" {
			t.Fatalf("the apply in between was reverted: %+v", s)
		}
	}
}

func TestServiceFormKeepsSubSecond(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, strings.Replace(three, "drain_timeout: 45s", "drain_timeout: 1500ms", 1))
	_, b := call(t, srv, tok, "GET", "/api/services/acme/production/api", "")
	var v ServiceView
	json.Unmarshal(b, &v)
	v.Form.HealthPath = "/health"
	form, _ := json.Marshal(v.Form)
	if res, b := call(t, srv, tok, "POST", "/api/environments/acme/production/services", string(form)); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	cur, _ := a.CurrentFile(context.Background(), "acme", "production")
	if d := cur.Services[0].DrainTimeout; d != 1500*time.Millisecond {
		t.Fatalf("drain_timeout %v", d)
	}
}
