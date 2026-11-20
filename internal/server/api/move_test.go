package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPlanMoveAPI(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	testDB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, last_heartbeat, created_at, updated_at) VALUES ('srv_2', 'hk1', '5.6.7.8', 'PUBK', '', '', 1, 1, 1)`)
	res, b := call(t, srv, tok, "GET", "/api/environments/acme/production/move?to=hk1", "")
	var p struct {
		Lines    []string `json:"lines"`
		ToPubkey string   `json:"to_pubkey"`
	}
	json.Unmarshal(b, &p)
	if res.StatusCode != 200 || p.ToPubkey != "PUBK" || len(p.Lines) == 0 || p.Lines[0] != "move production from hel1 to hk1" {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	res, b = call(t, srv, tok, "GET", "/api/environments/acme/production/move?to=nope", "")
	if res.StatusCode != 409 || strings.TrimSpace(string(b)) != "There is no server named nope." {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestStartMoveAPI(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	testDB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, last_heartbeat, created_at, updated_at) VALUES ('srv_2', 'hk1', '5.6.7.8', 'PUBK', '', '', 1, 1, 1)`)
	if res, _ := call(t, srv, tok, "GET", "/api/environments/acme/production/moves/latest", ""); res.StatusCode != 404 {
		t.Fatal(res.StatusCode)
	}
	res, b := call(t, srv, tok, "POST", "/api/environments/acme/production/move", `{"to":"hk1","boxes":{}}`)
	var out struct{ Move string }
	json.Unmarshal(b, &out)
	if res.StatusCode != 200 || out.Move == "" {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	res, b = call(t, srv, tok, "GET", "/api/environments/acme/production/moves/latest", "")
	if res.StatusCode != 200 || !strings.Contains(string(b), `"to":"hk1"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b = call(t, srv, tok, "POST", "/api/moves/"+out.Move+"/retry", `{}`); res.StatusCode != 409 || strings.TrimSpace(string(b)) != "This move is not stopped." {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestEnvWritesWaitForMove(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	testDB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, last_heartbeat, created_at, updated_at) VALUES ('srv_2', 'hk1', '5.6.7.8', 'PUBK', '', '', 1, 1, 1)`)
	testDB.Exec(`INSERT INTO env_moves (id, environment_id, from_server_id, to_server_id, step, state, created_at, updated_at)
		SELECT 'mv_1', id, server_id, 'srv_2', 'backup', 'running', 1, 1 FROM environments WHERE name = 'production'`)
	want := "production is moving to hk1; change its variables after the move."
	for _, c := range []struct{ method, path, body string }{
		{"PUT", "/api/services/acme/production/api/env/SECRET", sealed()},
		{"PUT", "/api/environments/acme/production/env/SECRET", sealed()},
		{"DELETE", "/api/services/acme/production/api/env/SECRET", ""},
	} {
		res, b := call(t, srv, tok, c.method, c.path, c.body)
		if res.StatusCode != 409 || strings.TrimSpace(string(b)) != want {
			t.Fatalf("%s %s: %d %s", c.method, c.path, res.StatusCode, b)
		}
	}
}

func TestSourceWritesWaitForMove(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	testDB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, last_heartbeat, created_at, updated_at) VALUES ('srv_2', 'hk1', '5.6.7.8', 'PUBK', '', '', 1, 1, 1)`)
	testDB.Exec(`INSERT INTO env_moves (id, environment_id, from_server_id, to_server_id, step, state, created_at, updated_at)
		SELECT 'mv_1', id, server_id, 'srv_2', 'backup', 'running', 1, 1 FROM environments WHERE name = 'production'`)
	dev := memberToken(t, srv, tok, "dev@acme.uz", "developer")
	P := "/api/environments/acme/production/sources/vault"
	want := "production is moving to hk1; change its variables after the move."
	for _, c := range []struct{ method, body string }{
		{"PUT", `{"url":"https://203.0.113.9","path":"secret/data/x","agent_box":"` + box + `","recovery_box":"` + box + `"}`},
		{"DELETE", ""},
	} {
		if res, b := call(t, srv, dev, c.method, P, c.body); res.StatusCode != 409 || strings.TrimSpace(string(b)) != want {
			t.Fatalf("%s %d %s", c.method, res.StatusCode, b)
		}
	}
	var n int
	testDB.QueryRow(`SELECT COUNT(*) FROM secret_sources`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d sources written", n)
	}
}
