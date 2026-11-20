package api

import (
	"net/http"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type analyticsBucket struct {
	At       int64 `json:"at"`
	Views    int64 `json:"views"`
	Visitors int64 `json:"visitors,omitempty"`
	Requests int64 `json:"requests"`
	Errors   int64 `json:"errors"`
	Bytes    int64 `json:"bytes"`
}

type analyticsTop struct {
	Value string `json:"value"`
	N     int64  `json:"n"`
}

type analyticsView struct {
	Range    string `json:"range"`
	Visitors int64  `json:"visitors"`
	Views    int64  `json:"views"`
	Requests int64  `json:"requests"`
	Errors   int64  `json:"errors"`
	Bytes    int64  `json:"bytes"`
	S2       int64  `json:"s2"`
	S3       int64  `json:"s3"`
	S4       int64  `json:"s4"`
	S5       int64  `json:"s5"`

	Series []analyticsBucket `json:"series"`
	Paths  []analyticsTop    `json:"paths"`
	Refs   []analyticsTop    `json:"refs"`
}

func (h *handler) analytics(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.service(w, r)
	if !ok {
		return
	}
	rg := r.URL.Query().Get("range")
	if rg == "" {
		rg = "24h"
	}
	n, step := map[string]int{"24h": 24, "7d": 7, "30d": 30}[rg], 24*time.Hour
	if n == 0 {
		http.Error(w, "range is 24h, 7d or 30d", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	last := now.Truncate(24 * time.Hour)
	if rg == "24h" {
		step, last = time.Hour, now.Truncate(time.Hour)
	}
	start := last.Add(-time.Duration(n-1) * step)
	ctx := r.Context()
	hours, err := h.a.St.Q.AnalyticsHours(ctx, db.AnalyticsHoursParams{ServiceID: svc.ID, At: start.UnixMilli()})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	days, err := h.a.St.Q.AnalyticsDays(ctx, db.AnalyticsDaysParams{ServiceID: svc.ID, Day: start.Format("2006-01-02")})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	top, err := h.a.St.Q.AnalyticsTop(ctx, db.AnalyticsTopParams{ServiceID: svc.ID, At: start.UnixMilli()})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	v := analyticsView{Range: rg, Series: make([]analyticsBucket, n), Paths: []analyticsTop{}, Refs: []analyticsTop{}}
	for i := range v.Series {
		v.Series[i].At = start.Add(time.Duration(i) * step).UnixMilli()
	}
	for _, x := range hours {
		b := &v.Series[min(n-1, int(time.UnixMilli(x.At).Sub(start)/step))]
		req := x.S2 + x.S3 + x.S4 + x.S5
		b.Views, b.Requests, b.Errors, b.Bytes = b.Views+x.Views, b.Requests+req, b.Errors+x.S5, b.Bytes+x.Bytes
		v.Views, v.Requests, v.Errors, v.Bytes = v.Views+x.Views, v.Requests+req, v.Errors+x.S5, v.Bytes+x.Bytes
		v.S2, v.S3, v.S4, v.S5 = v.S2+x.S2, v.S3+x.S3, v.S4+x.S4, v.S5+x.S5
	}
	for _, d := range days {
		if rg == "24h" && d.Day != now.Format("2006-01-02") {
			continue
		}
		v.Visitors += d.Visitors
		if day, err := time.Parse("2006-01-02", d.Day); err == nil && step == 24*time.Hour && !day.Before(start) {
			v.Series[min(n-1, int(day.Sub(start)/step))].Visitors = d.Visitors
		}
	}
	for _, t := range top {
		item := analyticsTop{Value: t.Value, N: t.N}
		if t.Kind == "path" && len(v.Paths) < 10 {
			v.Paths = append(v.Paths, item)
		} else if t.Kind == "ref" && len(v.Refs) < 10 {
			v.Refs = append(v.Refs, item)
		}
	}
	writeJSON(w, v)
}
