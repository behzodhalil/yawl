package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
)

type osGW struct{ sent []string }

func (g *osGW) Send(_ context.Context, _ string, c proto.Command) (proto.Result, error) {
	g.sent = append(g.sent, c.Name)
	if c.Name == proto.CmdOSStatus {
		b, _ := json.Marshal(proto.OSStatus{Updates: 12, Security: 5, Reboot: true, Auto: true, Installer: true})
		return proto.Result{OK: true, Data: b}, nil
	}
	return proto.Result{OK: true}, nil
}

func TestAPIOSUpdates(t *testing.T) {
	_, tok, a := setupApp(t)
	gw := &osGW{}
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", GW: gw}))
	defer srv.Close()
	if _, b := call(t, srv, tok, "GET", "/api/servers/hel1/os", ""); !strings.Contains(string(b), `"Updates":12`) || !strings.Contains(string(b), `"Reboot":true`) {
		t.Fatalf("%s", b)
	}
	if res, _ := call(t, srv, tok, "POST", "/api/servers/hel1/os/upgrade", ""); res.StatusCode != 204 || gw.sent[len(gw.sent)-1] != proto.CmdOSUpgrade {
		t.Fatalf("%d %v", res.StatusCode, gw.sent)
	}
	if res, _ := call(t, srv, tok, "GET", "/api/servers/nope/os", ""); res.StatusCode != 404 {
		t.Fatalf("%d", res.StatusCode)
	}
}
