package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/hetzner"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

func TestAPIHetzner(t *testing.T) {
	var made map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hz-good" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/locations":
			w.Write([]byte(`{"locations":[{"name":"hel1","city":"Helsinki"}]}`))
		case "/server_types":
			w.Write([]byte(`{"server_types":[{"name":"cx22","cores":2,"memory":4,"disk":40,"prices":[]}]}`))
		case "/ssh_keys":
			w.Write([]byte(`{"ssh_keys":[]}`))
		case "/servers":
			json.NewDecoder(r.Body).Decode(&made)
			if made["name"] == "full" {
				w.WriteHeader(422)
				w.Write([]byte(`{"error":{"message":"resource limit exceeded"}}`))
				return
			}
			w.Write([]byte(`{"server":{"id":42,"public_net":{"ipv4":{"ip":"203.0.113.5"}}}}`))
		}
	}))
	defer fake.Close()
	hetzner.APIBase = fake.URL
	_, tok, a := setupApp(t)
	v, _ := vault.LoadOrCreate(t.TempDir())
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "https://panel.acme.uz", Vault: v, Version: "1.4.0"}))
	defer srv.Close()
	if _, b := call(t, srv, tok, "GET", "/api/hetzner", ""); !strings.Contains(string(b), `"connected":false`) {
		t.Fatalf("%s", b)
	}
	if res, _ := call(t, srv, tok, "PUT", "/api/settings/hetzner", `{"token":"bad"}`); res.StatusCode != 400 {
		t.Fatalf("a bad token: %d", res.StatusCode)
	}
	if res, b := call(t, srv, tok, "PUT", "/api/settings/hetzner", `{"token":"hz-good"}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if _, b := call(t, srv, tok, "GET", "/api/hetzner", ""); !strings.Contains(string(b), `"connected":true`) || !strings.Contains(string(b), `"cx22"`) || strings.Contains(string(b), "hz-good") {
		t.Fatalf("%s", b)
	}
	if res, _ := call(t, srv, tok, "POST", "/api/hetzner/servers", `{"name":"Bad Name","type":"cx22","location":"hel1"}`); res.StatusCode != 400 {
		t.Fatalf("a bad name: %d", res.StatusCode)
	}
	if res, _ := call(t, srv, tok, "POST", "/api/hetzner/servers", `{"name":"hel1","type":"cx22","location":"hel1"}`); res.StatusCode != 400 {
		t.Fatalf("a taken name: %d", res.StatusCode)
	}
	res, b := call(t, srv, tok, "POST", "/api/hetzner/servers", `{"name":"fsn2","type":"cx22","location":"hel1"}`)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"ip":"203.0.113.5"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	ud, _ := made["user_data"].(string)
	if !strings.HasPrefix(ud, "#!/bin/sh\n") || !strings.Contains(ud, "| YAWL_VERSION=1.4.0 YAWL_JOIN='https://panel.acme.uz ") || !strings.Contains(ud, "curl -fsSL "+InstallScript) {
		t.Fatalf("%q", ud)
	}
	if _, b := call(t, srv, tok, "GET", "/api/hetzner", ""); !strings.Contains(string(b), `"made":[{"name":"fsn2","ip":"203.0.113.5"`) {
		t.Fatalf("%s", b)
	}
	testDB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, last_heartbeat, created_at, updated_at) VALUES ('srv_f', 'fsn2', '203.0.113.5', 'PK', '', '', 1, 1, 1)`)
	if _, b := call(t, srv, tok, "GET", "/api/hetzner", ""); strings.Contains(string(b), `"fsn2"`) {
		t.Fatalf("a joined server still waits: %s", b)
	}
	if res, _ := call(t, srv, tok, "POST", "/api/hetzner/servers", `{"name":"full","type":"cx22","location":"hel1"}`); res.StatusCode != 400 {
		t.Fatalf("%d", res.StatusCode)
	}
	var live int
	testDB.QueryRow(`SELECT COUNT(*) FROM join_tokens WHERE used_at IS NULL`).Scan(&live)
	if live != 1 {
		t.Fatalf("%d live tokens", live)
	}
	var n int
	testDB.QueryRow(`SELECT COUNT(*) FROM join_tokens`).Scan(&n)
	if n == 0 {
		t.Fatal("no join token was made")
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/settings/hetzner", ""); res.StatusCode != 204 {
		t.Fatalf("forget: %d", res.StatusCode)
	}
	dev := httptest.NewServer(New(Deps{App: a, PublicURL: "https://panel.acme.uz", Vault: v, Version: "dev"}))
	defer dev.Close()
	call(t, srv, tok, "PUT", "/api/settings/hetzner", `{"token":"hz-good"}`)
	if res, b := call(t, dev, tok, "POST", "/api/hetzner/servers", `{"name":"fsn3","type":"cx22","location":"hel1"}`); res.StatusCode != 400 || !strings.Contains(string(b), "not a release") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}
