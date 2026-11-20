package workers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"

	"github.com/behzodhalil/yawl/internal/proto"
)

func TestRouteOptions(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET noindex = 1, www = 'bare', maintenance = 1, basic_auth = '{"user":"ops","hash":"JDJh"}' WHERE id = ?`, svc)
	o, err := RouteOptions(ctx, st.Q, svc, []string{"api-production.1-2-3-4.sslip.io", "acme.uz", "www.acme.uz", "www.solo.uz"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.NoIndex || o.Maintenance != "api" || o.BasicUser != "ops" || o.BasicHash != "JDJh" || len(o.Redirect) != 1 || o.Redirect["www.acme.uz"] != "acme.uz" {
		t.Fatalf("%+v", o)
	}
	st.DB.Exec(`UPDATE services SET www = 'www', noindex = 0, maintenance = 0, basic_auth = '' WHERE id = ?`, svc)
	o, _ = RouteOptions(ctx, st.Q, svc, []string{"acme.uz", "www.acme.uz", "shop.uz/api", "www.shop.uz/api"})
	if o.NoIndex || o.Maintenance != "" || o.BasicUser != "" || o.Redirect["acme.uz"] != "www.acme.uz" || len(o.Redirect) != 1 {
		t.Fatalf("%+v", o)
	}
}

func TestSwitchCarriesRouteOptions(t *testing.T) {
	st, svc := seed(t, "web")
	st.DB.Exec(`UPDATE services SET maintenance = 1 WHERE id = ?`, svc)
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	for _, c := range gw.sent {
		if c.Name == proto.CmdSwitchRoute {
			var a proto.SwitchArgs
			json.Unmarshal(c.Args, &a)
			if a.Options.Maintenance != "api" {
				t.Fatalf("%+v", a)
			}
			return
		}
	}
	t.Fatal("no switch")
}

func TestRerouteWaitsForDeploy(t *testing.T) {
	st, svc := seed(t, "web")
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_run', ?, 'c', 'building', 1, 1)`, svc)
	gw.sent = nil
	if err := Reroute(context.Background(), st.Q, gw, svc); err != ErrDeployRunning || len(gw.sent) != 0 {
		t.Fatalf("%v %v", err, gw.names())
	}
}

func TestDomainsRedirectOnlyToReachable(t *testing.T) {
	st, svc := seed(t, "web")
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	st.DB.Exec(`UPDATE services SET www = 'bare' WHERE id = ?`, svc)
	o, _ := RouteOptions(context.Background(), st.Q, svc, reachable([]domainRow{{"acme.uz", "wrong"}, {"www.acme.uz", "ok"}}, "www.acme.uz"))
	if len(o.Redirect) != 0 {
		t.Fatalf("redirect to an unreachable name: %v", o.Redirect)
	}
}

func TestResendWhenChangedDuringDeploy(t *testing.T) {
	st, svc := seed(t, "web")
	deployOnce(t, st, svc, &fakeSender{})
	gw2 := &fakeSender{failKey: func(key string) bool {
		if strings.HasSuffix(key, ":drain") {
			st.DB.Exec(`UPDATE services SET maintenance = 1 WHERE id = 'svc_1'`)
		}
		return false
	}}
	id, _ := app.New(st).Deploy(context.Background(), svc, "", "owner")
	if err := NewRunner(st, gw2, nil).Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	last := gw2.sent[len(gw2.sent)-1]
	var a proto.SwitchArgs
	json.Unmarshal(last.Args, &a)
	if last.Name != proto.CmdSwitchRoute || a.Options.Maintenance != "api" {
		t.Fatalf("last %s %+v", last.Name, a)
	}
}

func TestRerouteWaitsForMoveAndAdoption(t *testing.T) {
	st, svc := seed(t, "web")
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	gw.sent = nil
	st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "adopt.pending", Value: "1"})
	if err := Reroute(context.Background(), st.Q, gw, svc); err == nil || len(gw.sent) != 0 {
		t.Fatalf("adopting: %v %v", err, gw.names())
	}
	st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "adopt.pending", Value: "0"})
	if _, err := st.DB.Exec(`INSERT INTO env_moves (id, environment_id, from_server_id, to_server_id, step, state, boxes_json, lines_json, created_at, updated_at)
		VALUES ('mv_1', 'env_1', 'srv_1', 'srv_1', 'deploy', 'running', '{}', '[]', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := Reroute(context.Background(), st.Q, gw, svc); err != ErrMoving || len(gw.sent) != 0 {
		t.Fatalf("moving: %v %v", err, gw.names())
	}
}
