package workers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/watch"
)

type Metrics struct {
	St       *store.Store
	GW       Sender
	Now      func() time.Time
	PanelURL string
	logs     map[string]map[int64]logMinute
}

const (
	keepMinutes = 7 * 24 * time.Hour
	keepHours   = 90 * 24 * time.Hour
)

func (w *Metrics) Tick(ctx context.Context) error {
	q := w.St.Q
	now := w.Now()
	servers, err := q.ListServers(ctx)
	if err != nil {
		return err
	}
	keys, err := q.ServiceKeys(ctx)
	if err != nil {
		return err
	}
	byName, label := map[string]string{}, map[string]string{}
	for _, k := range keys {
		byName[k.Project+"/"+k.Env+"/"+k.Name] = k.ID
		label[k.ID] = k.Project + "/" + k.Env + "/" + k.Name
	}
	rules, err := logRules(ctx, q, label)
	if err != nil {
		return err
	}
	lw := logWatch(rules)
	hosts, err := watch.Hosts(ctx, q)
	if err != nil {
		return err
	}
	for _, s := range servers {
		if s.Offline == 1 {
			continue
		}
		if err := w.pull(ctx, s.ID, now, byName, hosts, lw); err != nil {
			return err
		}
	}
	if err := w.fold(ctx, now); err != nil {
		return err
	}
	if err := w.checkLogAlerts(ctx, rules, now); err != nil {
		return err
	}
	return w.checks(ctx, servers, now)
}

func (w *Metrics) pull(ctx context.Context, serverID string, now time.Time, byName, hosts map[string]string, lw []proto.LogWatch) error {
	q := w.St.Q
	k := "metrics.since." + serverID
	since, err := strconv.ParseInt(setting(ctx, q, k), 10, 64)
	if err != nil {
		since = now.Add(-time.Hour).UnixMilli()
	}
	cmd, _ := proto.NewCommand(fmt.Sprintf("metrics:%s:%d", serverID, now.UnixMilli()), proto.CmdMetrics, proto.MetricsArgs{Since: since, LogWatch: lw})
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	res, err := w.GW.Send(sctx, serverID, cmd)
	cancel()
	if err != nil || !res.OK {
		return nil
	}
	var data proto.MetricsData
	json.Unmarshal(res.Data, &data)
	if len(data.Samples) == 0 {
		return nil
	}
	err = w.St.Tx(ctx, func(q *db.Queries) error {
		for _, s := range data.Samples {
			m := s.Machine
			if err := q.InsertMetricMinute(ctx, db.InsertMetricMinuteParams{ServerID: serverID, ServiceID: "", At: s.At, Cpu: m.CPU,
				Mem: int64(m.Mem), MemTotal: int64(m.MemTotal), Disk: int64(m.Disk), DiskTotal: int64(m.DiskTotal), Load: m.Load,
				NetRx: nullInt(m.NetRx), NetTx: nullInt(m.NetTx)}); err != nil {
				return err
			}
			rows := map[string]*db.InsertMetricMinuteParams{}
			row := func(id string) *db.InsertMetricMinuteParams {
				if rows[id] == nil {
					rows[id] = &db.InsertMetricMinuteParams{ServerID: serverID, ServiceID: id, At: s.At}
				}
				return rows[id]
			}
			for _, c := range s.Containers {
				if id := byName[c.Project+"/"+c.Env+"/"+c.Service]; id != "" {
					r := row(id)
					r.Cpu += c.CPU
					r.Mem += int64(c.Mem)
				}
			}
			if s.HostsOK {
				gap := map[string]bool{}
				for _, id := range hosts {
					r := row(id)
					r.Req, r.Err5, r.P95Ms = sql.NullInt64{Valid: true}, sql.NullInt64{Valid: true}, sql.NullInt64{Valid: true}
				}
				for _, h := range s.Hosts {
					id := hosts[h.Host]
					if id == "" {
						continue
					}
					if h.Gap {
						gap[id] = true
						continue
					}
					r := row(id)
					r.Req.Int64 += h.Req
					r.Err5.Int64 += h.Err5
					r.P95Ms.Int64 = max(r.P95Ms.Int64, h.P95ms)
				}
				for id := range gap {
					r := row(id)
					r.Req, r.Err5, r.P95Ms = sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{}
				}
			}
			for _, r := range rows {
				if err := q.InsertMetricMinute(ctx, *r); err != nil {
					return err
				}
			}
			if err := recordOOMs(ctx, q, s, byName, now); err != nil {
				return err
			}
			if err := recordVisits(ctx, q, s, hosts); err != nil {
				return err
			}
			since = max(since, s.At)
		}
		return q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: strconv.FormatInt(since, 10)})
	})
	if err == nil {
		w.addLogHits(data.Samples)
	}
	return err
}

func (w *Metrics) fold(ctx context.Context, now time.Time) error {
	q := w.St.Q
	cur := now.Truncate(time.Hour)
	last, err := strconv.ParseInt(setting(ctx, q, "metrics.folded"), 10, 64)
	start := time.UnixMilli(last).Add(time.Hour)
	if err != nil {
		start = cur.Add(-time.Hour)
	} else if cur.Sub(start) > 48*time.Hour {
		start = cur.Add(-48 * time.Hour)
	}
	if back := cur.Add(-2 * time.Hour); start.After(back) && err == nil {
		start = back
	}
	if !start.Before(cur) || setting(ctx, q, "metrics.folded") == strconv.FormatInt(cur.Add(-time.Hour).UnixMilli(), 10) {
		return nil
	}
	for h := start; h.Before(cur); h = h.Add(time.Hour) {
		if err := q.FoldMetricHour(ctx, db.FoldMetricHourParams{Hour: h.UnixMilli(), HourEnd: h.Add(time.Hour).UnixMilli()}); err != nil {
			return err
		}
	}
	if err := q.DeleteMetricMinutesBefore(ctx, now.Add(-keepMinutes).UnixMilli()); err != nil {
		return err
	}
	if err := q.DeleteMetricHoursBefore(ctx, now.Add(-keepHours).UnixMilli()); err != nil {
		return err
	}
	old := now.Add(-keepAnalytics)
	if err := q.DeleteAnalyticsHoursBefore(ctx, old.UnixMilli()); err != nil {
		return err
	}
	if err := q.DeleteAnalyticsTopBefore(ctx, old.UnixMilli()); err != nil {
		return err
	}
	if err := q.DeleteAnalyticsDaysBefore(ctx, old.UTC().Format("2006-01-02")); err != nil {
		return err
	}
	if err := q.SetSetting(ctx, db.SetSettingParams{Key: "metrics.folded", Value: strconv.FormatInt(cur.Add(-time.Hour).UnixMilli(), 10)}); err != nil {
		return err
	}
	return w.forecasts(ctx, now)
}

const keepAnalytics = 30 * 24 * time.Hour

func recordVisits(ctx context.Context, q *db.Queries, s proto.Sample, hosts map[string]string) error {
	hour := time.UnixMilli(s.At).Truncate(time.Hour).UnixMilli()
	for _, v := range s.Visits {
		id := hosts[v.Host]
		if id == "" {
			continue
		}
		if err := q.AddAnalyticsHour(ctx, db.AddAnalyticsHourParams{ServiceID: id, At: hour, S2: v.S2, S3: v.S3, S4: v.S4, S5: v.S5, Bytes: v.Bytes, Views: v.Views}); err != nil {
			return err
		}
		if v.Day != "" {
			if err := q.SetAnalyticsVisitors(ctx, db.SetAnalyticsVisitorsParams{ServiceID: id, Day: v.Day, Visitors: v.Visitors}); err != nil {
				return err
			}
		}
		for kind, m := range map[string]map[string]int64{"path": v.Paths, "ref": v.Refs} {
			for value, n := range m {
				if err := q.AddAnalyticsTop(ctx, db.AddAnalyticsTopParams{ServiceID: id, At: hour, Kind: kind, Value: value, N: n}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func nullInt(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}
