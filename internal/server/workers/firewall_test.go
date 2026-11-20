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

type fwSender struct {
	sent    map[string][]proto.FirewallArgs
	version map[string]string
	bans    []proto.FwBan
}

func (f *fwSender) Send(_ context.Context, server string, c proto.Command) (proto.Result, error) {
	switch c.Name {
	case proto.CmdFirewall:
		var a proto.FirewallArgs
		json.Unmarshal(c.Args, &a)
		f.sent[server] = append(f.sent[server], a)
		f.version[server] = a.Version
	case proto.CmdFirewallStatus:
		b, _ := json.Marshal(proto.FirewallStatus{On: true, Available: true, Version: f.version[server], Bans: f.bans})
		return proto.Result{OK: true, Data: b}, nil
	}
	return proto.Result{OK: true}, nil
}

func fwWorld(t *testing.T) (*store.Store, *app.App, *Firewall, *fwSender, *time.Time) {
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.DB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, created_at, updated_at) VALUES
		('srv_1','hel1','1.1.1.1','A','','',1,1), ('srv_2','fsn1','2.2.2.2','A','','',1,1)`)
	now := time.UnixMilli(1_800_000_000_000)
	a := app.New(st)
	a.Now = func() time.Time { return now }
	gw := &fwSender{sent: map[string][]proto.FirewallArgs{}, version: map[string]string{}}
	return st, a, &Firewall{St: st, GW: gw, App: a, Now: func() time.Time { return now }}, gw, &now
}

func TestWorkerResendsAndAudits(t *testing.T) {
	st, a, w, gw, now := fwWorld(t)
	ctx := context.Background()
	if err := w.Tick(ctx); err != nil || len(gw.sent) != 0 {
		t.Fatalf("a server never set up was sent rules: %v %v", err, gw.sent)
	}
	a.SetFirewallRules(ctx, "srv_1", app.FirewallRules{On: true, Allow: []proto.FwRule{{Proto: "tcp", Port: 8080}}})
	w.Tick(ctx)
	if len(gw.sent["srv_1"]) != 1 || gw.sent["srv_1"][0].Trial || !gw.sent["srv_1"][0].On {
		t.Fatalf("kept rules not sent: %+v", gw.sent)
	}

	a.SetFirewallTrial(ctx, "srv_1", app.FirewallRules{On: true}, now.Add(time.Minute).UnixMilli(), "trial")
	w.Tick(ctx)
	if len(gw.sent["srv_1"]) != 1 {
		t.Fatal("a live trial was overwritten")
	}
	*now = now.Add(2 * time.Minute)
	w.Tick(ctx)
	if _, ok := a.FirewallTrialOf(ctx, "srv_1"); ok || len(gw.sent["srv_1"]) != 1 {
		t.Fatalf("an expired trial stayed, or rules the agent has were sent again: %d", len(gw.sent["srv_1"]))
	}
	gw.version["srv_1"] = "something else"
	w.Tick(ctx)
	if len(gw.sent["srv_1"]) != 2 {
		t.Fatal("an agent on another version was not sent the kept rules")
	}

	gw.bans = []proto.FwBan{{IP: "192.0.2.9", Until: now.Add(time.Hour).UnixMilli()}}
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: app.FirewallTelegramKey, Value: "on"})
	w.Tick(ctx)
	w.Tick(ctx)
	var audits, outbox int
	st.DB.QueryRow("SELECT COUNT(*) FROM events WHERE kind = 'audit' AND body_json LIKE '%banned 192.0.2.9 for SSH on hel1%'").Scan(&audits)
	st.DB.QueryRow("SELECT COUNT(*) FROM outbox WHERE body LIKE '%192.0.2.9%'").Scan(&outbox)
	if audits != 1 || outbox != 1 {
		t.Fatalf("ban recorded %d times, %d messages", audits, outbox)
	}
}
