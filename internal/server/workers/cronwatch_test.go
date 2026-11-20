package workers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
)

type cronRuns struct {
	fakeSender
	runs []proto.CronRun
}

func (c *cronRuns) Send(ctx context.Context, id string, cmd proto.Command) (proto.Result, error) {
	if cmd.Name != proto.CmdCronRuns {
		return c.fakeSender.Send(ctx, id, cmd)
	}
	b, _ := json.Marshal(proto.CronRunsData{Runs: c.runs})
	return proto.Result{Key: cmd.Key, OK: true, Data: b}, nil
}

func TestCronWatchAlerts(t *testing.T) {
	st, svc := seed(t, "cron")
	st.DB.Exec(`UPDATE services SET port = 0, health_path = '', schedule = '0 3 * * *', timezone = 'UTC', command_json = '["x"]', timeout_ms = 3600000 WHERE id = ?`, svc)
	deployOnce(t, st, svc, &fakeSender{})
	st.DB.Exec(`UPDATE deploys SET finished_at = 1000`)
	at := func(h, m int) int64 { return time.Date(2026, 10, 27, h, m, 0, 0, time.UTC).UnixMilli() }
	gw := &cronRuns{}
	w := &CronWatch{St: st, GW: gw, Now: time.Now}
	tick := func() {
		if err := w.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	gw.runs = []proto.CronRun{{ID: "r1", ScheduledAt: at(3, 0), StartedAt: at(3, 0), FinishedAt: at(3, 0) + 41_000, Exit: 1}}
	tick()
	tick()
	gw.runs = append([]proto.CronRun{{ID: "r2", ScheduledAt: at(3, 0) + 86_400_000, StartedAt: at(3, 0) + 86_400_000, FinishedAt: at(3, 0) + 86_400_000 + 5000}}, gw.runs...)
	tick()
	gw.runs = append([]proto.CronRun{{ID: "r3", ScheduledAt: at(3, 0) + 2*86_400_000, StartedAt: at(3, 0) + 2*86_400_000, FinishedAt: at(4, 0) + 2*86_400_000, Exit: -1, TimedOut: true}}, gw.runs...)
	tick()
	want := []string{
		"cron:svc_1|open|0|acme/production/api|acme/production/api failed at 03:00 (UTC): exit 1 after 41 s.",
		"cron:svc_1|close|0|acme/production/api|acme/production/api succeeded again.",
		"cron:svc_1|open|0|acme/production/api|acme/production/api failed at 03:00 (UTC): stopped at its 1 hour limit.",
	}
	if got := keyed(t, st); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
}

func TestCronWatchIgnoresRunsBeforeTheDeploy(t *testing.T) {
	st, svc := seed(t, "cron")
	st.DB.Exec(`UPDATE services SET schedule = '0 3 * * *', timezone = 'UTC', command_json = '["x"]', timeout_ms = 3600000 WHERE id = ?`, svc)
	deployOnce(t, st, svc, &fakeSender{})
	st.DB.Exec(`UPDATE deploys SET finished_at = 5000`)
	gw := &cronRuns{runs: []proto.CronRun{{ID: "old", StartedAt: 4000, FinishedAt: 4500, Exit: 1}}}
	(&CronWatch{St: st, GW: gw, Now: time.Now}).Tick(context.Background())
	if got := keyed(t, st); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestCronWatchOneMessagePerIncident(t *testing.T) {
	st, svc := seed(t, "cron")
	st.DB.Exec(`UPDATE services SET schedule = '* * * * *', timezone = 'UTC', command_json = '["x"]', timeout_ms = 3600000 WHERE id = ?`, svc)
	deployOnce(t, st, svc, &fakeSender{})
	st.DB.Exec(`UPDATE deploys SET finished_at = 1000`)
	gw := &cronRuns{}
	w := &CronWatch{St: st, GW: gw, Now: time.Now}
	for i := int64(1); i <= 3; i++ {
		gw.runs = append([]proto.CronRun{{ID: "r", ScheduledAt: i * 60_000, StartedAt: i * 60_000, FinishedAt: i*60_000 + 1000, Exit: 1}}, gw.runs...)
		w.Tick(context.Background())
	}
	if got := keyed(t, st); len(got) != 1 {
		t.Fatalf("%q", got)
	}
}

func TestCronScheduledIsWhatRuns(t *testing.T) {
	st, svc := seed(t, "cron")
	st.DB.Exec(`UPDATE services SET port = 0, health_path = '', schedule = '0 4 * * *', timezone = 'UTC', command_json = '["x"]', timeout_ms = 3600000 WHERE id = ?`, svc)
	deployOnce(t, st, svc, &fakeSender{})
	st.DB.Exec(`UPDATE services SET schedule = '0 3 * * *' WHERE id = ?`, svc)
	jobs, err := app.LiveCrons(context.Background(), st.Q)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Scheduled.Schedule != "0 4 * * *" || jobs[0].Scheduled.Timezone != "UTC" {
		t.Fatalf("%+v", jobs)
	}
}
