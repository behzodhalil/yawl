package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
)

type tsGW struct{ sent []proto.Command }

func (g *tsGW) Send(_ context.Context, _ string, c proto.Command) (proto.Result, error) {
	g.sent = append(g.sent, c)
	if c.Name == proto.CmdTailscaleStatus {
		b, _ := json.Marshal(proto.TailscaleStatus{Running: true, Address: "100.64.1.2"})
		return proto.Result{OK: true, Data: b}, nil
	}
	return proto.Result{OK: true}, nil
}

func TestAPITailscale(t *testing.T) {
	_, tok, a := setupApp(t)
	gw := &tsGW{}
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", GW: gw}))
	defer srv.Close()
	testDB.Exec(`UPDATE servers SET agent_pubkey = 'PK1' WHERE name = 'hel1'`)
	if _, b := call(t, srv, tok, "GET", "/api/servers/hel1/tailscale", ""); !strings.Contains(string(b), `"address":"100.64.1.2"`) || !strings.Contains(string(b), `"agent_pubkey":"PK1"`) {
		t.Fatalf("%s", b)
	}
	if res, _ := call(t, srv, tok, "PUT", "/api/servers/hel1/tailscale", `{"auth_key":"tskey-auth-plain"}`); res.StatusCode != 400 {
		t.Fatalf("a plain key: %d", res.StatusCode)
	}
	if res, b := call(t, srv, tok, "PUT", "/api/servers/hel1/tailscale", `{"auth_key_box":"c2VhbGVk"}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var up proto.TailscaleUpArgs
	json.Unmarshal(gw.sent[len(gw.sent)-1].Args, &up)
	if up.AuthKey != "c2VhbGVk" || up.Hostname != "hel1" {
		t.Fatalf("%+v", up)
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/servers/hel1/tailscale", ""); res.StatusCode != 204 || gw.sent[len(gw.sent)-1].Name != proto.CmdTailscaleDown {
		t.Fatalf("%d", res.StatusCode)
	}
	dev := memberToken(t, srv, tok, "dev5@acme.uz", "developer")
	if res, _ := call(t, srv, dev, "PUT", "/api/servers/hel1/tailscale", `{"auth_key_box":"c2VhbGVk"}`); res.StatusCode != 403 {
		t.Fatalf("a Developer: %d", res.StatusCode)
	}
}

func TestAPITailscaleNoGateway(t *testing.T) {
	_, tok, a := setupApp(t)
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test"}))
	defer srv.Close()
	for _, m := range []string{"PUT", "DELETE"} {
		if res, b := call(t, srv, tok, m, "/api/servers/hel1/tailscale", `{"auth_key_box":"c2VhbGVk"}`); res.StatusCode < 400 || len(b) == 0 {
			t.Fatalf("%s: %d %q", m, res.StatusCode, b)
		}
	}
}
