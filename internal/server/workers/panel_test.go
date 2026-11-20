package workers

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func panelSetup(t *testing.T) (*store.Store, *domainAnswers, *Panel, *time.Time) {
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.Q.UpsertServer(context.Background(), db.UpsertServerParams{ID: "srv_p", Name: "hel1", PublicIp: "5.6.7.8", CreatedAt: 1, UpdatedAt: 1})
	gw := &domainAnswers{answer: map[string]proto.EnsureDomainData{
		"panel.5-6-7-8.sslip.io": {DNS: "ok", Cert: "issued"},
		"panel.acme.uz":          {DNS: "wrong", DNSDetail: "panel.acme.uz has no A record yet. Set one: panel.acme.uz → 5.6.7.8.", Cert: "pending"},
	}}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	w := &Panel{St: st, GW: gw, Upstream: "127.0.0.1:7000", Now: func() time.Time { return now }}
	return st, gw, w, &now
}

func TestPanelHosts(t *testing.T) {
	st, gw, w, now := panelSetup(t)
	ctx := context.Background()
	w.Tick(ctx)
	if len(gw.sent) != 0 {
		t.Fatal("routed with no panel server")
	}
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.server_id", Value: "srv_p"})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.domain", Value: "panel.acme.uz"})
	w.Tick(ctx)
	if len(gw.sent) != 2 {
		t.Fatalf("sent %d", len(gw.sent))
	}
	var a proto.EnsureDomainArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	if a.Host != "panel.5-6-7-8.sslip.io" || a.ExpectIP != "5.6.7.8" || a.Upstream != "127.0.0.1:7000" {
		t.Fatalf("%+v", a)
	}
	if v, _ := st.Q.GetSetting(ctx, "panel.panel.acme.uz.dns_detail"); v != "panel.acme.uz has no A record yet. Set one: panel.acme.uz → 5.6.7.8." {
		t.Fatalf("detail %q", v)
	}
	*now = now.Add(time.Minute)
	w.Tick(ctx)
	if len(gw.sent) != 3 {
		t.Fatalf("sent %d", len(gw.sent))
	}
	json.Unmarshal(gw.sent[2].Args, &a)
	if a.Host != "panel.acme.uz" {
		t.Fatalf("resent %s", a.Host)
	}
	*now = now.Add(time.Hour)
	w.Tick(ctx)
	if len(gw.sent) != 5 {
		t.Fatalf("after an hour sent %d", len(gw.sent))
	}
}

func TestPanelRoutesWhileAdopting(t *testing.T) {
	st, gw, w, _ := panelSetup(t)
	ctx := context.Background()
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.server_id", Value: "srv_p"})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "adopt.pending", Value: "1"})
	w.Tick(ctx)
	if len(gw.sent) != 1 {
		t.Fatalf("sent %d while adopting", len(gw.sent))
	}
}

func TestPanelDropsFreeHostWhileDomainWorks(t *testing.T) {
	st, gw, w, now := panelSetup(t)
	ctx := context.Background()
	gw.answer["panel.acme.uz"] = proto.EnsureDomainData{DNS: "ok", Cert: "issued"}
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.server_id", Value: "srv_p"})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.domain", Value: "panel.acme.uz"})
	removes := func() []string {
		var out []string
		for _, c := range gw.sent {
			if c.Name == proto.CmdRemoveRoute {
				var a proto.RemoveRouteArgs
				json.Unmarshal(c.Args, &a)
				out = append(out, a.Host)
			}
		}
		return out
	}
	w.Tick(ctx)
	gw.fail = map[string]string{proto.CmdRemoveRoute: "agent away"}
	*now = now.Add(time.Minute)
	w.Tick(ctx)
	if r := removes(); len(r) != 1 || r[0] != "panel.5-6-7-8.sslip.io" {
		t.Fatalf("removes %v", r)
	}
	gw.fail = nil
	*now = now.Add(time.Minute)
	w.Tick(ctx)
	*now = now.Add(time.Minute)
	w.Tick(ctx)
	if r := removes(); len(r) != 2 {
		t.Fatalf("removes %v", r)
	}
	if _, hosts := app.PanelHosts(ctx, st.Q, ""); len(hosts) != 1 || hosts[0] != "panel.acme.uz" {
		t.Fatalf("hosts %v", hosts)
	}
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.panel.acme.uz.cert", Value: "failed"})
	n := len(gw.sent)
	*now = now.Add(time.Minute)
	w.Tick(ctx)
	var a proto.EnsureDomainArgs
	json.Unmarshal(gw.sent[n].Args, &a)
	if gw.sent[n].Name != proto.CmdEnsureDomain || a.Host != "panel.5-6-7-8.sslip.io" {
		t.Fatalf("not ensured again: %+v", gw.sent[n:])
	}
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.panel.acme.uz.cert", Value: "issued"})
	*now = now.Add(time.Minute)
	w.Tick(ctx)
	if r := removes(); len(r) != 3 {
		t.Fatalf("removes %v", r)
	}
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.domain", Value: ""})
	n = len(gw.sent)
	*now = now.Add(time.Minute)
	w.Tick(ctx)
	if len(gw.sent) != n+1 || gw.sent[n].Name != proto.CmdEnsureDomain {
		t.Fatalf("after clearing: %+v", gw.sent[n:])
	}
}

func TestPanelKeepsFreeHostAgentsDial(t *testing.T) {
	st, gw, w, now := panelSetup(t)
	ctx := context.Background()
	w.PublicURL = "https://panel.5-6-7-8.sslip.io"
	gw.answer["panel.acme.uz"] = proto.EnsureDomainData{DNS: "ok", Cert: "issued"}
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.server_id", Value: "srv_p"})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.domain", Value: "panel.acme.uz"})
	w.Tick(ctx)
	*now = now.Add(time.Minute)
	w.Tick(ctx)
	for _, c := range gw.sent {
		if c.Name == proto.CmdRemoveRoute {
			t.Fatal("took down the host agents dial")
		}
	}
	if _, hosts := app.PanelHosts(ctx, st.Q, w.PublicURL); len(hosts) != 2 {
		t.Fatalf("hosts %v", hosts)
	}
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "adopt.pending", Value: "1"})
	if _, hosts := app.PanelHosts(ctx, st.Q, ""); len(hosts) != 2 {
		t.Fatalf("adopting: hosts %v", hosts)
	}
}
