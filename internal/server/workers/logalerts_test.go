package workers

import (
	"context"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
)

func TestMetricsLogAlerts(t *testing.T) {
	st, w, gw, now := metricsWorld(t)
	w.PanelURL = "https://panel.acme.uz/"
	var id string
	st.DB.QueryRow(`SELECT id FROM services WHERE name = 'api'`).Scan(&id)
	rules := func(js string) {
		st.DB.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, LogAlertsKey+id, js)
	}
	rules(`[{"id":"la_err","lines":5,"minutes":5},{"id":"la_time","text":"timeout","lines":0,"minutes":10}]`)
	hits := func(at time.Time, hs ...proto.LogHit) proto.Sample {
		s := sample(at)
		s.LogHits = hs
		return s
	}
	type msg struct{ body, incident, action string }
	sent := func() []msg {
		rows, _ := st.DB.Query(`SELECT body, incident, action FROM outbox WHERE incident LIKE 'logs:%' ORDER BY created_at, rowid`)
		defer rows.Close()
		var out []msg
		for rows.Next() {
			var m msg
			rows.Scan(&m.body, &m.incident, &m.action)
			out = append(out, m)
		}
		return out
	}
	tick := func(at time.Time) {
		t.Helper()
		*now = at
		if err := w.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	gw.all = []proto.Sample{
		hits(tM.Add(-6*time.Minute), proto.LogHit{Rule: "la_err", Lines: 9, First: "too old"}),
		hits(tM.Add(-2*time.Minute), proto.LogHit{Rule: "la_err", Lines: 3, First: "panic: nil map"}),
		hits(tM.Add(-time.Minute), proto.LogHit{Rule: "la_err", Lines: 3, First: "panic: again"}),
	}
	tick(tM)
	if len(gw.watch) != 2 || gw.watch[0] != (proto.LogWatch{Rule: "la_err", Project: "acme", Env: "production", Service: "api"}) || gw.watch[1].Text != "timeout" {
		t.Fatalf("watch %+v", gw.watch)
	}
	want := "acme/production/api: 6 stderr lines in 5 minutes (alert: more than 5).\nFirst: panic: nil map\nLogs: https://panel.acme.uz/p/acme/production/s/api/logs"
	if m := sent(); len(m) != 1 || m[0].body != want || m[0].incident != "logs:la_err" || m[0].action != "open" {
		t.Fatalf("%+v", m)
	}
	gw.all = append(gw.all, hits(tM, proto.LogHit{Rule: "la_err", Lines: 40, First: "x"}, proto.LogHit{Rule: "la_time", Lines: 1, First: "upstream Timeout"}))
	tick(tM.Add(time.Minute))
	m := sent()
	if len(m) != 2 || m[1].body != "acme/production/api: a line with \"timeout\" in 10 minutes.\nFirst: upstream Timeout\nLogs: https://panel.acme.uz/p/acme/production/s/api/logs" {
		t.Fatalf("%+v", m)
	}
	tick(tM.Add(30 * time.Minute))
	if m := sent(); len(m) != 2 {
		t.Fatalf("closed early: %+v", m)
	}
	rules(`[{"id":"la_err","lines":5,"minutes":5}]`)
	tick(tM.Add(31 * time.Minute))
	m = sent()
	if len(m) != 4 || m[2].action != "close" || m[3].action != "close" {
		t.Fatalf("%+v", m)
	}
	bodies := map[string]string{m[2].incident: m[2].body, m[3].incident: m[3].body}
	if bodies["logs:la_err"] != "acme/production/api: stderr lines under the alert for 30 minutes." || bodies["logs:la_time"] != "acme/production/api: the log alert was removed." {
		t.Fatalf("%+v", bodies)
	}
	tick(tM.Add(32 * time.Minute))
	if m := sent(); len(m) != 4 {
		t.Fatalf("closed twice: %+v", m)
	}
}

func TestAddLogHitsSumsServers(t *testing.T) {
	w := &Metrics{}
	w.addLogHits([]proto.Sample{{At: 60000, LogHits: []proto.LogHit{{Rule: "la_1", Lines: 2, First: "a"}}}})
	w.addLogHits([]proto.Sample{{At: 60000, LogHits: []proto.LogHit{{Rule: "la_1", Lines: 3, First: "b"}}}})
	if m := w.logs["la_1"][60000]; m.lines != 5 || m.first != "a" {
		t.Fatalf("%+v", m)
	}
}
