package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagedMode(t *testing.T) {
	_, tok, a := setupApp(t)
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "https://acme.cloud.test", Managed: true, Version: "0.1.0",
		InstallURL: "https://cloud.test/install.sh", Releases: "https://cloud.test/releases"}))
	defer srv.Close()
	if _, b := call(t, srv, tok, "GET", "/api/session", ""); !strings.Contains(string(b), `"managed":true`) {
		t.Fatalf("%s", b)
	}
	_, b := call(t, srv, tok, "POST", "/api/join-token", "")
	if !strings.Contains(string(b), `curl -fsSL https://cloud.test/install.sh | YAWL_VERSION=0.1.0 YAWL_RELEASES='https://cloud.test/releases' YAWL_JOIN='https://acme.cloud.test `) {
		t.Fatalf("%s", b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/update", ""); res.StatusCode != 409 || !strings.Contains(string(b), "The cloud updates this panel.") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	self := httptest.NewServer(New(Deps{App: a, PublicURL: "https://panel.test"}))
	defer self.Close()
	if _, b := call(t, self, tok, "POST", "/api/join-token", ""); !strings.Contains(string(b), "yawl agent join https://panel.test ") {
		t.Fatalf("self-hosted: %s", b)
	}
	if _, b := call(t, self, tok, "GET", "/api/session", ""); strings.Contains(string(b), `"managed"`) {
		t.Fatalf("%s", b)
	}
}

func TestManagedRefusesCloudsOwn(t *testing.T) {
	_, tok, a := setupApp(t)
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "https://acme.cloud.test", Managed: true, Version: "0.1.0"}))
	defer srv.Close()
	for _, c := range [][2]string{{"PUT", "/api/settings/hetzner"}, {"POST", "/api/hetzner/servers"}, {"GET", "/api/hetzner"},
		{"PUT", "/api/settings/panel"}} {
		if res, b := call(t, srv, tok, c[0], c[1], `{}`); res.StatusCode != 403 || !strings.Contains(string(b), "Yawl Cloud") {
			t.Errorf("%s %s: %d %s", c[0], c[1], res.StatusCode, b)
		}
	}
	if res, _ := call(t, srv, tok, "GET", "/api/settings/storage", ""); res.StatusCode != 200 {
		t.Fatalf("storage is the customer's: %d", res.StatusCode)
	}
}
