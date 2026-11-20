package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistries(t *testing.T) {
	srv, tok, _ := setupApp(t)
	if res, b := call(t, srv, tok, "PUT", "/api/registries/ghcr.io", `{"username":"acme","token":"ghp_secret"}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	_, b := call(t, srv, tok, "GET", "/api/registries", "")
	if !strings.Contains(string(b), `{"host":"ghcr.io","username":"acme"}`) || strings.Contains(string(b), "ghp_secret") {
		t.Fatalf("%s", b)
	}
	var stored string
	testDB.QueryRow(`SELECT value FROM settings WHERE key = 'registry.ghcr.io'`).Scan(&stored)
	if stored == "" || strings.Contains(stored, "ghp_secret") {
		t.Fatalf("stored %q", stored)
	}
	dev := memberToken(t, srv, tok, "dev@acme.uz", "developer")
	if res, _ := call(t, srv, dev, "PUT", "/api/registries/ghcr.io", `{"username":"x","token":"y"}`); res.StatusCode != 403 {
		t.Fatalf("a Developer saved a login: %d", res.StatusCode)
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/registries/ghcr.io", ""); res.StatusCode != 204 {
		t.Fatalf("delete %d", res.StatusCode)
	}
	if _, b := call(t, srv, tok, "GET", "/api/registries", ""); strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("after delete %s", b)
	}
}

func TestRegistryHostNormalised(t *testing.T) {
	srv, tok, _ := setupApp(t)
	for _, c := range [][2]string{{"https:%2F%2FGHCR.io%2F", "ghcr.io"}, {"index.docker.io", "docker.io"}} {
		if res, b := call(t, srv, tok, "PUT", "/api/registries/"+c[0], `{"username":"acme","token":"t"}`); res.StatusCode != 204 {
			t.Fatalf("%s: %d %s", c[0], res.StatusCode, b)
		}
		if _, b := call(t, srv, tok, "GET", "/api/registries", ""); !strings.Contains(string(b), `"host":"`+c[1]+`"`) {
			t.Fatalf("%s: %s", c[0], b)
		}
	}
	if res, _ := call(t, srv, tok, "PUT", "/api/registries/ghcr.io%2Facme", `{"username":"acme","token":"t"}`); res.StatusCode != 400 {
		t.Fatalf("a host with a path: %d", res.StatusCode)
	}
}

func TestRegistryTestUnreachable(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	host := gone.Listener.Addr().String()
	gone.Close()
	srv, tok := setup(t)
	call(t, srv, tok, "PUT", "/api/registries/"+host, `{"username":"acme","token":"t"}`)
	if res, b := call(t, srv, tok, "POST", "/api/registries/"+host+"/test", ""); res.StatusCode != 502 || !strings.Contains(string(b), host) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}
