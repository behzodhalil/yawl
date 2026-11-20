package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
)

const withDB = `project: acme
environment: production
server: hel1
services:
  - {name: api, kind: web, repo: "https://x/y"}
  - {name: web, kind: web, repo: "https://x/w"}
  - {name: acme-db, kind: postgres}
`

func TestDeleteNeedsName(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	res, b := call(t, srv, tok, "DELETE", "/api/services/acme/production/api", `{"confirm":"ap"}`)
	if res.StatusCode != 400 || string(b) != "Type api to delete it.\n" {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "DELETE", "/api/services/acme/production/api", `{"confirm":"api"}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestDeleteNamesUsers(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", withDB)
	testDB.Exec(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, ref, created_at, updated_at)
		SELECT 'v1', s.environment_id, s.id, 'DATABASE_URL', '', '', 'acme-db.url', 1, 1 FROM services s WHERE s.name = 'api'`)
	_, b := call(t, srv, tok, "GET", "/api/services/acme/production/acme-db/delete-info", "")
	var v struct {
		PurgeAt int64    `json:"purge_at"`
		Users   []string `json:"users"`
	}
	json.Unmarshal(b, &v)
	if len(v.Users) != 1 || v.Users[0] != "api" || v.PurgeAt == 0 {
		t.Fatalf("%s", b)
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/services/acme/production/acme-db", `{"confirm":"acme-db"}`); res.StatusCode != 204 {
		t.Fatalf("%d", res.StatusCode)
	}
}

func TestTrashListAndRestore(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", withDB)
	if res, b := call(t, srv, tok, "DELETE", "/api/environments/acme/production", `{"confirm":"production"}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	_, b := call(t, srv, tok, "GET", "/api/trash", "")
	var items []struct{ Kind, ID, Label string }
	json.Unmarshal(b, &items)
	if len(items) != 1 || items[0].Kind != "environment" || items[0].Label != "acme/production" {
		t.Fatalf("%s", b)
	}
	if res, _ := call(t, srv, tok, "POST", "/api/trash/environment/"+items[0].ID+"/restore", ""); res.StatusCode != 202 {
		t.Fatalf("%d", res.StatusCode)
	}
	if res, _ := call(t, srv, tok, "GET", "/api/services/acme/production/api", ""); res.StatusCode != 200 {
		t.Fatalf("restored service: %d", res.StatusCode)
	}
	if res, _ := call(t, srv, tok, "POST", "/api/trash/environment/"+items[0].ID+"/restore", ""); res.StatusCode != 404 {
		t.Fatalf("second restore: %d", res.StatusCode)
	}
	call(t, srv, tok, "DELETE", "/api/environments/acme/production", `{"confirm":"production"}`)
	var svcID string
	testDB.QueryRow(`SELECT id FROM services WHERE name = 'api'`).Scan(&svcID)
	if res, b := call(t, srv, tok, "POST", "/api/trash/service/"+svcID+"/restore", ""); res.StatusCode != 409 || string(b) != "Restore acme/production first.\n" {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
	_, b = call(t, srv, tok, "GET", "/api/trash", "")
	json.Unmarshal(b, &items)
	call(t, srv, tok, "POST", "/api/trash/environment/"+items[0].ID+"/restore", "")
	if res, b := call(t, srv, tok, "DELETE", "/api/projects/acme", `{"confirm":"acme"}`); res.StatusCode != 204 {
		t.Fatalf("project: %d %s", res.StatusCode, b)
	}
	_, b = call(t, srv, tok, "GET", "/api/trash", "")
	if !strings.Contains(string(b), `"kind":"project"`) {
		t.Fatalf("%s", b)
	}
}

func TestDeletedServiceIs404(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	call(t, srv, tok, "DELETE", "/api/services/acme/production/api", `{"confirm":"api"}`)
	if res, _ := call(t, srv, tok, "GET", "/api/services/acme/production/api", ""); res.StatusCode != 404 {
		t.Fatalf("%d", res.StatusCode)
	}
}

func TestDeleteStopsAtOnce(t *testing.T) {
	_, tok, a := setupApp(t)
	gw := &drainGW{}
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", GW: gw}))
	defer srv.Close()
	call(t, srv, tok, "POST", "/api/apply", yaml)
	testDB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, container, state, created_at, updated_at) SELECT 'dep_1', id, 'c', 'acme-production-api-1', 'live', 1, 1 FROM services WHERE name = 'api'`)
	testDB.Exec(`UPDATE services SET current_deploy_id = 'dep_1' WHERE name = 'api'`)
	if res, b := call(t, srv, tok, "DELETE", "/api/services/acme/production/api", `{"confirm":"api"}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var stopped int
	testDB.QueryRow(`SELECT stopped FROM services WHERE name = 'api'`).Scan(&stopped)
	if stopped != 1 || len(gw.sent) == 0 || gw.sent[len(gw.sent)-1].Name != proto.CmdStop {
		t.Fatalf("stopped %d, sent %d", stopped, len(gw.sent))
	}
}
