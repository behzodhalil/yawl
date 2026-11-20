package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
)

type drainGW struct{ sent []proto.Command }

func (g *drainGW) Send(_ context.Context, _ string, c proto.Command) (proto.Result, error) {
	g.sent = append(g.sent, c)
	if c.Name == proto.CmdLogs {
		b, _ := json.Marshal(proto.LogsData{Drain: true})
		return proto.Result{OK: true, Data: b}, nil
	}
	return proto.Result{OK: true}, nil
}

func TestAPILogDrain(t *testing.T) {
	_, tok, a := setupApp(t)
	gw := &drainGW{}
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", GW: gw}))
	defer srv.Close()
	testDB.Exec(`UPDATE servers SET agent_pubkey = 'PK1' WHERE name = 'hel1'`)
	if _, b := call(t, srv, tok, "GET", "/api/servers/hel1/drain", ""); !strings.Contains(string(b), `"set":false`) || !strings.Contains(string(b), `"agent_pubkey":"PK1"`) {
		t.Fatalf("%s", b)
	}
	if res, _ := call(t, srv, tok, "PUT", "/api/servers/hel1/drain", `{"url":"https://logs.example.com","user":"1234","token":"plain"}`); res.StatusCode != 400 {
		t.Fatalf("a plain token: %d", res.StatusCode)
	}
	if res, b := call(t, srv, tok, "PUT", "/api/servers/hel1/drain", `{"url":"https://logs.example.com","user":"1234","token_box":"c2VhbGVk"}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var args proto.LogDrainArgs
	json.Unmarshal(gw.sent[len(gw.sent)-1].Args, &args)
	if gw.sent[len(gw.sent)-1].Name != proto.CmdLogDrain || args.URL != "https://logs.example.com" || args.TokenBox != "c2VhbGVk" || args.Server != "hel1" {
		t.Fatalf("%+v", args)
	}
	if _, b := call(t, srv, tok, "GET", "/api/servers/hel1/drain", ""); !strings.Contains(string(b), `"set":true`) || !strings.Contains(string(b), `"url":"https://logs.example.com"`) || strings.Contains(string(b), "c2VhbGVk") {
		t.Fatalf("%s", b)
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/servers/hel1/drain", ""); res.StatusCode != 204 {
		t.Fatalf("%d", res.StatusCode)
	}
	json.Unmarshal(gw.sent[len(gw.sent)-1].Args, &args)
	if !args.Off {
		t.Fatalf("%+v", args)
	}
}
