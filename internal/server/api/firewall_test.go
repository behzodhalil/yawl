package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

type fwAgent struct {
	mu   sync.Mutex
	cmds []proto.Command
}

func (f *fwAgent) Send(_ context.Context, server string, c proto.Command) (proto.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, c)
	if c.Name == proto.CmdFirewallStatus {
		b, _ := json.Marshal(proto.FirewallStatus{On: true, Available: true, Version: "v", Bans: []proto.FwBan{{IP: "192.0.2.9", Until: 1}},
			Ports: []proto.FwPort{{Proto: "tcp", Port: 22, Address: "0.0.0.0", Process: "sshd", Exposure: "everyone"}}})
		return proto.Result{OK: true, Data: b}, nil
	}
	return proto.Result{OK: true}, nil
}

func (f *fwAgent) last(name string) (proto.Command, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.cmds) - 1; i >= 0; i-- {
		if f.cmds[i].Name == name {
			return f.cmds[i], true
		}
	}
	return proto.Command{}, false
}

func setupFirewall(t *testing.T) (*httptest.Server, *app.App, *fwAgent, *http.Cookie) {
	t.Helper()
	_, _, a := setupApp(t)
	ag := &fwAgent{}
	v, err := vault.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(Deps{App: a, GW: ag, PublicURL: "http://panel.test", Vault: v}))
	t.Cleanup(srv.Close)
	clock(a)
	return srv, a, ag, claimed(t, srv, a)
}

func TestPutStartsTrial(t *testing.T) {
	srv, a, ag, c := setupFirewall(t)
	res, b := withCookie(t, srv, c, "PUT", "/api/servers/hel1/firewall", `{"on":true,"allow":[{"proto":"tcp","port":8080}],"block":[],"ssh_from":[]}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	cmd, ok := ag.last(proto.CmdFirewall)
	var args proto.FirewallArgs
	json.Unmarshal(cmd.Args, &args)
	if !ok || !args.Trial || !args.On || args.Allow[0].Port != 8080 || args.Version == "" {
		t.Fatalf("sent %+v", args)
	}
	_, b = withCookie(t, srv, c, "GET", "/api/servers/hel1/firewall", "", false)
	var view struct {
		Rules      app.FirewallRules     `json:"rules"`
		TrialUntil int64                 `json:"trial_until"`
		Status     *proto.FirewallStatus `json:"status"`
		Yawl       []map[string]any      `json:"yawl"`
	}
	json.Unmarshal([]byte(b), &view)
	if view.TrialUntil != a.Now().Add(2*60e9).UnixMilli() || view.Status == nil || len(view.Status.Bans) != 1 || len(view.Yawl) == 0 {
		t.Fatalf("view %s", b)
	}
	if view.Rules.On {
		t.Fatal("the trial shows as kept before Keep")
	}
	if res, b := withCookie(t, srv, c, "POST", "/api/servers/hel1/firewall/keep", "", true); res.StatusCode != 200 {
		t.Fatalf("keep %d %s", res.StatusCode, b)
	}
	keep, _ := ag.last(proto.CmdFirewallKeep)
	var k proto.FirewallKeepArgs
	json.Unmarshal(keep.Args, &k)
	if k.Version != args.Version {
		t.Fatalf("kept %q, sent %q", k.Version, args.Version)
	}
	if r, _ := a.FirewallRulesOf(t.Context(), "srv_1"); !r.On || r.Allow[0].Port != 8080 {
		t.Fatalf("kept rules %+v", r)
	}
}

func TestAddressCheck(t *testing.T) {
	srv, _, ag, c := setupFirewall(t)
	for _, body := range []string{
		`{"on":true,"allow":[],"block":[],"ssh_from":["198.51.100.0/24"]}`,
		`{"on":true,"allow":[],"block":["127.0.0.0/8"],"ssh_from":[]}`,
	} {
		res, b := withCookie(t, srv, c, "PUT", "/api/servers/hel1/firewall", body, true)
		if res.StatusCode != 409 || strings.TrimSpace(b) != "Your address 127.0.0.1 is not in the list." {
			t.Fatalf("%s: %d %s", body, res.StatusCode, b)
		}
	}
	if _, ok := ag.last(proto.CmdFirewall); ok {
		t.Fatal("sent despite the check")
	}
	res, _ := withCookie(t, srv, c, "PUT", "/api/servers/hel1/firewall", `{"on":true,"allow":[],"block":[],"ssh_from":["198.51.100.0/24"],"confirm_address":true}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("confirmed: %d", res.StatusCode)
	}
}

func TestFirewallOffNeedsNoKeep(t *testing.T) {
	srv, a, ag, c := setupFirewall(t)
	withCookie(t, srv, c, "PUT", "/api/servers/hel1/firewall", `{"on":true,"allow":[],"block":[],"ssh_from":[]}`, true)
	withCookie(t, srv, c, "POST", "/api/servers/hel1/firewall/keep", "", true)
	res, b := withCookie(t, srv, c, "PUT", "/api/servers/hel1/firewall", `{"on":false,"allow":[],"block":[],"ssh_from":[]}`, true)
	cmd, _ := ag.last(proto.CmdFirewall)
	var args proto.FirewallArgs
	json.Unmarshal(cmd.Args, &args)
	if res.StatusCode != 200 || args.On || args.Trial {
		t.Fatalf("%d %s %+v", res.StatusCode, b, args)
	}
	if r, _ := a.FirewallRulesOf(t.Context(), "srv_1"); r.On {
		t.Fatal("still on")
	}
}

func TestFirewallRoles(t *testing.T) {
	srv, a, _, _ := setupFirewall(t)
	adm, _ := a.St.Q.FirstAdmin(t.Context())
	link, _, _ := a.Invite(t.Context(), "developer", adm.ID)
	id, _ := a.AcceptInvite(t.Context(), link, "dev@acme.uz", "correct horse battery")
	s, _ := a.NewSession(t.Context(), id)
	dev := &http.Cookie{Name: cookieName, Value: s}
	if res, _ := withCookie(t, srv, dev, "GET", "/api/servers/hel1/firewall", "", false); res.StatusCode != 200 {
		t.Fatalf("read %d", res.StatusCode)
	}
	for _, p := range [][2]string{{"PUT", "/api/servers/hel1/firewall"}, {"POST", "/api/servers/hel1/firewall/keep"}, {"POST", "/api/servers/hel1/firewall/unban"}} {
		if res, _ := withCookie(t, srv, dev, p[0], p[1], `{}`, true); res.StatusCode != 403 {
			t.Fatalf("%s %s: %d", p[0], p[1], res.StatusCode)
		}
	}
}

func TestUnbanSends(t *testing.T) {
	srv, _, ag, c := setupFirewall(t)
	if res, b := withCookie(t, srv, c, "POST", "/api/servers/hel1/firewall/unban", `{"ip":"192.0.2.9"}`, true); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	cmd, ok := ag.last(proto.CmdFirewallUnban)
	if !ok || !strings.Contains(string(cmd.Args), "192.0.2.9") {
		t.Fatalf("%+v", cmd)
	}
}

func TestFirewallTelegramSetting(t *testing.T) {
	srv, a, _, c := setupFirewall(t)
	if res, b := withCookie(t, srv, c, "PUT", "/api/settings/firewall", `{"telegram":true}`, true); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if v, _ := a.St.Q.GetSetting(t.Context(), app.FirewallTelegramKey); v != "on" {
		t.Fatalf("setting %q", v)
	}
	if _, b := withCookie(t, srv, c, "GET", "/api/servers/hel1/firewall", "", false); !strings.Contains(b, `"telegram":true`) {
		t.Fatalf("view %s", b)
	}
}
