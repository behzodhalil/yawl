package api

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func (h *handler) routesMetrics(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{project}/{env}/{name}/metrics", h.serviceMetrics)
	mux.HandleFunc("GET /api/servers/{server}/metrics", h.serverMetrics)
}

func span(r *http.Request) (since time.Duration, hours bool, ok bool) {
	switch r.URL.Query().Get("range") {
	case "1h":
		return time.Hour, false, true
	case "24h":
		return 24 * time.Hour, false, true
	case "7d":
		return 7 * 24 * time.Hour, true, true
	case "30d":
		return 30 * 24 * time.Hour, true, true
	case "90d":
		return 90 * 24 * time.Hour, true, true
	}
	return 0, false, false
}

type appPoint struct {
	At    int64   `json:"at"`
	CPU   float64 `json:"cpu"`
	Mem   int64   `json:"mem"`
	Req   *int64  `json:"req"`
	Err5  *int64  `json:"err5"`
	P95ms *int64  `json:"p95_ms"`
}

func nullable(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}

type machinePoint struct {
	At               int64   `json:"at"`
	CPU              float64 `json:"cpu"`
	Mem              int64   `json:"mem"`
	MemTotal         int64   `json:"mem_total"`
	Disk             int64   `json:"disk"`
	DiskTotal        int64   `json:"disk_total"`
	Load             float64 `json:"load"`
	NetRxBytesPerSec *int64  `json:"net_rx"`
	NetTxBytesPerSec *int64  `json:"net_tx"`
}

type serviceNow struct {
	Project          string  `json:"project"`
	Env              string  `json:"env"`
	Name             string  `json:"name"`
	CPUPercentOfCore float64 `json:"cpu"`
	MemBytes         int64   `json:"mem"`
	CPULimitCores    float64 `json:"cpu_limit"`
	MemLimitBytes    int64   `json:"mem_limit"`
}

func metricsAnswer(w http.ResponseWriter, rng string, hours bool, points any) {
	writeJSON(w, metricsBody(rng, hours, points))
}

func metricsBody(rng string, hours bool, points any) map[string]any {
	step := 60
	if hours {
		step = 3600
	}
	return map[string]any{"range": rng, "step_s": step, "points": points}
}

func (h *handler) serviceMetrics(w http.ResponseWriter, r *http.Request) {
	d, hours, ok := span(r)
	if !ok {
		http.Error(w, "range must be 1h, 24h, 7d, 30d or 90d", http.StatusBadRequest)
		return
	}
	s, ok := h.service(w, r)
	if !ok {
		return
	}
	ctx, since := r.Context(), h.a.Now().Add(-d).UnixMilli()
	var rows []db.MetricMinute
	var err error
	if hours {
		var hs []db.MetricHour
		hs, err = h.a.St.Q.ServiceMetricHours(ctx, db.ServiceMetricHoursParams{ServiceID: s.ID, At: since})
		for _, x := range hs {
			rows = append(rows, db.MetricMinute(x))
		}
	} else {
		rows, err = h.a.St.Q.ServiceMetricMinutes(ctx, db.ServiceMetricMinutesParams{ServiceID: s.ID, At: since})
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	points := []appPoint{}
	for i, x := range rows {
		if n := len(points); n > 0 && points[n-1].At == x.At {
			p := rows[i-1]
			x.Cpu, x.Mem, x.P95Ms = x.Cpu+p.Cpu, x.Mem+p.Mem, sql.NullInt64{Int64: max(x.P95Ms.Int64, p.P95Ms.Int64), Valid: x.P95Ms.Valid || p.P95Ms.Valid}
			x.Req = sql.NullInt64{Int64: x.Req.Int64 + p.Req.Int64, Valid: x.Req.Valid || p.Req.Valid}
			x.Err5 = sql.NullInt64{Int64: x.Err5.Int64 + p.Err5.Int64, Valid: x.Err5.Valid || p.Err5.Valid}
			rows[i], points = x, points[:n-1]
		}
		points = append(points, appPoint{At: x.At, CPU: x.Cpu, Mem: x.Mem, Req: nullable(x.Req), Err5: nullable(x.Err5), P95ms: nullable(x.P95Ms)})
	}
	metricsAnswer(w, r.URL.Query().Get("range"), hours, points)
}

func (h *handler) serverMetrics(w http.ResponseWriter, r *http.Request) {
	d, hours, ok := span(r)
	if !ok {
		http.Error(w, "range must be 1h, 24h, 7d, 30d or 90d", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	srv, err := h.a.St.Q.GetServerByName(ctx, r.PathValue("server"))
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such server", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	since := h.a.Now().Add(-d).UnixMilli()
	var rows []db.MetricMinute
	if hours {
		var hs []db.MetricHour
		hs, err = h.a.St.Q.MetricHours(ctx, db.MetricHoursParams{ServerID: srv.ID, ServiceID: "", At: since})
		for _, x := range hs {
			rows = append(rows, db.MetricMinute(x))
		}
	} else {
		rows, err = h.a.St.Q.MetricMinutes(ctx, db.MetricMinutesParams{ServerID: srv.ID, ServiceID: "", At: since})
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	points := []machinePoint{}
	for _, x := range rows {
		points = append(points, machinePoint{At: x.At, CPU: x.Cpu, Mem: x.Mem, MemTotal: x.MemTotal, Disk: x.Disk, DiskTotal: x.DiskTotal, Load: x.Load,
			NetRxBytesPerSec: nullable(x.NetRx), NetTxBytesPerSec: nullable(x.NetTx)})
	}
	now, err := h.a.St.Q.ServerServicesNow(ctx, srv.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	services := []serviceNow{}
	for _, x := range now {
		services = append(services, serviceNow{Project: x.Project, Env: x.Env, Name: x.Name, CPUPercentOfCore: x.Cpu, MemBytes: x.Mem, CPULimitCores: x.CpuLimit, MemLimitBytes: x.MemLimit})
	}
	out := metricsBody(r.URL.Query().Get("range"), hours, points)
	out["services"] = services
	writeJSON(w, out)
}
