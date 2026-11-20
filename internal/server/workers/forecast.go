package workers

import (
	"context"
	"fmt"
	"github.com/behzodhalil/yawl/internal/server/app"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

const bytesGB = app.BytesGB

func (w *Metrics) forecasts(ctx context.Context, now time.Time) error {
	q := w.St.Q
	servers, err := q.ListServers(ctx)
	if err != nil {
		return err
	}
	for _, s := range servers {
		rows, err := q.MetricHours(ctx, db.MetricHoursParams{ServerID: s.ID, ServiceID: "", At: now.Add(-7 * 24 * time.Hour).UnixMilli()})
		if err != nil || len(rows) == 0 {
			continue
		}
		days, rate, growing := app.Forecast(rows, uint64(rows[len(rows)-1].DiskTotal))
		k := "metrics.forecast." + s.ID
		open := setting(ctx, q, k) == "1"
		ms := now.UnixMilli()
		switch soon := growing && days <= 7; {
		case soon && !open:
			err = w.St.Tx(ctx, func(q *db.Queries) error {
				if err := q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: "1"}); err != nil {
					return err
				}
				return announceIncident(ctx, q, ms, "server.forecast", s.ID, map[string]any{"days": days, "gb_per_day": rate},
					app.ServerForecast(s.Name, fmt.Sprintf("full in about %d days (+%.1f GB a day)", days, rate),
						fmt.Sprintf("%s disk will be full in about %d days at this rate (+%.1f GB a day).", s.Name, days, rate)),
					"forecast:"+s.ID, "open", s.Name, false)
			})
		case !soon && open:
			err = w.St.Tx(ctx, func(q *db.Queries) error {
				if err := q.DeleteSettingKey(ctx, k); err != nil {
					return err
				}
				return announceIncident(ctx, q, ms, "server.forecast", s.ID, nil,
					app.ServerForecastOK(s.Name, s.Name+" disk is no longer on course to fill within a week."), "forecast:"+s.ID, "close", s.Name, false)
			})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *Metrics) checks(ctx context.Context, servers []db.Server, now time.Time) error {
	q := w.St.Q
	keys, err := q.ServiceKeys(ctx)
	if err != nil {
		return err
	}
	label := map[string]string{}
	for _, k := range keys {
		label[k.ID] = k.Project + "/" + k.Env + "/" + k.Name
	}
	if err := w.closeOOMs(ctx, label, now); err != nil {
		return err
	}
	for _, s := range servers {
		rows, err := q.LastMachineMinutes(ctx, db.LastMachineMinutesParams{ServerID: s.ID, Limit: 10})
		if err != nil || len(rows) == 0 {
			continue
		}
		if err := w.checkLoad(ctx, s, rows, label, now); err != nil {
			return err
		}
		if rows[0].MemTotal == 0 {
			continue
		}
		pct := func(r db.MetricMinute) float64 { return float64(r.Mem) * 100 / float64(max(r.MemTotal, 1)) }
		k := "metrics.memory." + s.ID
		open := setting(ctx, q, k) == "1"
		high := sustained(rows, func(r db.MetricMinute) bool { return pct(r) >= 90 })
		ms := now.UnixMilli()
		switch {
		case high && !open:
			text, topWord := s.Name+" memory has been above 90% for 10 minutes.", ""
			if top, err := q.TopContainerAt(ctx, db.TopContainerAtParams{ServerID: s.ID, At: rows[0].At}); err == nil && label[top.ServiceID] != "" {
				topWord = fmt.Sprintf("%s %.1f GB", label[top.ServiceID], float64(top.Mem)/bytesGB)
				text = fmt.Sprintf("%s memory has been above 90%% for 10 minutes (top: %s).", s.Name, topWord)
			}
			err = w.St.Tx(ctx, func(q *db.Queries) error {
				if err := q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: "1"}); err != nil {
					return err
				}
				return announceIncident(ctx, q, ms, "server.memory", s.ID, nil, app.ServerMemory(s.Name, topWord, text), "memory:"+s.ID, "open", s.Name, false)
			})
		case open && pct(rows[0]) < 85:
			err = w.St.Tx(ctx, func(q *db.Queries) error {
				if err := q.DeleteSettingKey(ctx, k); err != nil {
					return err
				}
				return announceIncident(ctx, q, ms, "server.memory", s.ID, nil, app.ServerMemoryOK(s.Name, s.Name+" memory is back under 85%."), "memory:"+s.ID, "close", s.Name, false)
			})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func sustained(rows []db.MetricMinute, high func(db.MetricMinute) bool) bool {
	if len(rows) != 10 {
		return false
	}
	for i, r := range rows {
		if !high(r) || (i > 0 && rows[i-1].At-r.At != int64(time.Minute/time.Millisecond)) {
			return false
		}
	}
	return true
}

func (w *Metrics) checkLoad(ctx context.Context, s db.Server, rows []db.MetricMinute, label map[string]string, now time.Time) error {
	if s.Cores <= 0 {
		return nil
	}
	q, cores := w.St.Q, float64(s.Cores)
	k := "metrics.load." + s.ID
	open := setting(ctx, q, k) == "1"
	ms := now.UnixMilli()
	switch {
	case !open && sustained(rows, func(r db.MetricMinute) bool { return r.Load/cores >= 1.5 }):
		sum := 0.0
		for _, r := range rows {
			sum += r.Load
		}
		value := fmt.Sprintf("%.1f over 10 minutes, %d CPUs (limit 1.5 per CPU)", sum/10, s.Cores)
		text, busiest := s.Name+" load has been above 1.5 per CPU for 10 minutes.", ""
		if top, err := q.TopCPUContainerAt(ctx, db.TopCPUContainerAtParams{ServerID: s.ID, At: rows[0].At}); err == nil && label[top.ServiceID] != "" {
			busiest = fmt.Sprintf("%s %.0f%% CPU", label[top.ServiceID], top.Cpu)
			text = fmt.Sprintf("%s load has been above 1.5 per CPU for 10 minutes (top: %s %.0f%%).", s.Name, label[top.ServiceID], top.Cpu)
		}
		return w.St.Tx(ctx, func(q *db.Queries) error {
			if err := q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: "1"}); err != nil {
				return err
			}
			return announceIncident(ctx, q, ms, "server.load", s.ID, nil, app.ServerLoad(s.Name, value, busiest, text), "load:"+s.ID, "open", s.Name, false)
		})
	case open && rows[0].Load/cores < 1.0:
		value := fmt.Sprintf("%.1f now, %d CPUs", rows[0].Load, s.Cores)
		return w.St.Tx(ctx, func(q *db.Queries) error {
			if err := q.DeleteSettingKey(ctx, k); err != nil {
				return err
			}
			return announceIncident(ctx, q, ms, "server.load", s.ID, nil, app.ServerLoadOK(s.Name, value, s.Name+" load is back under 1.0 per CPU."), "load:"+s.ID, "close", s.Name, false)
		})
	}
	return nil
}
