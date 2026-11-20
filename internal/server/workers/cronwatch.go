package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/cron"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type CronWatch struct {
	St  *store.Store
	GW  Sender
	Now func() time.Time
}

func (w *CronWatch) Tick(ctx context.Context) error {
	q := w.St.Q
	if app.Adopting(ctx, q) {
		return nil
	}
	jobs, err := app.LiveCrons(ctx, q)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if err := w.job(ctx, j); err != nil {
			return err
		}
	}
	return nil
}

func (w *CronWatch) job(ctx context.Context, j app.LiveCron) error {
	q := w.St.Q
	cmd, _ := proto.NewCommand(fmt.Sprintf("cronwatch:%s:%d", j.ID, w.Now().UnixMilli()), proto.CmdCronRuns, proto.CronRemoveArgs{ID: j.ID})
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	res, err := w.GW.Send(sctx, j.ServerID, cmd)
	cancel()
	if err != nil || !res.OK {
		return nil
	}
	var data proto.CronRunsData
	json.Unmarshal(res.Data, &data)
	seenKey, failKey := "cron.seen."+j.ID, "cron.failing."+j.ID
	seen, err := strconv.ParseInt(setting(ctx, q, seenKey), 10, 64)
	if err != nil {
		seen = j.FinishedAt.Int64
	}
	tz := j.Scheduled.Timezone
	label := j.Project + "/" + j.Env + "/" + j.Name
	timeout := time.Duration(j.Scheduled.TimeoutMs) * time.Millisecond
	for i := len(data.Runs) - 1; i >= 0; i-- {
		r := data.Runs[i]
		if r.Skipped || r.FinishedAt == 0 || r.StartedAt <= seen {
			continue
		}
		seen = r.StartedAt
		failing := setting(ctx, q, failKey) == "1"
		text := CronFailure(label, tz, timeout, r)
		now := w.Now().UnixMilli()
		err := w.St.Tx(ctx, func(q *db.Queries) error {
			if err := q.SetSetting(ctx, db.SetSettingParams{Key: seenKey, Value: strconv.FormatInt(seen, 10)}); err != nil {
				return err
			}
			switch {
			case text != "" && failing:
				return nil
			case text != "":
				if err := q.SetSetting(ctx, db.SetSettingParams{Key: failKey, Value: "1"}); err != nil {
					return err
				}
				return announceIncident(ctx, q, now, "cron.failed", j.ID, map[string]string{"run": r.ID}, app.CronFailed(label, cronCause(text), text), "cron:"+j.ID, "open", label, false)
			case failing:
				if err := q.DeleteSettingKey(ctx, failKey); err != nil {
					return err
				}
				return announceIncident(ctx, q, now, "cron.recovered", j.ID, map[string]string{"run": r.ID}, app.CronOK(label, label+" succeeded again."), "cron:"+j.ID, "close", label, false)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func setting(ctx context.Context, q *db.Queries, k string) string {
	v, _ := q.GetSetting(ctx, k)
	return v
}

func Lasted(d time.Duration) string {
	m := int(d / time.Minute)
	plural := func(n int, w string) string {
		if n == 1 {
			return "1 " + w
		}
		return fmt.Sprintf("%d %ss", n, w)
	}
	switch h := m / 60; {
	case m < 1:
		return "less than a minute"
	case h == 0:
		return plural(m, "minute")
	case m%60 == 0:
		return plural(h, "hour")
	default:
		return plural(h, "hour") + " " + plural(m%60, "minute")
	}
}

func Took(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d/time.Second))
	}
	return Lasted(d)
}

func CronFailure(label, tz string, timeout time.Duration, r proto.CronRun) string {
	loc, err := cron.Zone(tz)
	if err != nil {
		loc = time.UTC
	}
	at := fmt.Sprintf("%s failed at %s (%s)", label, time.UnixMilli(r.ScheduledAt).In(loc).Format("15:04"), tz)
	switch {
	case r.TimedOut:
		return fmt.Sprintf("%s: stopped at its %s limit.", at, Lasted(timeout))
	case r.Cause != "":
		return fmt.Sprintf("%s: %s.", at, trimDot(r.Cause))
	case r.Exit != 0:
		return fmt.Sprintf("%s: exit %d after %s.", at, r.Exit, Took(time.Duration(r.FinishedAt-r.StartedAt)*time.Millisecond))
	}
	return ""
}

func cronCause(text string) string {
	_, c, _ := strings.Cut(text, "): ")
	return trimDot(c)
}

func trimDot(s string) string {
	for len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}
