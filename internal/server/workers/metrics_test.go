package workers

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type samples struct {
	fakeSender
	all   []proto.Sample
	away  bool
	asks  []int64
	watch []proto.LogWatch
}

func (s *samples) Send(ctx context.Context, id string, c proto.Command) (proto.Result, error) {
	if c.Name != proto.CmdMetrics {
		return s.fakeSender.Send(ctx, id, c)
	}
	if s.away {
		return proto.Result{}, context.DeadlineExceeded
	}
	var a proto.MetricsArgs
	json.Unmarshal(c.Args, &a)
	s.asks = append(s.asks, a.Since)
	s.watch = a.LogWatch
	var out []proto.Sample
	for _, x := range s.all {
		if x.At > a.Since {
			out = append(out, x)
		}
	}
	b, _ := json.Marshal(proto.MetricsData{Samples: out})
	return proto.Result{Key: c.Key, OK: true, Data: b}, nil
}

var tM = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func sample(at time.Time) proto.Sample {
	return proto.Sample{At: at.UnixMilli(), HostsOK: true,
		Machine:    proto.MachineSample{CPU: 25, Mem: 3 << 30, MemTotal: 4 << 30, Disk: 20 << 30, DiskTotal: 50 << 30, Load: 0.5},
		Containers: []proto.ContainerSample{{Project: "acme", Env: "production", Service: "api", CPU: 12, Mem: 300 << 20}},
		Hosts: []proto.HostSample{{Host: "api-production.1-2-3-4.sslip.io", Req: 60, Err5: 1, P95ms: 120},
			{Host: "api.acme.uz", Req: 40, Err5: 0, P95ms: 250}, {Host: "unknown.example", Req: 9}}}
}

func metricsWorld(t *testing.T) (*store.Store, *Metrics, *samples, *time.Time) {
	st, svc := seed(t, "web")
	id, _ := deployOnce(t, st, svc, &fakeSender{})
	_ = id
	st.DB.Exec(`INSERT INTO domains (id, service_id, host, dns_state, created_at, updated_at) VALUES ('dom_1', ?, 'api.acme.uz', 'ok', 1, 1)`, svc)
	now := tM
	gw := &samples{}
	return st, &Metrics{St: st, GW: gw, Now: func() time.Time { return now }}, gw, &now
}

type minuteRow struct {
	service             string
	cpu                 float64
	mem, req, err5, p95 int64
}

func minutes(t *testing.T, st *store.Store) []minuteRow {
	rows, err := st.DB.Query(`SELECT service_id, cpu, mem, req, err5, p95_ms FROM metric_minutes ORDER BY at, service_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []minuteRow
	for rows.Next() {
		var r minuteRow
		rows.Scan(&r.service, &r.cpu, &r.mem, &r.req, &r.err5, &r.p95)
		out = append(out, r)
	}
	return out
}

func TestMetricsStoresMinutes(t *testing.T) {
	st, w, gw, _ := metricsWorld(t)
	gw.all = []proto.Sample{sample(tM.Add(-time.Minute))}
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := minutes(t, st)
	if len(got) != 2 || got[0].service != "" || got[0].cpu != 25 || got[0].mem != 3<<30 {
		t.Fatalf("%+v", got)
	}
	if got[1] != (minuteRow{service: "svc_1", cpu: 12, mem: 300 << 20, req: 100, err5: 1, p95: 250}) {
		t.Fatalf("service row %+v", got[1])
	}
}

func TestMetricsFillsFromRing(t *testing.T) {
	st, w, gw, _ := metricsWorld(t)
	for i := 30; i >= 1; i-- {
		gw.all = append(gw.all, sample(tM.Add(-time.Duration(i)*time.Minute)))
	}
	w.Tick(context.Background())
	w.Tick(context.Background())
	if got := minutes(t, st); len(got) != 60 {
		t.Fatalf("%d rows", len(got))
	}
	if gw.asks[1] != tM.Add(-time.Minute).UnixMilli() {
		t.Fatalf("second ask since %d", gw.asks[1])
	}
}

func TestMetricsOfflineGap(t *testing.T) {
	st, w, gw, _ := metricsWorld(t)
	gw.away = true
	gw.all = []proto.Sample{sample(tM.Add(-time.Minute))}
	w.Tick(context.Background())
	if got := minutes(t, st); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestMetricsFoldsHours(t *testing.T) {
	st, w, gw, now := metricsWorld(t)
	for i := 0; i < 60; i++ {
		s := sample(tM.Add(-time.Hour + time.Duration(i)*time.Minute))
		s.Machine.CPU = float64(i)
		gw.all = append(gw.all, s)
	}
	st.DB.Exec(`INSERT INTO metric_minutes (server_id, service_id, at) VALUES ('srv_1', '', ?)`, tM.Add(-8*24*time.Hour).UnixMilli())
	st.DB.Exec(`INSERT INTO metric_hours (server_id, service_id, at) VALUES ('srv_1', '', ?)`, tM.Add(-91*24*time.Hour).UnixMilli())
	st.DB.Exec(`INSERT INTO settings (key, value) VALUES ('metrics.since.srv_1', ?)`, strconv.FormatInt(tM.Add(-2*time.Hour).UnixMilli(), 10))
	*now = tM.Add(time.Minute)
	w.Tick(context.Background())
	var n int
	var cpu float64
	var req int64
	st.DB.QueryRow(`SELECT count(*), max(cpu) FROM metric_hours WHERE service_id = '' AND at = ?`, tM.Add(-time.Hour).UnixMilli()).Scan(&n, &cpu)
	st.DB.QueryRow(`SELECT req FROM metric_hours WHERE service_id = 'svc_1' AND at = ?`, tM.Add(-time.Hour).UnixMilli()).Scan(&req)
	if n != 1 || cpu != 59 || req != 6000 {
		t.Fatalf("hour rows %d, cpu %v, req %d", n, cpu, req)
	}
	var old int
	st.DB.QueryRow(`SELECT (SELECT count(*) FROM metric_minutes WHERE at < ?) + (SELECT count(*) FROM metric_hours WHERE at < ?)`,
		tM.Add(-7*24*time.Hour).UnixMilli(), tM.Add(-90*24*time.Hour).UnixMilli()).Scan(&old)
	if old != 0 {
		t.Fatalf("%d old rows kept", old)
	}
}

func TestMetricsGapNotZero(t *testing.T) {
	st, w, gw, _ := metricsWorld(t)
	a := sample(tM.Add(-2 * time.Minute))
	a.HostsOK = false
	b := sample(tM.Add(-time.Minute))
	b.Hosts = append(b.Hosts, proto.HostSample{Host: "api.acme.uz", Gap: true})
	gw.all = []proto.Sample{a, b}
	w.Tick(context.Background())
	var nulls int
	st.DB.QueryRow(`SELECT count(*) FROM metric_minutes WHERE service_id = 'svc_1' AND req IS NULL AND err5 IS NULL AND p95_ms IS NULL`).Scan(&nulls)
	if nulls != 2 {
		t.Fatalf("%d gap rows", nulls)
	}
}

func TestMetricsRefoldsLateMinutes(t *testing.T) {
	st, w, gw, now := metricsWorld(t)
	st.DB.Exec(`INSERT INTO settings (key, value) VALUES ('metrics.since.srv_1', ?)`, strconv.FormatInt(tM.Add(-2*time.Hour).UnixMilli(), 10))
	for i := 0; i < 30; i++ {
		gw.all = append(gw.all, sample(tM.Add(-time.Hour+time.Duration(i)*time.Minute)))
	}
	*now = tM.Add(time.Minute)
	w.Tick(context.Background())
	for i := 30; i < 60; i++ {
		gw.all = append(gw.all, sample(tM.Add(-time.Hour+time.Duration(i)*time.Minute)))
	}
	*now = tM.Add(time.Hour + time.Minute)
	w.Tick(context.Background())
	var req int64
	st.DB.QueryRow(`SELECT req FROM metric_hours WHERE service_id = 'svc_1' AND at = ?`, tM.Add(-time.Hour).UnixMilli()).Scan(&req)
	if req != 6000 {
		t.Fatalf("hour req %d", req)
	}
}

func TestMetricsOOMIncident(t *testing.T) {
	st, w, gw, now := metricsWorld(t)
	st.DB.Exec(`UPDATE services SET memory = 512000000 WHERE name = 'api'`)
	oom := func(at time.Time) proto.Sample {
		s := sample(at)
		s.OOMs = []proto.OOMSample{{Project: "acme", Env: "production", Service: "api", Kills: 1}}
		return s
	}
	type msg struct{ body, incident, action string }
	sent := func() []msg {
		rows, _ := st.DB.Query(`SELECT body, incident, action FROM outbox WHERE incident LIKE 'oom:%' ORDER BY created_at, rowid`)
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
		*now = at
		if err := w.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	gw.all = []proto.Sample{oom(tM.Add(-time.Minute))}
	tick(tM)
	gw.all = append(gw.all, oom(tM))
	tick(tM.Add(time.Minute))
	if m := sent(); len(m) != 1 || m[0].body != "acme/production/api ran out of its 512 MB memory limit and was restarted." || m[0].action != "open" {
		t.Fatalf("%+v", m)
	}
	tick(tM.Add(59 * time.Minute))
	if m := sent(); len(m) != 1 {
		t.Fatalf("closed early: %+v", m)
	}
	tick(tM.Add(60 * time.Minute))
	if m := sent(); len(m) != 2 || m[1].body != "acme/production/api has not run out of memory for an hour." || m[1].action != "close" || m[1].incident != m[0].incident {
		t.Fatalf("%+v", m)
	}
}

func TestRecordOOMsKeepsGoing(t *testing.T) {
	st, _, _, _ := metricsWorld(t)
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO settings (key, value) VALUES ('metrics.oom.svc_a', '1')`)
	s := proto.Sample{At: tM.UnixMilli(), OOMs: []proto.OOMSample{
		{Project: "acme", Env: "production", Service: "a", Kills: 1}, {Project: "acme", Env: "production", Service: "b", Kills: 1}}}
	byName := map[string]string{"acme/production/a": "svc_a", "acme/production/b": "svc_b"}
	if err := recordOOMs(ctx, st.Q, s, byName, tM); err != nil {
		t.Fatal(err)
	}
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM outbox WHERE incident = 'oom:svc_b' AND action = 'open'`).Scan(&n)
	a, _ := st.Q.GetSetting(ctx, "metrics.oom.svc_a")
	if n != 1 || a != strconv.FormatInt(tM.UnixMilli(), 10) {
		t.Fatalf("b opened %d, a's last kill %q", n, a)
	}
}

func TestMetricsStoresVisits(t *testing.T) {
	st, w, gw, _ := metricsWorld(t)
	v := func(at time.Time, visitors int64) proto.Sample {
		s := sample(at)
		s.Visits = []proto.VisitSample{
			{Host: "api.acme.uz", S2: 10, S4: 2, S5: 1, Bytes: 5000, Views: 7, Visitors: visitors, Day: "2026-10-27", Paths: map[string]int64{"/": 5, "/a": 2}, Refs: map[string]int64{"news.example": 3}},
			{Host: "unknown.example", S2: 99, Views: 99},
		}
		return s
	}
	gw.all = []proto.Sample{v(tM.Add(-2*time.Minute), 4), v(tM.Add(-time.Minute), 3)}
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO analytics_hours (service_id, at, s2) VALUES ('svc_1', ?, 1)`, tM.Add(-31*24*time.Hour).UnixMilli())
	if err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	hours, _ := st.Q.AnalyticsHours(ctx, db.AnalyticsHoursParams{ServiceID: "svc_1", At: 0})
	if len(hours) != 1 || hours[0].S2 != 20 || hours[0].S4 != 4 || hours[0].S5 != 2 || hours[0].Bytes != 10000 || hours[0].Views != 14 || hours[0].At != tM.Add(-time.Hour).UnixMilli() {
		t.Fatalf("%+v", hours)
	}
	days, _ := st.Q.AnalyticsDays(ctx, db.AnalyticsDaysParams{ServiceID: "svc_1", Day: ""})
	if len(days) != 1 || days[0].Visitors != 4 {
		t.Fatalf("%+v", days)
	}
	top, _ := st.Q.AnalyticsTop(ctx, db.AnalyticsTopParams{ServiceID: "svc_1", At: 0})
	if len(top) != 3 || top[0].Value != "/" || top[0].N != 10 {
		t.Fatalf("%+v", top)
	}
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM analytics_hours WHERE service_id = '' OR s2 = 99`).Scan(&n)
	if n != 0 {
		t.Fatal("an unknown host was counted")
	}
}

func TestMetricsFoldAfterLongDown(t *testing.T) {
	st, w, _, now := metricsWorld(t)
	st.DB.Exec(`INSERT INTO settings (key, value) VALUES ('metrics.folded', ?)`, strconv.FormatInt(tM.Add(-72*time.Hour).UnixMilli(), 10))
	st.DB.Exec(`INSERT INTO settings (key, value) VALUES ('metrics.since.srv_1', ?)`, strconv.FormatInt(tM.UnixMilli(), 10))
	for _, back := range []time.Duration{40 * time.Hour, 2 * time.Hour} {
		st.DB.Exec(`INSERT INTO metric_minutes (server_id, service_id, at, cpu) VALUES ('srv_1', '', ?, 5)`, tM.Add(-back).UnixMilli())
	}
	*now = tM.Add(time.Minute)
	w.Tick(context.Background())
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM metric_hours WHERE service_id = ''`).Scan(&n)
	if n != 2 {
		t.Fatalf("%d hours folded", n)
	}
}

func TestMetricsNetwork(t *testing.T) {
	st, w, gw, now := metricsWorld(t)
	for i := 0; i < 60; i++ {
		s := sample(tM.Add(-time.Hour + time.Duration(i)*time.Minute))
		if i > 0 {
			rx, tx := int64(i*1000), int64(i*10)
			s.Machine.NetRx, s.Machine.NetTx = &rx, &tx
		}
		gw.all = append(gw.all, s)
	}
	st.DB.Exec(`INSERT INTO settings (key, value) VALUES ('metrics.since.srv_1', ?)`, strconv.FormatInt(tM.Add(-2*time.Hour).UnixMilli(), 10))
	*now = tM.Add(time.Minute)
	w.Tick(context.Background())
	var nulls, rx, tx int64
	st.DB.QueryRow(`SELECT count(*) FROM metric_minutes WHERE service_id = '' AND net_rx IS NULL`).Scan(&nulls)
	st.DB.QueryRow(`SELECT net_rx, net_tx FROM metric_hours WHERE service_id = '' AND at = ?`, tM.Add(-time.Hour).UnixMilli()).Scan(&rx, &tx)
	if nulls != 1 || rx != 59_000 || tx != 590 {
		t.Fatalf("nulls %d, hour rx %d tx %d", nulls, rx, tx)
	}
}
