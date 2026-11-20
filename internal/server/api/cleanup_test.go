package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
)

type cleanupGW struct{ sent []proto.Command }

func (g *cleanupGW) Send(_ context.Context, _ string, c proto.Command) (proto.Result, error) {
	g.sent = append(g.sent, c)
	switch c.Name {
	case proto.CmdCleanup:
		b, _ := json.Marshal(proto.CleanupData{Containers: 3, Images: 2, Freed: 5 << 30})
		return proto.Result{OK: true, Data: b}, nil
	case proto.CmdUnusedVolumes:
		b, _ := json.Marshal(proto.UnusedVolumesData{Names: []string{"yawl-acme-production-db-data", "yawl-old-gone-data", "yawl_acme_production_api_uploads", "yawl-caddy-data"}})
		return proto.Result{OK: true, Data: b}, nil
	}
	return proto.Result{OK: true}, nil
}

func TestAPICleanup(t *testing.T) {
	_, tok, a := setupApp(t)
	gw := &cleanupGW{}
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", GW: gw}))
	defer srv.Close()
	call(t, srv, tok, "POST", "/api/apply", "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: db, kind: postgres}\n")
	testDB.Exec(`UPDATE services SET deleted_at = 1 WHERE name = 'db'`)
	if res, b := call(t, srv, tok, "POST", "/api/servers/hel1/cleanup", ""); res.StatusCode != 200 || !strings.Contains(string(b), `"freed":5368709120`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if _, b := call(t, srv, tok, "GET", "/api/servers/hel1/cleanup", ""); !strings.Contains(string(b), `"why":"by hand"`) {
		t.Fatalf("%s", b)
	}
	_, b := call(t, srv, tok, "GET", "/api/servers/hel1/volumes/unused", "")
	if strings.TrimSpace(string(b)) != `["yawl-old-gone-data","yawl_acme_production_api_uploads"]` {
		t.Fatalf("%s", b)
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/servers/hel1/volumes/yawl-acme-production-db-data", ""); res.StatusCode != 409 {
		t.Fatalf("an owned volume: %d", res.StatusCode)
	}
	if res, b := call(t, srv, tok, "DELETE", "/api/servers/hel1/volumes/yawl-old-gone-data", ""); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	testDB.Exec(`UPDATE services SET deleted_at = NULL WHERE name = 'db'`)
	if res, b := call(t, srv, tok, "POST", "/api/apply", "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: db, kind: postgres}\n  - {name: api, kind: worker, image: x, volumes: [{name: uploads, path: /u}]}\n"); res.StatusCode != 200 {
		t.Fatalf("apply %d %s", res.StatusCode, b)
	}
	testDB.Exec(`UPDATE services SET deleted_at = 1 WHERE name = 'db'`)
	testDB.Exec(`UPDATE services SET volumes_json = '[]', volumes_kept = '["uploads"]' WHERE name = 'api'`)
	_, b = call(t, srv, tok, "GET", "/api/servers/hel1/volumes/unused", "")
	if strings.Contains(string(b), "uploads") || strings.Contains(string(b), "caddy") {
		t.Fatalf("%s", b)
	}
	for _, name := range []string{"yawl_acme_production_api_uploads", "yawl-caddy-data", "yawl-not-reported"} {
		if res, _ := call(t, srv, tok, "DELETE", "/api/servers/hel1/volumes/"+name, ""); res.StatusCode != 409 {
			t.Errorf("%s: %d", name, res.StatusCode)
		}
	}
	dev := memberToken(t, srv, tok, "dev8@acme.uz", "developer")
	if res, _ := call(t, srv, dev, "POST", "/api/servers/hel1/cleanup", ""); res.StatusCode != 403 {
		t.Fatalf("a Developer: %d", res.StatusCode)
	}
}
