package workers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

func addDomain(t *testing.T, st interface {
	InsertDomain(context.Context, db.InsertDomainParams) error
}, id, svc, host string) {
	if err := st.InsertDomain(context.Background(), db.InsertDomainParams{ID: id, ServiceID: svc, Host: host, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestSwitchAllHosts(t *testing.T) {
	st, svc := seed(t, "web")
	addDomain(t, st.Q, "d1", svc, "a.uz")
	addDomain(t, st.Q, "d2", svc, "b.uz")
	st.Q.SetDomainState(context.Background(), db.SetDomainStateParams{DnsState: "ok", CertState: "pending", ID: "d1"})
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	for _, c := range gw.sent {
		if c.Name == proto.CmdSwitchRoute {
			var a proto.SwitchArgs
			json.Unmarshal(c.Args, &a)
			if !slices.Equal(a.Hosts, []string{"api-production.1-2-3-4.sslip.io", "a.uz"}) {
				t.Fatalf("hosts %v", a.Hosts)
			}
			return
		}
	}
	t.Fatal("no switch sent")
}

type domainAnswers struct {
	fakeSender
	answer map[string]proto.EnsureDomainData
}

func (d *domainAnswers) Send(ctx context.Context, id string, c proto.Command) (proto.Result, error) {
	if c.Name != proto.CmdEnsureDomain {
		return d.fakeSender.Send(ctx, id, c)
	}
	d.mu.Lock()
	d.sent = append(d.sent, c)
	d.mu.Unlock()
	var a proto.EnsureDomainArgs
	json.Unmarshal(c.Args, &a)
	b, _ := json.Marshal(d.answer[a.Host])
	return proto.Result{Key: c.Key, OK: true, Data: b}, nil
}

func TestDomainsWorker(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	deployOnce(t, st, svc, &fakeSender{})
	addDomain(t, st.Q, "d1", svc, "a.uz")
	addDomain(t, st.Q, "d2", svc, "b.uz")
	st.Q.SetDomainState(ctx, db.SetDomainStateParams{DnsState: "ok", CertState: "issued", ID: "d2"})
	st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_2", EnvironmentID: "env_1", Name: "web", Kind: "web", Repo: "https://x/y", Branch: "main", Port: 10000, HealthPath: "/", CreatedAt: 1, UpdatedAt: 1})
	addDomain(t, st.Q, "d3", "svc_2", "c.uz")
	gw := &domainAnswers{answer: map[string]proto.EnsureDomainData{"a.uz": {DNS: "wrong", DNSDetail: "a.uz points to 9.9.9.9.", Cert: "pending"}}}
	w := &Domains{St: st, GW: gw}
	if err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gw.sent) != 1 {
		t.Fatalf("sent %v", gw.names())
	}
	var a proto.EnsureDomainArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	if a.Host != "a.uz" || a.ExpectIP != "1.2.3.4" || a.Container == "" || a.Port != 10000 {
		t.Fatalf("args %+v", a)
	}
	ds, _ := st.Q.ListDomains(ctx, svc)
	if ds[0].DnsState != "wrong" || ds[0].DnsDetail != "a.uz points to 9.9.9.9." {
		t.Fatalf("stored %+v", ds[0])
	}
}

func TestDomainRemoved(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	a := app.New(st)
	f := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\", domains: [%s]}\n"
	for _, d := range []string{"a.uz", ""} {
		file, err := specParse(fmtS(f, d))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Apply(ctx, file, "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	_ = svc
	gw := &fakeSender{}
	w := &Domains{St: st, GW: gw}
	w.Tick(ctx)
	w.Tick(ctx)
	if !slices.Equal(gw.names(), []string{proto.CmdRemoveRoute}) {
		t.Fatalf("sent %v", gw.names())
	}
	var r proto.RemoveRouteArgs
	json.Unmarshal(gw.sent[0].Args, &r)
	if r.Host != "a.uz" {
		t.Fatal(r.Host)
	}
}

func specParse(s string) (spec.File, error) { return spec.Parse([]byte(s)) }
func fmtS(f, d string) string               { return fmt.Sprintf(f, d) }

type switching struct {
	domainAnswers
	st    *store.Store
	svc   string
	to    string
	hosts map[string]string
}

func (s *switching) Send(ctx context.Context, id string, c proto.Command) (proto.Result, error) {
	if c.Name == proto.CmdEnsureDomain {
		var a proto.EnsureDomainArgs
		json.Unmarshal(c.Args, &a)
		s.hosts[a.Host] = a.Container
		s.st.Q.SetCurrentDeploy(ctx, db.SetCurrentDeployParams{CurrentDeployID: sql.NullString{String: s.to, Valid: true}, ID: s.svc})
	}
	return s.domainAnswers.Send(ctx, id, c)
}

func TestDomainsFollowSwitchMidTick(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	one := &fakeSender{}
	deployOnce(t, st, svc, one)
	second, _ := deployOnce(t, st, svc, one)
	d2, _ := st.Q.GetDeploy(ctx, second)
	first, _ := st.Q.RollbackTarget(ctx, db.RollbackTargetParams{ServiceID: svc, CurrentImage: d2.Image, BuiltAt: d2.CreatedAt})
	st.Q.SetCurrentDeploy(ctx, db.SetCurrentDeployParams{CurrentDeployID: sql.NullString{String: first.ID, Valid: true}, ID: svc})
	addDomain(t, st.Q, "d1", svc, "a.uz")
	addDomain(t, st.Q, "d2", svc, "b.uz")
	gw := &switching{st: st, svc: svc, to: second, hosts: map[string]string{}}
	gw.answer = map[string]proto.EnsureDomainData{}
	if err := (&Domains{St: st, GW: gw}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if first.Container == d2.Container {
		t.Fatal("the test needs two containers")
	}
	if gw.hosts["a.uz"] != first.Container || gw.hosts["b.uz"] != d2.Container {
		t.Fatalf("sent %v; first %s second %s", gw.hosts, first.Container, d2.Container)
	}
}

func TestDomainOkReroutesTwin(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	deployOnce(t, st, svc, &fakeSender{})
	st.DB.Exec(`UPDATE services SET www = 'bare' WHERE id = ?`, svc)
	addDomain(t, st.Q, "d1", svc, "acme.uz")
	addDomain(t, st.Q, "d2", svc, "www.acme.uz")
	st.Q.SetDomainState(ctx, db.SetDomainStateParams{DnsState: "ok", CertState: "issued", ID: "d2"})
	gw := &domainAnswers{answer: map[string]proto.EnsureDomainData{"acme.uz": {DNS: "ok", Cert: "issued"}}}
	if err := (&Domains{St: st, GW: gw}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range gw.sent {
		if c.Name == proto.CmdSwitchRoute {
			var a proto.SwitchArgs
			json.Unmarshal(c.Args, &a)
			if a.Options.Redirect["www.acme.uz"] != "acme.uz" {
				t.Fatalf("options %+v", a.Options)
			}
			return
		}
	}
	t.Fatalf("not routed again: %v", gw.names())
}

func TestDomainRemovalsKeepReaddedAndNew(t *testing.T) {
	st, _ := seed(t, "web")
	ctx := context.Background()
	a := app.New(st)
	f := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\", domains: [%s]}\n"
	for _, d := range []string{"a.uz", "", "a.uz"} {
		file, _ := specParse(fmtS(f, d))
		if _, err := a.Apply(ctx, file, "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: app.RemovalKey("srv_1"), Value: `["a.uz","old.uz"]`})
	gw := &queuing{fakeSender: &fakeSender{}, st: st}
	(&Domains{St: st, GW: gw}).removals(ctx)
	for _, c := range gw.sent {
		var r proto.RemoveRouteArgs
		if json.Unmarshal(c.Args, &r); c.Name == proto.CmdRemoveRoute && r.Host == "a.uz" {
			t.Fatal("removed the route of a host added back")
		}
	}
	if v, _ := st.Q.GetSetting(ctx, app.RemovalKey("srv_1")); v != `["new.uz"]` {
		t.Fatalf("queue %s", v)
	}
}

type queuing struct {
	*fakeSender
	st *store.Store
}

func (q *queuing) Send(ctx context.Context, id string, c proto.Command) (proto.Result, error) {
	if c.Name == proto.CmdRemoveRoute {
		app.QueueRemoval(ctx, q.st.Q, id, "new.uz")
	}
	return q.fakeSender.Send(ctx, id, c)
}
