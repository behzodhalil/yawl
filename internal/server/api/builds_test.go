package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

func TestAPIBuildSettings(t *testing.T) {
	_, tok, a := setupApp(t)
	v, _ := vault.LoadOrCreate(t.TempDir())
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", Vault: v}))
	defer srv.Close()
	if _, b := call(t, srv, tok, "GET", "/api/settings/builds", ""); !strings.Contains(string(b), `"server":""`) {
		t.Fatalf("%s", b)
	}
	for body, want := range map[string]string{
		`{"server":"nope","repository":"ghcr.io/acme/builds"}`:   "no server nope",
		`{"server":"hel1","repository":"not a repository"}`:      "like ghcr.io/acme/builds",
		`{"server":"hel1","repository":"ghcr.io/acme/builds"}`:   "Add a login for ghcr.io",
		`{"server":"hel1","repository":"ghcr.io/acme/b:latest"}`: "without a tag",
	} {
		if res, b := call(t, srv, tok, "PUT", "/api/settings/builds", body); res.StatusCode != 400 || !strings.Contains(string(b), want) {
			t.Errorf("%s: %d %s", body, res.StatusCode, b)
		}
	}
	app.SaveRegistry(context.Background(), a.St.Q, v, "ghcr.io", "bot", "ghp_x")
	if res, b := call(t, srv, tok, "PUT", "/api/settings/builds", `{"server":"hel1","repository":"ghcr.io/acme/builds"}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if _, b := call(t, srv, tok, "GET", "/api/settings/builds", ""); !strings.Contains(string(b), `"server":"hel1"`) || !strings.Contains(string(b), `"repository":"ghcr.io/acme/builds"`) {
		t.Fatalf("%s", b)
	}
	if res, _ := call(t, srv, tok, "PUT", "/api/settings/builds", `{"server":"","repository":""}`); res.StatusCode != 204 {
		t.Fatalf("%d", res.StatusCode)
	}
	if _, b := call(t, srv, tok, "GET", "/api/settings/builds", ""); !strings.Contains(string(b), `"server":""`) {
		t.Fatalf("%s", b)
	}
}
