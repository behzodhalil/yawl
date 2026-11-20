package api

import (
	"strings"
	"testing"
)

const prodDB = "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: acme-db, kind: postgres}\n"
const stagingAPI = "project: acme\nenvironment: staging\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n"

func TestRefOtherEnvironment(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	call(t, srv, tok, "POST", "/api/apply", prodDB)
	call(t, srv, tok, "POST", "/api/apply", stagingAPI)
	path := "/api/services/acme/staging/api/env/DATABASE_URL"
	code, b := put(t, srv, tok, path, `{"ref":"production/acme-db.url"}`)
	if code != 200 || !strings.Contains(b, "api uses acme-db in production over the private network. acme-db shares its address with hel1 (no restart).") {
		t.Fatalf("%d %s", code, b)
	}
	for body, want := range map[string]string{
		`{"ref":"production/nope.url"}`: "DATABASE_URL refers to production/nope: production has no database nope.",
		`{"ref":"nowhere/acme-db.url"}`: "DATABASE_URL refers to nowhere/acme-db: nowhere has no database acme-db.",
		`{"ref":"a/b/c.url"}`:           "DATABASE_URL: a reference names a database in this project: <db>.url or <env>/<db>.url",
	} {
		if code, b := put(t, srv, tok, path, body); code != 400 || !strings.Contains(b, want) {
			t.Errorf("%s: %d %s", body, code, b)
		}
	}
}

func TestRefStaysInProject(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	call(t, srv, tok, "POST", "/api/apply", strings.Replace(prodDB, "project: acme", "project: other", 1))
	call(t, srv, tok, "POST", "/api/apply", strings.Replace(prodDB, "services:\n  - {name: acme-db, kind: postgres}\n", "services:\n  - {name: web, kind: worker, repo: \"https://x/y\"}\n", 1))
	call(t, srv, tok, "POST", "/api/apply", stagingAPI)
	if code, b := put(t, srv, tok, "/api/services/acme/staging/api/env/DATABASE_URL", `{"ref":"production/acme-db.url"}`); code != 400 ||
		!strings.Contains(b, "production has no database acme-db") {
		t.Fatalf("%d %s", code, b)
	}
}

func TestNewRefQueuesEnsure(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	call(t, srv, tok, "POST", "/api/apply", prodDB)
	testDB.Exec(`UPDATE servers SET wg_pubkey = 'W1', private_ip = '10.73.0.1' WHERE name = 'hel1'`)
	testDB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, wg_pubkey, private_ip, created_at, updated_at) VALUES ('srv_2','fsn1','5.6.7.8','P2','','','W2','10.73.0.2',1,1)`)
	call(t, srv, tok, "POST", "/api/apply", strings.Replace(stagingAPI, "server: hel1", "server: fsn1", 1))
	code, b := put(t, srv, tok, "/api/services/acme/staging/api/env/DATABASE_URL", `{"ref":"production/acme-db.url"}`)
	if code != 200 || !strings.Contains(b, "acme-db shares its address with fsn1 (it restarts once to listen on the private network).") {
		t.Fatalf("%d %s", code, b)
	}
	var port, queued int
	testDB.QueryRow(`SELECT private_port FROM services WHERE name = 'acme-db'`).Scan(&port)
	testDB.QueryRow(`SELECT count(*) FROM deploys d JOIN services s ON s.id = d.service_id WHERE s.name = 'acme-db' AND d.state = 'queued'`).Scan(&queued)
	if port != 15432 || queued != 1 {
		t.Fatalf("port %d, queued %d", port, queued)
	}
	if _, b := put(t, srv, tok, "/api/services/acme/staging/api/env/OTHER_URL", `{"ref":"production/acme-db.url"}`); strings.Contains(b, "restarts") {
		t.Fatalf("%s", b)
	}
}

func TestRefRefusesServerOffMesh(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	call(t, srv, tok, "POST", "/api/apply", prodDB)
	testDB.Exec(`UPDATE servers SET wg_pubkey = 'W1', private_ip = '10.73.0.1' WHERE name = 'hel1'`)
	testDB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, created_at, updated_at) VALUES ('srv_2','fsn1','5.6.7.8','P2','','',1,1)`)
	call(t, srv, tok, "POST", "/api/apply", strings.Replace(stagingAPI, "server: hel1", "server: fsn1", 1))
	code, b := put(t, srv, tok, "/api/services/acme/staging/api/env/DATABASE_URL", `{"ref":"production/acme-db.url"}`)
	if code != 400 || !strings.Contains(b, "fsn1 is not on the private network; rejoin it (yawl agent join) to use it.") {
		t.Fatalf("%d %s", code, b)
	}
}
