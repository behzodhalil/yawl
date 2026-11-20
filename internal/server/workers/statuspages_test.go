package workers

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/statuspage"
	"github.com/behzodhalil/yawl/internal/server/store"
)

func statusSetup(t *testing.T) (*store.Store, *domainAnswers, *StatusPages, *time.Time) {
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, q := range []string{
		`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, created_at, updated_at) VALUES ('srv_1', 'hel1', '5.6.7.8', '', '', 0, 0)`,
		`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('p_1', 'acme', 0, 0)`,
		`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('e_1', 'p_1', 'production', 'srv_1', 0, 0)`,
		`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, current_deploy_id, health, created_at, updated_at)
		 VALUES ('s_api', 'e_1', 'secret-admin', 'web', '', '', 80, '/healthz', 0, 0, 'd_1', 'ok', 0, 0)`,
		`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('d_1', 's_api', 'abc', 'live', 0, 0)`,
	} {
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	gw := &domainAnswers{answer: map[string]proto.EnsureDomainData{
		"acme.status.5-6-7-8.sslip.io": {DNS: "ok", Cert: "issued"},
		"status.acme.uz":               {DNS: "wrong", DNSDetail: "status.acme.uz has no A record yet.", Cert: "pending"},
	}}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	p := statuspage.PageRec{ID: "sp_1", Slug: "acme", Title: "Acme status", ServerID: "srv_1", Domain: "status.acme.uz",
		Rows: []statuspage.RowRec{{ID: "r1", Name: "API", Subject: "service:s_api"}}}
	if err := statuspage.SavePage(context.Background(), st.DB, p, 0); err != nil {
		t.Fatal(err)
	}
	return st, gw, &StatusPages{St: st, GW: gw, Now: func() time.Time { return now }}, &now
}

func sentPage(t *testing.T, gw *domainAnswers) proto.StatusPageArgs {
	t.Helper()
	gw.mu.Lock()
	defer gw.mu.Unlock()
	for i := len(gw.fakeSender.sent) - 1; i >= 0; i-- {
		if c := gw.fakeSender.sent[i]; c.Name == proto.CmdStatusPage {
			var a proto.StatusPageArgs
			json.Unmarshal(c.Args, &a)
			return a
		}
	}
	t.Fatal("no page sent")
	return proto.StatusPageArgs{}
}

func ensured(gw *domainAnswers) []proto.Command {
	gw.mu.Lock()
	defer gw.mu.Unlock()
	var out []proto.Command
	for _, c := range gw.sent {
		if c.Name == proto.CmdEnsureDomain {
			out = append(out, c)
		}
	}
	return out
}

func TestStatusPagesSendsAndRoutes(t *testing.T) {
	st, gw, w, now := statusSetup(t)
	ctx := context.Background()
	if err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	a := sentPage(t, gw)
	if a.ID != "sp_1" || !strings.Contains(a.HTML, "Acme status") || !strings.Contains(a.HTML, "All services are operational") {
		t.Fatalf("%+v", a)
	}
	for _, leak := range []string{"secret-admin", "acme/production", "5.6.7.8", "/healthz", "hel1", "abc"} {
		if strings.Contains(a.HTML, leak) {
			t.Errorf("the page holds %q", leak)
		}
	}
	if len(ensured(gw)) != 2 {
		t.Fatalf("ensured %d hosts", len(ensured(gw)))
	}
	var e proto.EnsureDomainArgs
	for _, c := range ensured(gw) {
		json.Unmarshal(c.Args, &e)
		if e.ExpectIP != "5.6.7.8" || e.Options.Status != "sp_1" || e.Upstream != "" || e.Container != "" {
			t.Fatalf("%+v", e)
		}
	}
	rs, _ := statuspage.Routes(ctx, st.DB)
	if len(rs) != 2 || rs[0].Host != "acme.status.5-6-7-8.sslip.io" || rs[0].Cert != "issued" || rs[1].DNSDetail != "status.acme.uz has no A record yet." {
		t.Fatalf("%+v", rs)
	}
	*now = now.Add(time.Minute)
	w.Tick(ctx)
	if len(ensured(gw)) != 3 {
		t.Fatalf("ensured %d", len(ensured(gw)))
	}
}

func TestStatusPagesDrafts(t *testing.T) {
	st, gw, w, now := statusSetup(t)
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET health = 'down' WHERE id = 's_api'`)
	w.Tick(ctx)
	w.Tick(ctx)
	incs, _ := statuspage.Incidents(ctx, st.DB, "sp_1", 0)
	if len(incs) != 1 || incs[0].State != "draft" || !incs[0].Auto || incs[0].Title != "API is down" || incs[0].Updates[0].Body != "We are looking into it." {
		t.Fatalf("one draft, once: %+v", incs)
	}
	if a := sentPage(t, gw); !strings.Contains(a.HTML, "All services are down") || strings.Contains(a.HTML, "API is down") {
		t.Fatal("the state shows at once; the draft does not")
	}
	statuspage.SetState(ctx, st.DB, incs[0].ID, "dismissed", 1)
	w.Tick(ctx)
	if incs, _ := statuspage.Incidents(ctx, st.DB, "sp_1", 0); len(incs) != 1 {
		t.Fatalf("%+v", incs)
	}
	st.DB.Exec(`UPDATE services SET health = 'ok' WHERE id = 's_api'`)
	*now = now.Add(time.Minute)
	w.Tick(ctx)
	if incs, _ := statuspage.Incidents(ctx, st.DB, "sp_1", 0); len(incs) != 0 {
		t.Fatalf("up again, the draft goes: %+v", incs)
	}
	st.DB.Exec(`UPDATE services SET health = 'down' WHERE id = 's_api'`)
	w.Tick(ctx)
	incs, _ = statuspage.Incidents(ctx, st.DB, "sp_1", 0)
	statuspage.SetState(ctx, st.DB, incs[0].ID, "published", 2)
	st.DB.Exec(`UPDATE services SET health = 'ok' WHERE id = 's_api'`)
	w.Tick(ctx)
	if incs, _ := statuspage.Incidents(ctx, st.DB, "sp_1", 0); len(incs) != 1 {
		t.Fatalf("%+v", incs)
	}
	if a := sentPage(t, gw); !strings.Contains(a.HTML, "API is down") || !strings.Contains(a.HTML, "Some services are degraded") {
		t.Fatal("a published incident shows, and its row is degraded")
	}
}

func TestStatusPagesTakesDownWhatIsGone(t *testing.T) {
	st, gw, w, now := statusSetup(t)
	ctx := context.Background()
	w.Tick(ctx)
	p, _ := statuspage.Pages(ctx, st.DB)
	p[0].Domain = ""
	statuspage.SavePage(ctx, st.DB, p[0], 1)
	*now = now.Add(time.Minute)
	gw.fakeSender.fail = map[string]string{proto.CmdRemoveRoute: "agent away"}
	w.Tick(ctx)
	if rs, _ := statuspage.Routes(ctx, st.DB); len(rs) != 2 {
		t.Fatal("a route the agent did not take down was forgotten")
	}
	gw.fakeSender.fail = nil
	w.Tick(ctx)
	if rs, _ := statuspage.Routes(ctx, st.DB); len(rs) != 1 {
		t.Fatalf("%+v", rs)
	}
	for _, c := range gw.fakeSender.sent {
		var a proto.StatusPageArgs
		if c.Name == proto.CmdStatusPage && json.Unmarshal(c.Args, &a) == nil && a.HTML == "" {
			t.Fatal("the file went while the free host serves it")
		}
	}
	statuspage.DeletePage(ctx, st.DB, "sp_1")
	w.Tick(ctx)
	if rs, _ := statuspage.Routes(ctx, st.DB); len(rs) != 0 {
		t.Fatalf("%+v", rs)
	}
	last := gw.fakeSender.sent[len(gw.fakeSender.sent)-1]
	var a proto.StatusPageArgs
	json.Unmarshal(last.Args, &a)
	if last.Name != proto.CmdStatusPage || a.ID != "sp_1" || a.HTML != "" {
		t.Fatalf("the file stayed: %+v", last)
	}
}

func TestStatusPagesReadsTheWatcher(t *testing.T) {
	r := watchSetup(t)
	w := &StatusPages{St: r.st, V: r.v}
	if out := w.outside(context.Background()); len(out) != 1 || out[0].Subject != "service:svc_1" || out[0].ClosedAt != nil {
		t.Fatalf("%+v", out)
	}
	r.st.DB.Exec(`DELETE FROM settings WHERE key = 'watch.url'`)
	if out := w.outside(context.Background()); out != nil {
		t.Fatalf("off: %+v", out)
	}
}

func TestWatcherSendsStatusPage(t *testing.T) {
	r := watchSetup(t)
	ctx := context.Background()
	statuspage.SavePage(ctx, r.st.DB, statuspage.PageRec{ID: "sp_1", Slug: "yawl", Title: "Yawl", ServerID: "srv_1", Watcher: true,
		Rows: []statuspage.RowRec{{ID: "r1", Name: "Website", Subject: "service:svc_1"}, {ID: "r2", Name: "Dashboard", Subject: "panel"}}}, 0)
	r.tick(t, r.watcher())
	cfg := r.wk.Expects[0]["config"].(map[string]any)
	page, _ := cfg["status"].(string)
	if !strings.Contains(page, `"title":"Yawl"`) || !strings.Contains(page, `{"id":"r1","name":"Website","subjects":["service:svc_1"]}`) || strings.Contains(page, "acme") {
		t.Fatalf("%q", page)
	}
}
