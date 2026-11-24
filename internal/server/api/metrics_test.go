package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func metricsAPI(t *testing.T) (*app.App, func(path string) (int, []byte)) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, three)
	return a, func(path string) (int, []byte) {
		res, b := call(t, srv, tok, "GET", path, "")
		return res.StatusCode, b
	}
}

func TestMetricsRangePoints(t *testing.T) {
	a, get := metricsAPI(t)
	ctx := context.Background()
	svc, _ := a.St.Q.GetServiceByName(ctx, db.GetServiceByNameParams{Project: "acme", Env: "production", Service: "api"})
	now := a.Now()
	tx, _ := a.St.DB.Begin()
	for i := 0; i < 90*24; i++ {
		tx.Exec(`INSERT INTO metric_hours (server_id, service_id, at, cpu, req) VALUES ('srv_1', ?, ?, 10, 600)`, svc.ID, now.Add(-time.Duration(i)*time.Hour).Truncate(time.Hour).UnixMilli())
	}
	for i := 0; i < 120; i++ {
		tx.Exec(`INSERT INTO metric_minutes (server_id, service_id, at, cpu, req) VALUES ('srv_1', ?, ?, 10, 10)`, svc.ID, now.Add(-time.Duration(i)*time.Minute).Truncate(time.Minute).UnixMilli())
	}
	tx.Commit()
	var v struct {
		Range  string
		StepS  int `json:"step_s"`
		Points []map[string]any
	}
	for _, c := range []struct {
		r    string
		step int
		max  int
	}{{"1h", 60, 61}, {"24h", 60, 1441}, {"7d", 3600, 169}, {"30d", 3600, 721}, {"90d", 3600, 2161}} {
		code, b := get("/api/services/acme/production/api/metrics?range=" + c.r)
		json.Unmarshal(b, &v)
		if code != 200 || v.StepS != c.step || len(v.Points) == 0 || len(v.Points) > c.max {
			t.Fatalf("%s: %d step %d, %d points", c.r, code, v.StepS, len(v.Points))
		}
	}
	if _, ok := v.Points[0]["req"]; !ok {
		t.Fatalf("a service point has no req: %v", v.Points[0])
	}
	if code, b := get("/api/services/acme/production/api/metrics?range=2h"); code != 400 || string(b) != "range must be 1h, 24h, 7d, 30d or 90d\n" {
		t.Fatalf("%d %q", code, b)
	}
}

func TestServerMetricsAndForecast(t *testing.T) {
	a, get := metricsAPI(t)
	now := a.Now()
	tx, _ := a.St.DB.Begin()
	for i := 0; i < 168; i++ {
		at := now.Add(-time.Duration(168-i) * time.Hour).Truncate(time.Hour).UnixMilli()
		tx.Exec(`INSERT INTO metric_hours (server_id, service_id, at, disk, disk_total, mem, mem_total) VALUES ('srv_1', '', ?, ?, 50000000000, 1, 2)`, at, int64(26e9+float64(i)*2e9/24))
	}
	tx.Commit()
	code, b := get("/api/servers/hel1/metrics?range=7d")
	if code != 200 || !strings.Contains(string(b), `"disk_total":50000000000`) {
		t.Fatalf("%d %s", code, b)
	}
	_, b = get("/api/servers")
	if !strings.Contains(string(b), `"forecast":{"days":5,"gb_per_day":2`) || !strings.Contains(string(b), `"growing":true`) {
		t.Fatalf("%s", b)
	}
}

func TestServiceMetricsOnePointAMinute(t *testing.T) {
	a, get := metricsAPI(t)
	ctx := context.Background()
	svc, _ := a.St.Q.GetServiceByName(ctx, db.GetServiceByNameParams{Project: "acme", Env: "production", Service: "api"})
	at := a.Now().Add(-10 * time.Minute).Truncate(time.Minute).UnixMilli()
	a.St.DB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, created_at, updated_at) VALUES ('srv_2', 'hk1', '5.6.7.8', 'P', '', 1, 1)`)
	a.St.DB.Exec(`INSERT INTO metric_minutes (server_id, service_id, at, cpu, mem, req, err5, p95_ms) VALUES ('srv_1', ?, ?, 10, 100, 5, 1, 200), ('srv_2', ?, ?, 20, 300, 7, NULL, 90)`,
		svc.ID, at, svc.ID, at)
	_, b := get("/api/services/acme/production/api/metrics?range=1h")
	var v struct{ Points []appPoint }
	json.Unmarshal(b, &v)
	if len(v.Points) != 1 {
		t.Fatalf("%s", b)
	}
	p := v.Points[0]
	if p.CPU != 30 || p.Mem != 400 || *p.Req != 12 || *p.Err5 != 1 || *p.P95ms != 200 {
		t.Fatalf("%s", b)
	}
}

func TestServerOverviewData(t *testing.T) {
	a, get := metricsAPI(t)
	ctx := context.Background()
	_, b := get("/api/servers")
	if strings.Contains(string(b), `"os_name"`) || strings.Contains(string(b), `"cores"`) || strings.Contains(string(b), `"reboot_required"`) {
		t.Fatalf("an agent that reported nothing has no facts: %s", b)
	}
	a.St.Q.TouchServer(ctx, db.TouchServerParams{ID: "srv_1", AgentVersion: "1.0.0", OsName: "Ubuntu 24.04.1 LTS", OsID: "ubuntu", Kernel: "6.8.0", Cores: 4, BootedAt: 1000, RebootRequired: 1})
	svc, _ := a.St.Q.GetServiceByName(ctx, db.GetServiceByNameParams{Project: "acme", Env: "production", Service: "api"})
	a.St.DB.Exec(`UPDATE services SET memory = 512000000 WHERE id = ?`, svc.ID)
	at := a.Now().Add(-5 * time.Minute).Truncate(time.Minute).UnixMilli()
	a.St.DB.Exec(`INSERT INTO metric_minutes (server_id, service_id, at, mem_total, disk_total, net_rx, net_tx) VALUES ('srv_1', '', ?, 8000000000, 80000000000, 1250, 300), ('srv_1', '', ?, 8000000000, 80000000000, NULL, NULL)`, at-60_000, at)
	a.St.DB.Exec(`INSERT INTO metric_minutes (server_id, service_id, at, cpu, mem) VALUES ('srv_1', ?, ?, 40, 1), ('srv_1', ?, ?, 12.5, 470000000)`, svc.ID, at-60_000, svc.ID, at)
	_, b = get("/api/servers")
	for _, want := range []string{`"os_name":"Ubuntu 24.04.1 LTS"`, `"os_id":"ubuntu"`, `"kernel":"6.8.0"`, `"cores":4`, `"booted_at":1000`, `"reboot_required":true`, `"mem_total":8000000000`, `"disk_total":80000000000`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("no %s in %s", want, b)
		}
	}
	_, b = get("/api/servers/hel1/metrics?range=1h")
	var v struct {
		Points   []machinePoint
		Services []serviceNow
	}
	json.Unmarshal(b, &v)
	if len(v.Points) != 2 || *v.Points[0].NetRxBytesPerSec != 1250 || *v.Points[0].NetTxBytesPerSec != 300 || v.Points[1].NetRxBytesPerSec != nil {
		t.Fatalf("%s", b)
	}
	if len(v.Services) != 1 || v.Services[0].Name != "api" || v.Services[0].CPUPercentOfCore != 12.5 || v.Services[0].MemLimitBytes != 512000000 {
		t.Fatalf("the newest minute only: %s", b)
	}
}
