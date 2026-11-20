package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func statusSeed(t *testing.T) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('prj_1','acme',1,1)`,
		`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_1','prj_1','production','srv_1',1,1)`,
		`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, current_deploy_id, created_at, updated_at) VALUES ('svc_1','env_1','secret-admin','web','r','main',10000,'/',1,1,'dep_1',1,1)`,
		`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, created_at, updated_at) VALUES ('svc_c','env_1','report','cron','r','main',0,'',1,1,1,1)`,
		`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_1','svc_1','abc','live',1,1)`,
		`INSERT INTO domains (id, service_id, host, created_at, updated_at) VALUES ('dom_1','svc_1','api.acme.uz',1,1)`,
	} {
		if _, err := testDB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStatusChoicesSkipTrash(t *testing.T) {
	srv, tok := setup(t)
	statusSeed(t)
	for _, q := range []string{
		`INSERT INTO projects (id, name, deleted_at, created_at, updated_at) VALUES ('prj_t','old',5,1,1)`,
		`INSERT INTO environments (id, project_id, name, server_id, deleted_at, created_at, updated_at) VALUES ('env_t','prj_t','production','srv_1',5,1,1)`,
		`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, deleted_at, created_at, updated_at) VALUES ('svc_t','env_t','web','web','r','main',10001,'/',1,1,5,1,1)`,
	} {
		if _, err := testDB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	res, b := call(t, srv, tok, "GET", "/api/status-pages", "")
	if res.StatusCode != 200 || !strings.Contains(string(b), `"subject":"project:prj_1"`) || strings.Contains(string(b), "prj_t") || strings.Contains(string(b), "svc_t") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/status-pages", `{"title":"Old","slug":"old","server_id":"srv_1","rows":[{"name":"Old","subject":"project:prj_t"}]}`); res.StatusCode != 400 || !strings.Contains(string(b), "A row covers") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestStatusPagesAPI(t *testing.T) {
	srv, tok := setup(t)
	statusSeed(t)
	res, b := call(t, srv, tok, "GET", "/api/status-pages", "")
	if res.StatusCode != 200 || !strings.Contains(string(b), `{"subject":"service:svc_1","label":"acme/production/secret-admin"}`) ||
		!strings.Contains(string(b), `"subject":"project:prj_1"`) || !strings.Contains(string(b), `"subject":"panel"`) || strings.Contains(string(b), "svc_c") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if _, err := testDB.Exec(`INSERT INTO domains (id, service_id, host, created_at, updated_at) VALUES ('dom_p','svc_1','docs.acme.uz/api',1,1)`); err != nil {
		t.Fatal(err)
	}
	for body, want := range map[string]string{
		`{"title":"","slug":"acme","server_id":"srv_1"}`:                                                       "A title is needed.",
		`{"title":"Acme","slug":"Acme!","server_id":"srv_1"}`:                                                  "The short name",
		`{"title":"Acme","slug":"acme","server_id":"srv_9"}`:                                                   "Choose a server",
		`{"title":"Acme","slug":"acme","server_id":"srv_1","domain":"api.acme.uz"}`:                            "already points to a service",
		`{"title":"Acme","slug":"acme","server_id":"srv_1","domain":"docs.acme.uz"}`:                           "already points to a service",
		`{"title":"Acme","slug":"acme","server_id":"srv_1","domain":"x.sslip.io"}`:                             "is not a domain name",
		`{"title":"Acme","slug":"acme","server_id":"srv_1","rows":[{"name":"Job","subject":"service:svc_c"}]}`: "A row covers",
		`{"title":"Acme","slug":"acme","server_id":"srv_1","rows":[{"name":" ","subject":"panel"}]}`:           "public name is needed",
	} {
		if res, b := call(t, srv, tok, "POST", "/api/status-pages", body); res.StatusCode != 400 || !strings.Contains(string(b), want) {
			t.Errorf("%s: %d %s", body, res.StatusCode, b)
		}
	}
	res, b = call(t, srv, tok, "POST", "/api/status-pages", `{"title":"Acme","slug":"acme","server_id":"srv_1","domain":"Status.Acme.uz.","watcher":true,
		"rows":[{"name":"API","subject":"service:svc_1"},{"name":"Dashboard","subject":"panel"}]}`)
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var page struct {
		ID     string
		Domain string
		Rows   []struct{ ID, Name string }
	}
	json.Unmarshal(b, &page)
	if page.Domain != "status.acme.uz" || len(page.Rows) != 2 || !strings.HasPrefix(page.Rows[0].ID, "sr_") {
		t.Fatalf("%s", b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/status-pages", `{"title":"Other","slug":"acme","server_id":"srv_1"}`); res.StatusCode != 400 || !strings.Contains(string(b), "Another status page") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if _, b := call(t, srv, tok, "GET", "/api/status-pages", ""); !strings.Contains(string(b), `"url":"https://acme.status.1-2-3-4.sslip.io"`) {
		t.Fatalf("%s", b)
	}

	pi := "/api/status-pages/" + page.ID + "/incidents"
	if res, b := call(t, srv, tok, "POST", pi, `{"kind":"incident","title":"Slow API","rows":["nope"],"body":"x"}`); res.StatusCode != 400 {
		t.Fatalf("a row of another page: %d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", pi, `{"kind":"maintenance","title":"Upgrade","starts_at":5,"ends_at":4}`); res.StatusCode != 400 || !strings.Contains(string(b), "Maintenance needs") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	res, b = call(t, srv, tok, "POST", pi, `{"kind":"incident","title":"Slow API","rows":["`+page.Rows[0].ID+`"],"body":"We are looking into it."}`)
	var inc struct{ ID, State string }
	json.Unmarshal(b, &inc)
	if res.StatusCode != 200 || inc.State != "draft" {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	preview := func() string {
		res, b := call(t, srv, tok, "GET", "/api/status-pages/"+page.ID+"/preview", "")
		if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
			t.Fatalf("%d %s", res.StatusCode, b)
		}
		return string(b)
	}
	if p := preview(); strings.Contains(p, "Slow API") || !strings.Contains(p, "API") || strings.Contains(p, "secret-admin") || strings.Contains(p, "1.2.3.4") {
		t.Fatalf("%s", p)
	}
	if res, _ := call(t, srv, tok, "POST", "/api/status-incidents/"+inc.ID+"/publish", ""); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	if p := preview(); !strings.Contains(p, "Slow API") || !strings.Contains(p, "Some services are degraded") {
		t.Fatalf("%s", p)
	}
	if res, _ := call(t, srv, tok, "POST", "/api/status-incidents/"+inc.ID+"/updates", `{"body":"Fixed.","resolve":true}`); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	if res, b := call(t, srv, tok, "POST", "/api/status-incidents/"+inc.ID+"/updates", `{"body":"again","resolve":true}`); res.StatusCode != 400 {
		t.Fatalf("resolved twice: %d %s", res.StatusCode, b)
	}
	if _, b := call(t, srv, tok, "GET", pi, ""); !strings.Contains(string(b), `"body":"Fixed."`) || !strings.Contains(string(b), `"state":"published"`) {
		t.Fatalf("%s", b)
	}
	if p := preview(); !strings.Contains(p, "Recent incidents") || !strings.Contains(p, "All services are operational") {
		t.Fatalf("%s", p)
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/status-incidents/"+inc.ID, ""); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/status-pages/"+page.ID, ""); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	if res, _ := call(t, srv, tok, "PUT", "/api/status-pages/"+page.ID, `{"title":"Acme","slug":"acme","server_id":"srv_1"}`); res.StatusCode != 404 {
		t.Fatal("saved a deleted page")
	}
}

func TestStatusPagesRoles(t *testing.T) {
	srv, tok := setup(t)
	statusSeed(t)
	_, b := call(t, srv, tok, "POST", "/api/status-pages", `{"title":"Acme","slug":"acme","server_id":"srv_1","rows":[{"name":"API","subject":"service:svc_1"}]}`)
	var page struct{ ID string }
	json.Unmarshal(b, &page)
	dev := memberToken(t, srv, tok, "dev@acme.uz", "developer")
	if res, b := call(t, srv, dev, "POST", "/api/status-pages", `{"title":"Mine","slug":"mine","server_id":"srv_1"}`); res.StatusCode != 403 || !strings.Contains(string(b), "Developers cannot set up status pages.") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, dev, "POST", "/api/status-pages/"+page.ID+"/incidents", `{"kind":"incident","title":"Down","publish":true}`); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}
