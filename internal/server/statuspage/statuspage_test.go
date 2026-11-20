package statuspage

import (
	"context"
	stdhtml "html"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
)

var tash = time.FixedZone("+05", 5*3600)

var now = time.Date(2026, 11, 5, 14, 2, 0, 0, tash)

func ms(t time.Time) int64 { return t.UnixMilli() }

func TestJoin(t *testing.T) {
	got := Join([]Span{{50, 60}, {10, 20}, {15, 30}, {30, 40}, {70, 70}})
	want := []Span{{10, 40}, {50, 60}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("%v", got)
	}
}

func TestSpans(t *testing.T) {
	evs := []Event{{"service.recovered", "s", 20}, {"service.down", "s", 30}, {"service.down", "s", 35}, {"service.recovered", "s", 40}, {"service.down", "s", 90}}
	got := Spans(evs, "service.down", "service.recovered", 0, 100)
	want := []Span{{0, 20}, {30, 40}, {90, 100}}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
}

func TestRowState(t *testing.T) {
	up, down := Member{}, Member{DownNow: true}
	n := ms(now)
	open := Incident{Kind: KindIncident, Rows: []string{"r"}}
	maint := Incident{Kind: KindMaint, Rows: []string{"r"}, StartsAt: n - 1, EndsAt: n + 1}
	cases := []struct {
		name string
		r    Row
		inc  []Incident
		want State
	}{
		{"up", Row{ID: "r", Members: []Member{up}}, nil, Operational},
		{"down", Row{ID: "r", Members: []Member{down}}, nil, Down},
		{"project, some down", Row{ID: "r", Members: []Member{up, down}}, nil, Degraded},
		{"project, all down", Row{ID: "r", Members: []Member{down, down}}, nil, Down},
		{"an open incident makes an up row degraded", Row{ID: "r", Members: []Member{up}}, []Incident{open}, Degraded},
		{"never better than its checks", Row{ID: "r", Members: []Member{down}}, []Incident{open}, Down},
		{"a resolved one does nothing", Row{ID: "r", Members: []Member{up}}, []Incident{{Kind: KindIncident, Rows: []string{"r"}, ResolvedAt: 5}}, Operational},
		{"another row's does nothing", Row{ID: "x", Members: []Member{up}}, []Incident{open}, Operational},
		{"maintenance wins", Row{ID: "r", Members: []Member{down}}, []Incident{maint}, Maintenance},
		{"maintenance not yet", Row{ID: "r", Members: []Member{up}}, []Incident{{Kind: KindMaint, Rows: []string{"r"}, StartsAt: n + 1, EndsAt: n + 2}}, Operational},
	}
	for _, c := range cases {
		if got := RowState(c.r, c.inc, n); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestSummary(t *testing.T) {
	for _, c := range []struct {
		in   []State
		want string
	}{
		{nil, "off"},
		{[]State{Down, Down}, "All services are down"},
		{[]State{Down, Operational}, "Some services are down"},
		{[]State{Degraded, Maintenance}, "Some services are degraded"},
		{[]State{Maintenance, Operational}, "Maintenance is under way"},
		{[]State{Operational}, "All services are operational"},
	} {
		if got := Summary(c.in); got.Word != c.want && got.Kind != c.want {
			t.Errorf("%v: %v", c.in, got)
		}
	}
}

func TestBars(t *testing.T) {
	today := time.Date(2026, 11, 5, 0, 0, 0, 0, tash)
	yesterday := today.AddDate(0, 0, -1)
	m := Member{Since: ms(today.AddDate(0, 0, -10)), Down: []Span{{ms(yesterday.Add(time.Hour)), ms(yesterday.Add(time.Hour + 12*time.Minute))}}}
	bars, up := Bars(Row{Members: []Member{m}}, now)
	if len(bars) != Days {
		t.Fatalf("%d bars", len(bars))
	}
	if b := bars[Days-2]; b.Class != "bad" || b.Title != "4 Nov: 12 minutes down" {
		t.Fatalf("yesterday: %+v", b)
	}
	if b := bars[Days-1]; b.Class != "ok" || b.Title != "5 Nov: no downtime" {
		t.Fatalf("today: %+v", b)
	}
	if b := bars[Days-12]; b.Class != "off" {
		t.Fatalf("before it existed: %+v", b)
	}
	if up != "99.92%" {
		t.Fatalf("uptime %q", up)
	}
	if _, up := Bars(Row{Members: []Member{{Since: 0}}}, now); up != "100%" {
		t.Fatalf("no downtime: %q", up)
	}
	if _, up := Bars(Row{Members: []Member{{Since: 0, Down: []Span{{ms(now) - 1000, ms(now)}}}}}, now); up != "99.99%" {
		t.Fatalf("a second down is not 100%%: %q", up)
	}
}

func TestBarsProject(t *testing.T) {
	today := time.Date(2026, 11, 5, 0, 0, 0, 0, tash)
	a := Member{Down: []Span{{ms(today.Add(time.Hour)), ms(today.Add(time.Hour + 30*time.Minute))}}}
	b, c := Member{}, Member{}
	bars, _ := Bars(Row{Members: []Member{a, b, c}}, now)
	if bars[Days-1].Class != "bad" {
		t.Fatalf("%+v", bars[Days-1])
	}
	short := Member{Down: []Span{{ms(today.Add(time.Hour)), ms(today.Add(time.Hour + 2*time.Minute))}}}
	bars, _ = Bars(Row{Members: []Member{short, b, c}}, now)
	if got := bars[Days-1]; got.Class != "warn" || !strings.Contains(got.Title, "the longest 2 minutes") {
		t.Fatalf("%+v", got)
	}
}

func TestRenderShowsOnlyPublicWords(t *testing.T) {
	n := ms(now)
	p := Page{
		Title: "Acme <status>",
		Rows: []Row{
			{ID: "r1", Name: "Website", Members: []Member{{}}},
			{ID: "r2", Name: "API", Members: []Member{{DownNow: true, Down: []Span{{n - 20*60000, n}}}}},
		},
		Incidents: []Incident{
			{Kind: KindIncident, Title: "API errors", Rows: []string{"r2"}, CreatedAt: n - 20*60000, Updates: []Update{{At: n - 60000, Body: "Fixed <b>soon</b>"}, {At: n - 20*60000, Body: "Looking"}}},
			{Kind: KindIncident, Title: "Old outage", Rows: []string{"r1"}, CreatedAt: n - 3*day, ResolvedAt: n - 3*day + 7*minute},
			{Kind: KindIncident, Title: "Ancient", Rows: []string{"r1"}, CreatedAt: n - 30*day, ResolvedAt: n - 29*day},
			{Kind: KindMaint, Title: "Database upgrade", Rows: []string{"r1"}, StartsAt: n + day, EndsAt: n + day + 60*minute},
		},
		Now: now,
	}
	html := string(Render(p))
	text := stdhtml.UnescapeString(html)
	for _, want := range []string{"<title>Acme &lt;status&gt;</title>", "Fixed &lt;b&gt;soon&lt;/b&gt;", `class="badge bad"`, "default-src 'none'"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, want := range []string{
		"Some services are down", "Updated 5 Nov 14:02 +05", "Website", "API", "API errors", "Since 5 Nov 13:42 +05. Affects API",
		"Old outage", "7 minutes", "Scheduled maintenance", "Database upgrade", "6 Nov 14:02 +05 to 6 Nov 15:02 +05",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(html, "Ancient") {
		t.Error("an incident resolved 29 days ago is listed")
	}
	if strings.Contains(html, "<script") || strings.Contains(html, "http") && !strings.Contains(html, "http-equiv") {
		t.Error("the page fetches or links something")
	}
	if strings.Index(html, "Looking") < strings.Index(html, "Fixed") {
		t.Error("updates are not newest first")
	}
	if strings.ContainsAny(html, "\u2013\u2014") {
		t.Error("a dash")
	}
}

func TestStore(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.DB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, created_at, updated_at) VALUES ('srv_1', 'hel1', '1.2.3.4', '', '', 0, 0)`)
	a := PageRec{ID: "sp_a", Slug: "acme", Title: "Acme", ServerID: "srv_1", Watcher: true, Rows: []RowRec{{ID: "r1", Name: "Website", Subject: "service:s1"}, {ID: "r2", Name: "Dashboard", Subject: "panel"}}}
	b := PageRec{ID: "sp_b", Slug: "beta", Title: "Beta", ServerID: "srv_1", Watcher: true}
	for _, p := range []PageRec{a, b} {
		if err := SavePage(ctx, st.DB, p, 1); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Pages(ctx, st.DB)
	if err != nil || len(got) != 2 {
		t.Fatal(got, err)
	}
	if got[0].Watcher || !got[1].Watcher {
		t.Fatalf("only the last page saved with the watcher keeps it: %+v", got)
	}
	if len(got[0].Rows) != 2 || got[0].Rows[1].Name != "Dashboard" {
		t.Fatalf("%+v", got[0].Rows)
	}

	i := IncidentRec{ID: "si_1", PageID: "sp_a", Kind: KindIncident, Title: "Website is down", Rows: []string{"r1"}, State: "draft", Auto: true, CreatedAt: 5}
	if err := InsertIncident(ctx, st.DB, i, "We are looking into it.", "su_1", 5); err != nil {
		t.Fatal(err)
	}
	if err := DropAutoDrafts(ctx, st.DB, "sp_a", map[string]bool{"r1": false}); err != nil {
		t.Fatal(err)
	}
	if one, err := GetIncident(ctx, st.DB, "si_1"); err != nil || len(one.Updates) != 1 || one.Updates[0].Body != "We are looking into it." {
		t.Fatalf("a draft whose row is down stays: %+v %v", one, err)
	}
	DropAutoDrafts(ctx, st.DB, "sp_a", map[string]bool{"r1": true})
	if _, err := GetIncident(ctx, st.DB, "si_1"); err != ErrNotFound {
		t.Fatalf("a draft whose row is up again goes: %v", err)
	}

	i.ID, i.Auto = "si_2", false
	InsertIncident(ctx, st.DB, i, "", "", 6)
	if err := SetState(ctx, st.DB, "si_2", "published", 7); err != nil {
		t.Fatal(err)
	}
	if err := Resolve(ctx, st.DB, "si_2", 8); err != nil {
		t.Fatal(err)
	}
	if err := Resolve(ctx, st.DB, "si_2", 9); err != ErrNotFound {
		t.Fatalf("resolved twice: %v", err)
	}
	if list, _ := Incidents(ctx, st.DB, "sp_a", 9); len(list) != 0 {
		t.Fatalf("ended before since: %+v", list)
	}
	if err := DeletePage(ctx, st.DB, "sp_a"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetIncident(ctx, st.DB, "si_2"); err != ErrNotFound {
		t.Fatalf("a page's incidents go with it: %v", err)
	}

	SaveRoute(ctx, st.DB, Route{Host: "acme.status.1-2-3-4.sslip.io", ServerID: "srv_1", PageID: "sp_a", DNS: "ok"})
	if rs, _ := Routes(ctx, st.DB); len(rs) != 1 || rs[0].DNS != "ok" {
		t.Fatalf("%+v", rs)
	}

	st.DB.Exec(`INSERT INTO events (id, kind, subject_id, actor, body_json, created_at, updated_at) VALUES ('e1', 'service.down', 's1', 'system', '{}', 10, 10), ('e2', 'deploy.live', 's1', 'system', '{}', 11, 11), ('e3', 'service.recovered', 's1', 'system', '{}', 12, 12), ('e0', 'service.down', 's1', 'system', '{}', 1, 1)`)
	evs, err := HealthEvents(ctx, st.DB, 5)
	if err != nil || len(evs) != 2 || evs[0].At != 10 || evs[1].Kind != "service.recovered" {
		t.Fatalf("%+v %v", evs, err)
	}
}

func TestLoadAndBuild(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	n := ms(now)
	for _, q := range []string{
		`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, offline, created_at, updated_at) VALUES ('srv_1', 'hel1', '1.2.3.4', '', '', 0, 0, 0), ('srv_2', 'hel2', '1.2.3.5', '', '', 1, 0, 0)`,
		`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('p_site', 'site', 0, 0), ('p_cells', 'cells', 0, 0)`,
		`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('e_site', 'p_site', 'production', 'srv_1', 0, 0), ('e_a', 'p_cells', 'a', 'srv_1', 0, 0), ('e_b', 'p_cells', 'b', 'srv_2', 0, 0)`,
		`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, current_deploy_id, health, created_at, updated_at) VALUES
		 ('s_web', 'e_site', 'web', 'web', '', '', 80, '/', 0, 0, 'd_web', 'ok', 0, 0),
		 ('s_a', 'e_a', 'panel', 'web', '', '', 80, '/', 0, 0, 'd_a', 'down', 0, 0),
		 ('s_b', 'e_b', 'panel', 'web', '', '', 80, '/', 0, 0, 'd_b', 'ok', 0, 0),
		 ('s_job', 'e_site', 'job', 'cron', '', '', 0, '', 0, 0, NULL, 'ok', 0, 0)`,
		`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('d_web', 's_web', 'x', 'live', 0, 0), ('d_a', 's_a', 'x', 'live', 0, 0), ('d_b', 's_b', 'x', 'live', 0, 0)`,
	} {
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	ev := func(id, kind, subject string, at int64) {
		st.DB.Exec(`INSERT INTO events (id, kind, subject_id, actor, body_json, created_at, updated_at) VALUES (?, ?, ?, 'system', '{}', ?, ?)`, id, kind, subject, at, at)
	}
	ev("e1", "service.down", "s_web", n-2*day)
	ev("e2", "service.recovered", "s_web", n-2*day+10*minute)
	ev("e3", "service.down", "s_a", n-5*minute)
	closed := n - 2*day + 15*minute
	outside := []Outside{{Subject: "service:s_web", OpenedAt: n - 2*day + 5*minute, ClosedAt: &closed}, {Subject: "panel", OpenedAt: n - minute}}
	w, err := Load(ctx, st.DB, outside, now)
	if err != nil {
		t.Fatal(err)
	}
	web := w.Members("service:s_web")
	if len(web) != 1 || web[0].DownNow || len(web[0].Down) != 1 || web[0].Down[0] != (Span{n - 2*day, n - 2*day + 15*minute}) {
		t.Fatalf("web: %+v", web)
	}
	cells := w.Members("project:p_cells")
	if len(cells) != 2 || !cells[0].DownNow || !cells[1].DownNow {
		t.Fatalf("cells: %+v", cells)
	}
	if p := w.Members("panel"); !p[0].DownNow {
		t.Fatalf("panel: %+v", p)
	}
	if w.Members("service:s_job") != nil || w.Members("service:gone") != nil {
		t.Fatal("a cron job or a deleted service covers something")
	}
	page := PageRec{ID: "sp_1", Title: "Acme", Rows: []RowRec{{ID: "r1", Name: "Website", Subject: "service:s_web"}, {ID: "r2", Name: "Gone", Subject: "service:gone"}, {ID: "r3", Name: "Cloud panels", Subject: "project:p_cells"}}}
	pg := w.Page(page, []IncidentRec{{Kind: KindIncident, Title: "draft", State: "draft"}, {Kind: KindIncident, Title: "pub", State: "published"}}, now)
	if len(pg.Rows) != 2 || pg.Rows[1].Name != "Cloud panels" || len(pg.Incidents) != 1 || pg.Incidents[0].Title != "pub" {
		t.Fatalf("%+v", pg)
	}
	if h := FreeHost("acme", netip.MustParseAddr("1.2.3.4")); h != "acme.status.1-2-3-4.sslip.io" {
		t.Fatal(h)
	}

	page.Watcher, page.ServerID = true, "srv_1"
	page.Rows = append(page.Rows, RowRec{ID: "r4", Name: "Dashboard", Subject: "panel"})
	if err := SavePage(ctx, st.DB, page, n); err != nil {
		t.Fatal(err)
	}
	InsertIncident(ctx, st.DB, IncidentRec{ID: "si_1", PageID: "sp_1", Kind: KindIncident, Title: "Slow", Rows: []string{"r1"}, State: "published", CreatedAt: n}, "Looking", "su_1", n)
	InsertIncident(ctx, st.DB, IncidentRec{ID: "si_2", PageID: "sp_1", Kind: KindIncident, Title: "secret draft", Rows: []string{"r1"}, State: "draft", CreatedAt: n}, "", "", n)
	cfg, err := WatcherConfig(ctx, st.DB, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"title":"Acme"`, `{"id":"r3","name":"Cloud panels","subjects":["service:s_a","service:s_b"]}`, `{"id":"r4","name":"Dashboard","subjects":["panel"]}`, `"title":"Slow"`, `"body":"Looking"`} {
		if !strings.Contains(cfg, want) {
			t.Errorf("lacks %s in %s", want, cfg)
		}
	}
	if strings.Contains(cfg, "secret draft") || strings.Contains(cfg, "Gone") || strings.Contains(cfg, "cells") && !strings.Contains(cfg, "Cloud panels") {
		t.Fatalf("%s", cfg)
	}
}
