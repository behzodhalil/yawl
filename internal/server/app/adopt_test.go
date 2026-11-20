package app

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func adoptSeed(t *testing.T) (*App, string) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	st.Q.CreateDeploy(ctx, db.CreateDeployParams{ID: "dep_79", ServiceID: svc, CommitSha: "abc", CreatedAt: 1, UpdatedAt: 1})
	st.Q.SetDeployState(ctx, db.SetDeployStateParams{State: "live", ID: "dep_79"})
	st.Q.SetCurrentDeploy(ctx, db.SetCurrentDeployParams{CurrentDeployID: nullStr("dep_79"), ID: svc})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "adopt.pending", Value: "1"})
	return a, svc
}

var reports = map[string]proto.ReportData{"srv_1": {Running: []proto.Running{
	{Project: "acme", Env: "production", Service: "api", Deploy: "dep_81", Image: "yawl/acme-production-api:dep_81", Container: "yawl-acme-production-api-dep_81"},
	{Project: "shop", Env: "production", Service: "api", Deploy: "dep_5", Image: "yawl/shop-production-api:dep_5", Container: "yawl-shop-production-api-dep_5"},
}}}

func TestAdoptView(t *testing.T) {
	a, _ := adoptSeed(t)
	lines, err := a.AdoptView(context.Background(), reports, []string{"hk1"})
	want := []string{
		"acme-api runs deploy dep_81, the server last knew dep_79.",
		"yawl-shop-production-api-dep_5 runs shop-production-api, which this backup does not know. It is left alone.",
		"hk1 has not connected yet.",
	}
	if err != nil || !reflect.DeepEqual(lines, want) {
		t.Fatalf("%v\n%q", err, lines)
	}
}

func TestAdoptAccept(t *testing.T) {
	a, svc := adoptSeed(t)
	ctx := context.Background()
	if err := a.Adopt(ctx, reports, "owner"); err != nil {
		t.Fatal(err)
	}
	d, err := a.St.Q.GetDeploy(ctx, "dep_81")
	if err != nil || d.State != "live" || d.Image != "yawl/acme-production-api:dep_81" || d.Container != "yawl-acme-production-api-dep_81" || d.CommitSha != "" || d.ServiceID != svc {
		t.Fatalf("%+v %v", d, err)
	}
	s, _ := a.St.Q.GetService(ctx, svc)
	if s.CurrentDeployID.String != "dep_81" || a.Adopting(ctx) {
		t.Fatalf("current %s adopting %v", s.CurrentDeployID.String, a.Adopting(ctx))
	}
	var queued, events int
	var body string
	a.St.DB.QueryRow(`SELECT COUNT(*) FROM deploys WHERE state = 'queued'`).Scan(&queued)
	a.St.DB.QueryRow(`SELECT COUNT(*), MAX(body_json) FROM events WHERE kind = 'panel.adopted'`).Scan(&events, &body)
	if queued != 0 || events != 1 || !strings.Contains(body, "dep_81") {
		t.Fatalf("queued %d events %d %s", queued, events, body)
	}
	var shop int
	a.St.DB.QueryRow(`SELECT COUNT(*) FROM projects WHERE name = 'shop'`).Scan(&shop)
	if shop != 0 {
		t.Fatal("an unknown service was made up")
	}
}

func TestAdoptSupersedesStaleDeploys(t *testing.T) {
	a, svc := adoptSeed(t)
	ctx := context.Background()
	a.St.Q.CreateDeploy(ctx, db.CreateDeployParams{ID: "dep_80", ServiceID: svc, CreatedAt: 5, UpdatedAt: 5})
	a.St.Q.SetDeployState(ctx, db.SetDeployStateParams{State: "building", ID: "dep_80"})
	a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "adopt.since", Value: "100"})
	a.St.Q.CreateDeploy(ctx, db.CreateDeployParams{ID: "dep_90", ServiceID: svc, CreatedAt: 200, UpdatedAt: 200})
	a.St.Q.CreateDeploy(ctx, db.CreateDeployParams{ID: "dep_82", ServiceID: svc, CreatedAt: 6, UpdatedAt: 6})
	a.St.Q.SetDeployState(ctx, db.SetDeployStateParams{State: "pulling", ID: "dep_82"})
	if err := a.Adopt(ctx, reports, "owner"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"dep_80", "dep_82"} {
		if d, _ := a.St.Q.GetDeploy(ctx, id); d.State != "superseded" {
			t.Fatalf("stale deploy %s %s", id, d.State)
		}
	}
	if d, _ := a.St.Q.GetDeploy(ctx, "dep_90"); d.State != "queued" {
		t.Fatalf("new deploy %s", d.State)
	}
}
