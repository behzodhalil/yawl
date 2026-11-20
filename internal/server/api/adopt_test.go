package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type reportSender struct {
	down   map[string]bool
	deploy *string
}

func (s reportSender) Send(_ context.Context, id string, c proto.Command) (proto.Result, error) {
	if c.Name != proto.CmdReport {
		return proto.Result{}, errors.New("unexpected " + c.Name)
	}
	if s.down[id] {
		return proto.Result{}, errors.New("not connected")
	}
	dep := "dep_81"
	if s.deploy != nil {
		dep = *s.deploy
	}
	b, _ := json.Marshal(proto.ReportData{Running: []proto.Running{{Project: "acme", Env: "production", Service: "api", Deploy: dep, Image: "img", Container: "c81"}}})
	return proto.Result{OK: true, Data: b}, nil
}

func TestAdoptAPI(t *testing.T) {
	_, _, a := setupApp(t)
	ctx := context.Background()
	mustApply(t, a, yaml)
	a.St.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_2", Name: "hk1", PublicIp: "5.6.7.8", CreatedAt: 1, UpdatedAt: 1})
	a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "adopt.pending", Value: "1"})
	gw := reportSender{down: map[string]bool{"srv_2": true}}
	srv := httptest.NewServer(New(Deps{App: a, GW: gw}))
	defer srv.Close()
	tok, _ := a.CreateToken(ctx, "x")
	_, b := call(t, srv, tok, "GET", "/api/adopt", "")
	var v struct {
		Adopting bool
		Lines    []string
		Seen     string
	}
	json.Unmarshal(b, &v)
	if !v.Adopting || len(v.Lines) != 2 || v.Lines[0] != "acme-api runs deploy dep_81, the server last knew nothing." || v.Lines[1] != "hk1 has not connected yet." {
		t.Fatalf("%s", b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/adopt", ""); res.StatusCode != 409 || !strings.Contains(string(b), "Some servers have not reported yet: hk1.") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if _, b := call(t, srv, "", "GET", "/api/session", ""); !strings.Contains(string(b), `"adopting":true`) {
		t.Fatalf("session %s", b)
	}
	gw.down["srv_2"] = false
	if res, b := call(t, srv, tok, "POST", "/api/adopt", `{"seen":"`+v.Seen+`"}`); res.StatusCode != 409 {
		t.Fatalf("accepted lines the owner did not see: %d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/adopt", `{"seen":"`+seen(t, srv, tok)+`"}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if a.Adopting(ctx) {
		t.Fatal("still adopting")
	}
}

func TestAdoptWithoutMissing(t *testing.T) {
	_, _, a := setupApp(t)
	ctx := context.Background()
	mustApply(t, a, yaml)
	a.St.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_2", Name: "hk1", PublicIp: "5.6.7.8", CreatedAt: 1, UpdatedAt: 1})
	a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "adopt.pending", Value: "1"})
	srv := httptest.NewServer(New(Deps{App: a, GW: reportSender{down: map[string]bool{"srv_2": true}}}))
	defer srv.Close()
	tok, _ := a.CreateToken(ctx, "x")
	if res, _ := call(t, srv, tok, "POST", "/api/adopt", `{"without":["hel1"]}`); res.StatusCode != 409 {
		t.Fatal("accepted without naming the missing server")
	}
	if res, b := call(t, srv, tok, "POST", "/api/adopt", `{"without":["hk1"],"seen":"`+seen(t, srv, tok)+`"}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if a.Adopting(ctx) {
		t.Fatal("still adopting")
	}
}

func seen(t *testing.T, srv *httptest.Server, tok string) string {
	t.Helper()
	_, b := call(t, srv, tok, "GET", "/api/adopt", "")
	var v struct{ Seen string }
	json.Unmarshal(b, &v)
	if len(v.Seen) != 64 {
		t.Fatalf("no seen in %s", b)
	}
	return v.Seen
}

func TestAdoptRefusesUnseen(t *testing.T) {
	_, _, a := setupApp(t)
	ctx := context.Background()
	mustApply(t, a, yaml)
	a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "adopt.pending", Value: "1"})
	dep := "dep_81"
	srv := httptest.NewServer(New(Deps{App: a, GW: reportSender{deploy: &dep}}))
	defer srv.Close()
	tok, _ := a.CreateToken(ctx, "x")
	first := seen(t, srv, tok)
	dep = "dep_82"
	res, b := call(t, srv, tok, "POST", "/api/adopt", `{"seen":"`+first+`"}`)
	if res.StatusCode != 409 || strings.TrimSpace(string(b)) != "What the servers run changed since you looked; review it again." {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var n int
	testDB.QueryRow(`SELECT COUNT(*) FROM deploys WHERE id IN ('dep_81', 'dep_82')`).Scan(&n)
	if n != 0 || !a.Adopting(ctx) {
		t.Fatalf("recorded %d deploys, adopting %v", n, a.Adopting(ctx))
	}
}
