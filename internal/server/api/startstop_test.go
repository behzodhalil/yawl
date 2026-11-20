package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAPIStartStop(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	base := "/api/services/acme/production/api/"
	if res, b := call(t, srv, tok, "POST", base+"restart", ""); res.StatusCode != 409 || !strings.Contains(string(b), "has not been deployed yet") {
		t.Fatalf("restart: %d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", base+"stop", ""); res.StatusCode != 202 {
		t.Fatalf("stop: %d %s", res.StatusCode, b)
	}
	var n int
	testDB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'service.stopped'`).Scan(&n)
	if n != 1 {
		t.Fatalf("events %d", n)
	}
	if res, b := call(t, srv, tok, "POST", base+"start", ""); res.StatusCode != 409 || !strings.Contains(string(b), "has not been deployed yet") {
		t.Fatalf("start: %d %s", res.StatusCode, b)
	}
	call(t, srv, tok, "POST", base+"stop", "")
	call(t, srv, tok, "POST", base+"deploys", `{"commit":""}`)
	var us int
	testDB.QueryRow(`SELECT user_stopped FROM services WHERE name = 'api'`).Scan(&us)
	if us != 0 {
		t.Fatal("a manual deploy left it stopped")
	}
	viewer := memberToken(t, srv, tok, "v@acme.uz", "viewer")
	if res, _ := call(t, srv, viewer, "POST", base+"stop", ""); res.StatusCode != 403 {
		t.Fatalf("a Viewer stopped it: %d", res.StatusCode)
	}
}

func TestAPIPasswordAndMaintenance(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	base := "/api/services/acme/production/api/"
	if res, b := call(t, srv, tok, "PUT", base+"password", `{"user":"ops","password":"s3cret-pass"}`); res.StatusCode != 204 {
		t.Fatalf("password: %d %s", res.StatusCode, b)
	}
	var auth string
	testDB.QueryRow(`SELECT basic_auth FROM services WHERE name = 'api'`).Scan(&auth)
	if !strings.Contains(auth, `"user":"ops"`) || strings.Contains(auth, "s3cret-pass") {
		t.Fatalf("stored %q", auth)
	}
	if res, _ := call(t, srv, tok, "PUT", base+"password", `{"user":"","password":""}`); res.StatusCode != 204 {
		t.Fatal("remove")
	}
	testDB.QueryRow(`SELECT basic_auth FROM services WHERE name = 'api'`).Scan(&auth)
	if auth != "" {
		t.Fatalf("not removed: %q", auth)
	}
	if res, b := call(t, srv, tok, "PUT", base+"password", `{"user":"ops","password":"short"}`); res.StatusCode != 400 || !strings.Contains(string(b), "at least 8") {
		t.Fatalf("short: %d %s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, tok, "PUT", base+"maintenance", `{"on":true}`); res.StatusCode != 204 {
		t.Fatal("maintenance")
	}
	var m int
	testDB.QueryRow(`SELECT maintenance FROM services WHERE name = 'api'`).Scan(&m)
	viewer := memberToken(t, srv, tok, "v2@acme.uz", "viewer")
	if res, _ := call(t, srv, viewer, "PUT", base+"maintenance", `{"on":false}`); m != 1 || res.StatusCode != 403 {
		t.Fatalf("m %d viewer %d", m, res.StatusCode)
	}
}

func TestAPIRerouteNote(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	testDB.Exec(`UPDATE services SET current_deploy_id = 'dep_live' WHERE name = 'api'`)
	testDB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, container, created_at, updated_at) SELECT 'dep_live', id, 'c', 'live', 'c1', 1, 1 FROM services WHERE name = 'api'`)
	testDB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) SELECT 'dep_run', id, 'd', 'building', 2, 2 FROM services WHERE name = 'api'`)
	res, b := call(t, srv, tok, "PUT", "/api/services/acme/production/api/maintenance", `{"on":true}`)
	if res.StatusCode != 202 || !strings.Contains(string(b), `"note":"Saved. A deploy is running; it takes the change when it switches."`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestAPICancelAndNoCache(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	_, b := call(t, srv, tok, "POST", "/api/services/acme/production/api/deploys", `{"commit":"","no_cache":true}`)
	var out struct {
		DeployID string `json:"deploy_id"`
	}
	json.Unmarshal(b, &out)
	var noCache int
	testDB.QueryRow(`SELECT no_cache FROM deploys WHERE id = ?`, out.DeployID).Scan(&noCache)
	if noCache != 1 {
		t.Fatalf("no_cache %d (%s)", noCache, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/deploys/"+out.DeployID+"/cancel", ""); res.StatusCode != 202 {
		t.Fatalf("cancel: %d %s", res.StatusCode, b)
	}
	var state, cause string
	testDB.QueryRow(`SELECT state, cause FROM deploys WHERE id = ?`, out.DeployID).Scan(&state, &cause)
	if state != "superseded" || cause != "Canceled." {
		t.Fatalf("%s %q", state, cause)
	}
	var events int
	testDB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'deploy.canceled' AND subject_id = ?`, out.DeployID).Scan(&events)
	if events != 1 {
		t.Fatalf("%d deploy.canceled events", events)
	}
	testDB.Exec(`UPDATE deploys SET state = 'switching' WHERE id = ?`, out.DeployID)
	if res, b := call(t, srv, tok, "POST", "/api/deploys/"+out.DeployID+"/cancel", ""); res.StatusCode != 409 || !strings.Contains(string(b), "past its build") {
		t.Fatalf("late cancel: %d %s", res.StatusCode, b)
	}
}

func TestAPIStopWhileDeploying(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	testDB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) SELECT 'dep_run', id, 'd', 'building', 2, 2 FROM services WHERE name = 'api'`)
	res, b := call(t, srv, tok, "POST", "/api/services/acme/production/api/stop", "")
	if res.StatusCode != 202 || !strings.Contains(string(b), "A deploy is running") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}
