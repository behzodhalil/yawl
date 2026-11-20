package workers

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/notify"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

const gb = 1_000_000_000

func hours(n int, disk func(i int) float64) []db.MetricHour {
	var out []db.MetricHour
	for i := 0; i < n; i++ {
		out = append(out, db.MetricHour{At: tM.Add(time.Duration(i-n) * time.Hour).UnixMilli(), Disk: int64(disk(i)), DiskTotal: 50 * gb})
	}
	return out
}

func TestForecastLine(t *testing.T) {
	days, rate, growing := app.Forecast(hours(168, func(i int) float64 { return 26*gb + float64(i)*2*gb/24 }), 50*gb)
	if !growing || math.Abs(rate-2.0) > 0.01 || days != 5 {
		t.Fatalf("days %d rate %v growing %v", days, rate, growing)
	}
}

func TestForecastNeedsADay(t *testing.T) {
	if _, _, growing := app.Forecast(hours(20, func(i int) float64 { return float64(i) * gb }), 50*gb); growing {
		t.Fatal("20 hours made a forecast")
	}
}

func forecastWorld(t *testing.T, disk func(i int) float64) (*store.Store, *Metrics, *time.Time) {
	st, w, _, now := metricsWorld(t)
	for _, h := range hours(168, disk) {
		st.DB.Exec(`INSERT INTO metric_hours (server_id, service_id, at, disk, disk_total) VALUES ('srv_1', '', ?, ?, ?)`, h.At, h.Disk, h.DiskTotal)
	}
	return st, w, now
}

func TestForecastAlerts(t *testing.T) {
	st, w, now := forecastWorld(t, func(i int) float64 { return 26*gb + float64(i)*2*gb/24 })
	*now = tM.Add(2 * time.Minute)
	w.Tick(context.Background())
	*now = tM.Add(time.Hour + 2*time.Minute)
	w.Tick(context.Background())
	want := "forecast:srv_1|open|0|hel1|hel1 disk will be full in about 5 days at this rate (+2.0 GB a day)."
	if got := keyed(t, st); len(got) != 1 || got[0] != want {
		t.Fatalf("%q", got)
	}
}

func TestForecastShrinking(t *testing.T) {
	st, w, now := forecastWorld(t, func(i int) float64 {
		if i >= 144 {
			return 32 * gb
		}
		return 30*gb + float64(i)*2*gb/24
	})
	st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "metrics.forecast.srv_1", Value: "1"})
	*now = tM.Add(2 * time.Minute)
	w.Tick(context.Background())
	got := keyed(t, st)
	if len(got) != 1 || got[0] != "forecast:srv_1|close|0|hel1|hel1 disk is no longer on course to fill within a week." {
		t.Fatalf("%q", got)
	}
}

func machineMinutes(st *store.Store, pcts []float64, gapAt int) {
	for i, p := range pcts {
		at := tM.Add(time.Duration(i-len(pcts)+1) * time.Minute)
		if i >= gapAt {
			at = at.Add(time.Minute)
		}
		st.DB.Exec(`INSERT INTO metric_minutes (server_id, service_id, at, mem, mem_total) VALUES ('srv_1', '', ?, ?, ?)`, at.UnixMilli(), int64(p*4*gb/100), 4*gb)
		st.DB.Exec(`INSERT INTO metric_minutes (server_id, service_id, at, mem) VALUES ('srv_1', 'svc_1', ?, ?)`, at.UnixMilli(), int64(1.2*gb))
	}
}

func TestMemoryNeedsTenMinutes(t *testing.T) {
	st, w, _, _ := metricsWorld(t)
	machineMinutes(st, []float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 89}, 99)
	w.Tick(context.Background())
	if got := keyed(t, st); len(got) != 0 {
		t.Fatalf("%q", got)
	}
	st.DB.Exec(`DELETE FROM metric_minutes`)
	machineMinutes(st, []float64{91, 91, 91, 91, 91, 91, 91, 91, 91, 91}, 99)
	w.Tick(context.Background())
	st.DB.Exec(`DELETE FROM metric_minutes`)
	machineMinutes(st, []float64{91, 84}, 99)
	w.Tick(context.Background())
	got := strings.Join(keyed(t, st), "\n")
	want := "memory:srv_1|open|0|hel1|hel1 memory has been above 90% for 10 minutes (top: acme/production/api 1.2 GB).\n" +
		"memory:srv_1|close|0|hel1|hel1 memory is back under 85%."
	if got != want {
		t.Fatalf("got\n%s", got)
	}
}

func TestMemoryGapResets(t *testing.T) {
	st, w, _, _ := metricsWorld(t)
	machineMinutes(st, []float64{95, 95, 95, 95, 95, 95, 95, 95, 95, 95}, 9)
	w.Tick(context.Background())
	if got := keyed(t, st); len(got) != 0 {
		t.Fatalf("%q", got)
	}
}

func TestForecastNoTotalNeverNegative(t *testing.T) {
	grow := func(i int) float64 { return 26*gb + float64(i)*2*gb/24 }
	if days, _, growing := app.Forecast(hours(168, grow), 0); growing || days != 0 {
		t.Fatalf("total 0: days %d growing %v", days, growing)
	}
	if days, _, growing := app.Forecast(hours(168, grow), 30*gb); !growing || days != 0 {
		t.Fatalf("past total: days %d growing %v", days, growing)
	}
}

func loadMinutes(st *store.Store, loads []float64, gapAt int) {
	for i, l := range loads {
		at := tM.Add(time.Duration(i-len(loads)+1) * time.Minute)
		if i >= gapAt {
			at = at.Add(time.Minute)
		}
		st.DB.Exec(`INSERT INTO metric_minutes (server_id, service_id, at, load) VALUES ('srv_1', '', ?, ?)`, at.UnixMilli(), l)
		st.DB.Exec(`INSERT INTO metric_minutes (server_id, service_id, at, cpu) VALUES ('srv_1', 'svc_1', ?, 312)`, at.UnixMilli())
	}
}

func loadWorld(t *testing.T, cores int) (*store.Store, *Metrics) {
	st, w, _, _ := metricsWorld(t)
	st.DB.Exec(`UPDATE servers SET cores = ? WHERE id = 'srv_1'`, cores)
	return st, w
}

func repeat(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestLoadOpensAndCloses(t *testing.T) {
	st, w := loadWorld(t, 4)
	loadMinutes(st, repeat(6.4, 10), 99)
	w.Tick(context.Background())
	w.Tick(context.Background())
	st.DB.Exec(`DELETE FROM metric_minutes`)
	loadMinutes(st, []float64{6.4, 3.6}, 99)
	w.Tick(context.Background())
	got := strings.Join(keyed(t, st), "\n")
	want := "load:srv_1|open|0|hel1|hel1 load has been above 1.5 per CPU for 10 minutes (top: acme/production/api 312%).\n" +
		"load:srv_1|close|0|hel1|hel1 load is back under 1.0 per CPU."
	if got != want {
		t.Fatalf("got\n%s", got)
	}
}

func TestLoadNeedsTenMinutes(t *testing.T) {
	for name, c := range map[string]struct {
		loads []float64
		gapAt int
	}{
		"nine minutes":   {repeat(8, 9), 99},
		"one under 1.5":  {append(repeat(8, 9), 5.9), 99},
		"missing minute": {repeat(8, 10), 9},
	} {
		st, w := loadWorld(t, 4)
		loadMinutes(st, c.loads, c.gapAt)
		w.Tick(context.Background())
		if got := keyed(t, st); len(got) != 0 {
			t.Fatalf("%s: %q", name, got)
		}
	}
}

func TestLoadSkipsWithoutCores(t *testing.T) {
	st, w := loadWorld(t, 0)
	loadMinutes(st, repeat(50, 10), 99)
	w.Tick(context.Background())
	if got := keyed(t, st); len(got) != 0 {
		t.Fatalf("%q", got)
	}
}

func TestLoadMessage(t *testing.T) {
	st, w := loadWorld(t, 4)
	loadMinutes(st, repeat(6.4, 10), 99)
	w.Tick(context.Background())
	rows, _ := st.Q.ListOutbox(context.Background())
	if len(rows) != 1 {
		t.Fatalf("%d rows", len(rows))
	}
	var m notify.Message
	if err := json.Unmarshal([]byte(rows[0].MessageJson), &m); err != nil {
		t.Fatal(err)
	}
	got := notify.TelegramPayload(m, m.At)["text"]
	want := "ⓘ <b>Server hel1 load is high</b>\n\n" +
		"Server: <code>hel1</code>\n" +
		"Value: 6.4 over 10 minutes, 4 CPUs (limit 1.5 per CPU)\n" +
		"Busiest: acme/production/api 312% CPU\n" +
		"Time: " + notify.When(m.At, m.At) + "\n\n" +
		"ⓘ <i>Next: Check the busiest service or give it a CPU limit.</i>"
	if got != want || m.State != "warn" || m.Head != "Server load" || m.Badge != "High" || m.Links[0].URL != "/servers/hel1" {
		t.Fatalf("got\n%s\n%+v", got, m)
	}
}
