package api

import (
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/spec"
)

func TestAPIVolumeBackupAndRestore(t *testing.T) {
	srv, tok := ready(t, true)
	call(t, srv, tok, "POST", "/api/apply", "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\", volumes: [{name: uploads, path: /u}]}\n")
	base := "/api/services/acme/production/api/"
	if res, b := call(t, srv, tok, "POST", base+"backups", `{"volume":"uploads"}`); res.StatusCode != 400 || !strings.Contains(string(b), "api has not been deployed yet") {
		t.Fatalf("never deployed: %d %s", res.StatusCode, b)
	}
	testDB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, image, state, created_at, updated_at) SELECT 'dep_live', id, 'abc', 'img:1', 'live', 1, 1 FROM services WHERE name = 'api'`)
	testDB.Exec(`UPDATE services SET current_deploy_id = 'dep_live' WHERE name = 'api'`)
	if res, b := call(t, srv, tok, "POST", base+"backups", `{"volume":"cache"}`); res.StatusCode != 400 || !strings.Contains(string(b), "api has no volume cache") {
		t.Fatalf("unknown volume: %d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", base+"backups", `{"volume":"uploads"}`); res.StatusCode != 200 || !strings.Contains(string(b), `"id":"bak_`) {
		t.Fatalf("backup: %d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "GET", base+"backups", ""); !strings.Contains(string(b), `"volume":"uploads"`) {
		t.Fatalf("list: %d %s", res.StatusCode, b)
	}
	testDB.Exec(`INSERT INTO backups (id, service_id, object_key, volume, state, created_at, updated_at) SELECT 'bak_v', id, 'k', 'uploads', 'ok', 1, 1 FROM services WHERE name = 'api'`)
	res, b := call(t, srv, tok, "POST", base+"restore", `{"backup":"bak_v","recovery_box":"`+box+`"}`)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"deploy":"dep_`) {
		t.Fatalf("restore: %d %s", res.StatusCode, b)
	}
}

func TestFormDetachesLastVolume(t *testing.T) {
	f := ServiceForm{Name: "api", Kind: "web", BackupAt: "03:30"}
	if s, _ := f.spec(); s.BackupAt != "" {
		t.Fatalf("%q", s.BackupAt)
	}
	f.Volumes = []spec.Volume{{Name: "uploads", Path: "/u"}}
	if s, _ := f.spec(); s.BackupAt != "03:30" {
		t.Fatalf("with a volume: %q", s.BackupAt)
	}
	db := ServiceForm{Name: "acme-db", Kind: "postgres", BackupAt: "02:00"}
	if s, _ := db.spec(); s.BackupAt != "02:00" {
		t.Fatalf("database: %q", s.BackupAt)
	}
}

func TestServerViewPrivateIP(t *testing.T) {
	srv, tok, _ := setupApp(t)
	testDB.Exec(`UPDATE servers SET private_ip = '10.73.0.1', wg_pubkey = '' WHERE name = 'hel1'`)
	_, b := call(t, srv, tok, "GET", "/api/servers", "")
	if !strings.Contains(string(b), `"private_ip":"10.73.0.1"`) || !strings.Contains(string(b), `"network":"rejoin to use the private network"`) {
		t.Fatalf("%s", b)
	}
	testDB.Exec(`UPDATE servers SET wg_pubkey = 'K' WHERE name = 'hel1'`)
	if _, b = call(t, srv, tok, "GET", "/api/servers", ""); strings.Contains(string(b), `"network"`) || strings.Contains(string(b), `"K"`) {
		t.Fatalf("%s", b)
	}
}

func TestServerViewPeers(t *testing.T) {
	srv, tok, _ := setupApp(t)
	testDB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, wg_pubkey, private_ip, created_at, updated_at) VALUES ('srv_2','fsn1','5.6.7.8','P2','','','W2','10.73.0.2',1,1)`)
	testDB.Exec(`INSERT INTO server_peers (server_id, peer_id, last_handshake, since, updated_at) VALUES ('srv_1','srv_2',1700000000000,1,1)`)
	_, b := call(t, srv, tok, "GET", "/api/servers", "")
	if !strings.Contains(string(b), `"peers":[{"name":"fsn1","last_handshake":1700000000000}]`) {
		t.Fatalf("%s", b)
	}
}

func TestServerViewNetworkCause(t *testing.T) {
	srv, tok, _ := setupApp(t)
	testDB.Exec(`UPDATE servers SET private_ip = '10.73.0.1', wg_pubkey = 'K' WHERE name = 'hel1'`)
	testDB.Exec(`INSERT INTO settings (key, value) SELECT 'network.cause.' || id, 'the private network needs Linux with WireGuard' FROM servers WHERE name = 'hel1'`)
	if _, b := call(t, srv, tok, "GET", "/api/servers", ""); !strings.Contains(string(b), `"network":"the private network is not up: the private network needs Linux with WireGuard"`) {
		t.Fatalf("%s", b)
	}
}
