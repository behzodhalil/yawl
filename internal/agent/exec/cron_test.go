package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/agent/state"
	"github.com/behzodhalil/yawl/internal/proto"
)

type reports struct {
	mu  sync.Mutex
	got []string
}

func (r *reports) add(_ context.Context, ev string, j proto.CronJob, run proto.CronRun) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, fmt.Sprintf("%s %s %d", ev, j.ID, run.Exit))
}

func (r *reports) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

func cronSetup(t *testing.T) (*Dispatcher, *runtime.Fake, *reports, func(string) string) {
	t.Helper()
	rt := &runtime.Fake{}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	kr, sealTo := keyring(t)
	d.Keys = kr
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	d.St, d.CronFile = st, filepath.Join(dir, "cron.json")
	rep := &reports{}
	d.Report = rep.add
	return d, rt, rep, sealTo
}

func job(sealTo func(string) string, schedule string) proto.CronJob {
	return proto.CronJob{ID: "svc_1", Project: "acme", Env: "production", Service: "report", Image: "img:1", Schedule: schedule,
		Timezone: "UTC", Cmd: []string{"node", "report.js"}, Vars: []proto.SealedVar{{Key: "KEY", Box: sealTo("value")}},
		Timeout: time.Hour, Label: "acme/production/report"}
}

func schedule(t *testing.T, d *Dispatcher, j proto.CronJob) {
	t.Helper()
	c, _ := proto.NewCommand("k", proto.CmdCronSchedule, j)
	if res := d.Handle(context.Background(), c); !res.OK {
		t.Fatalf("%+v", res)
	}
}

var t10 = time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)

func TestCronScheduleCommandPersists(t *testing.T) {
	d, _, _, sealTo := cronSetup(t)
	a, b := job(sealTo, "0 3 * * *"), job(sealTo, "0 4 * * *")
	b.ID = "svc_2"
	schedule(t, d, a)
	schedule(t, d, b)
	c, _ := proto.NewCommand("k2", proto.CmdCronRemove, proto.CronRemoveArgs{ID: "svc_1"})
	if res := d.Handle(context.Background(), c); !res.OK {
		t.Fatalf("%+v", res)
	}
	fresh := &Dispatcher{CronFile: d.CronFile}
	jobs := fresh.CronJobs()
	if len(jobs) != 1 || jobs["svc_2"].Schedule != "0 4 * * *" {
		t.Fatalf("%+v", jobs)
	}
}

func TestCronRunsOnSchedule(t *testing.T) {
	d, rt, rep, sealTo := cronSetup(t)
	schedule(t, d, job(sealTo, "*/2 * * * *"))
	for i := 0; i < 5; i++ {
		d.CronTick(context.Background(), t10.Add(time.Duration(i)*time.Minute))
		d.CronWait()
	}
	if len(rt.Ran) != 3 {
		t.Fatalf("%d runs", len(rt.Ran))
	}
	s := rt.Ran[1].Spec
	if s.Name != fmt.Sprintf("yawl-acme-production-report-cron-%d", t10.Add(2*time.Minute).Unix()) || s.Image != "img:1" ||
		!slices.Contains(s.Env, "KEY=value") || s.Network != "yawl-acme-production" || s.Labels["yawl.cron"] != "1" || !slices.Equal(s.Cmd, []string{"node", "report.js"}) {
		t.Fatalf("spec %+v", s)
	}
	runs, _ := d.St.CronRuns("svc_1", 20)
	if len(runs) != 3 || runs[0].ScheduledAt != t10.Add(4*time.Minute).UnixMilli() || runs[0].FinishedAt == 0 {
		t.Fatalf("runs %+v", runs)
	}
	if got := rep.list(); len(got) != 6 || got[0] != "start svc_1 0" || got[1] != "finish svc_1 0" {
		t.Fatalf("reports %v", got)
	}
}

func TestCronSkipsWhileRunning(t *testing.T) {
	d, rt, rep, sealTo := cronSetup(t)
	release := make(chan struct{})
	rt.OnRun = func(runtime.ContainerSpec, []byte) (string, int, string, error) { <-release; return "", 0, "", nil }
	schedule(t, d, job(sealTo, "* * * * *"))
	d.CronTick(context.Background(), t10)
	waitRunning(t, d)
	d.CronTick(context.Background(), t10.Add(time.Minute))
	close(release)
	d.CronWait()
	if len(rt.Ran) != 1 {
		t.Fatalf("%d containers", len(rt.Ran))
	}
	runs, _ := d.St.CronRuns("svc_1", 20)
	if len(runs) != 2 || !runs[0].Skipped || runs[0].Cause != "Skipped: the run before was still going." || runs[1].Skipped {
		t.Fatalf("runs %+v", runs)
	}
	if got := rep.list(); !slices.Contains(got, "skip svc_1 0") {
		t.Fatalf("reports %v", got)
	}
}

func waitRunning(t *testing.T, d *Dispatcher) {
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if d.cronBusy("svc_1") {
			return
		}
	}
	t.Fatal("never started")
}

func TestCronTimeout(t *testing.T) {
	d, rt, _, sealTo := cronSetup(t)
	rt.OnRun = func(runtime.ContainerSpec, []byte) (string, int, string, error) {
		time.Sleep(time.Second)
		return "", 0, "", nil
	}
	j := job(sealTo, "* * * * *")
	j.Timeout = 50 * time.Millisecond
	schedule(t, d, j)
	d.CronTick(context.Background(), t10)
	d.CronWait()
	runs, _ := d.St.CronRuns("svc_1", 20)
	if len(runs) != 1 || !runs[0].TimedOut || runs[0].Exit != -1 {
		t.Fatalf("runs %+v", runs)
	}
}

func TestCronKeepsLastLines(t *testing.T) {
	d, rt, _, sealTo := cronSetup(t)
	var out strings.Builder
	for i := 1; i <= 80; i++ {
		fmt.Fprintf(&out, "line %d\n", i)
	}
	rt.OnRun = func(runtime.ContainerSpec, []byte) (string, int, string, error) { return out.String(), 3, "", nil }
	schedule(t, d, job(sealTo, "* * * * *"))
	d.CronTick(context.Background(), t10)
	d.CronWait()
	runs, _ := d.St.CronRuns("svc_1", 20)
	lines := strings.Split(runs[0].Output, "\n")
	if runs[0].Exit != 3 || len(lines) != 50 || lines[0] != "line 31" || lines[49] != "line 80" {
		t.Fatalf("exit %d, %d lines, first %q last %q", runs[0].Exit, len(lines), lines[0], lines[len(lines)-1])
	}
}

func TestCronKeepsTwentyRuns(t *testing.T) {
	d, _, _, sealTo := cronSetup(t)
	schedule(t, d, job(sealTo, "* * * * *"))
	for i := 0; i < 25; i++ {
		d.CronTick(context.Background(), t10.Add(time.Duration(i)*time.Minute))
		d.CronWait()
	}
	if runs, _ := d.St.CronRuns("svc_1", 100); len(runs) != 20 {
		t.Fatalf("%d runs kept", len(runs))
	}
}

func TestCronRestartFinishesStale(t *testing.T) {
	d, rt, _, sealTo := cronSetup(t)
	schedule(t, d, job(sealTo, "* * * * *"))
	if err := d.St.AddCronRun("svc_1", proto.CronRun{ID: "run_old", ScheduledAt: 1, StartedAt: 2}); err != nil {
		t.Fatal(err)
	}
	id, _ := rt.Start(context.Background(), runtime.ContainerSpec{Name: "yawl-acme-production-report-cron-1", Labels: map[string]string{"yawl.cron": "1"}})
	if err := d.CronStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, _ := d.St.CronRuns("svc_1", 20)
	if len(runs) != 1 || runs[0].FinishedAt == 0 || runs[0].Exit != -1 || runs[0].Cause != "The agent restarted during this run." {
		t.Fatalf("runs %+v", runs)
	}
	if _, err := rt.Inspect(context.Background(), id); err == nil {
		t.Fatal("container kept")
	}
	d.CronTick(context.Background(), t10)
	d.CronWait()
	if len(rt.Ran) != 1 {
		t.Fatalf("%d runs", len(rt.Ran))
	}
}

func TestCronStartKeepsRunGoing(t *testing.T) {
	d, rt, _, sealTo := cronSetup(t)
	release := make(chan struct{})
	rt.OnRun = func(runtime.ContainerSpec, []byte) (string, int, string, error) { <-release; return "", 0, "", nil }
	schedule(t, d, job(sealTo, "0 3 * * *"))
	if err := d.St.AddCronRun("svc_1", proto.CronRun{ID: "run_old", ScheduledAt: 1, StartedAt: 2}); err != nil {
		t.Fatal(err)
	}
	c, _ := proto.NewCommand("n", proto.CmdCronRunNow, proto.CronRemoveArgs{ID: "svc_1"})
	if res := d.Handle(context.Background(), c); !res.OK {
		t.Fatalf("%+v", res)
	}
	waitRunning(t, d)
	going := d.cron.running["svc_1"]
	id, _ := rt.Start(context.Background(), runtime.ContainerSpec{Name: going, Labels: map[string]string{"yawl.cron": "1"}})
	if err := d.CronStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, _ := d.St.CronRuns("svc_1", 20)
	for _, r := range runs {
		if (r.ID == going) != (r.FinishedAt == 0) {
			t.Fatalf("runs %+v", runs)
		}
	}
	if _, err := rt.Inspect(context.Background(), id); err != nil {
		t.Fatal("the going run's container was removed")
	}
	close(release)
	d.CronWait()
}

func TestCronRunNowBusy(t *testing.T) {
	d, rt, _, sealTo := cronSetup(t)
	release := make(chan struct{})
	rt.OnRun = func(runtime.ContainerSpec, []byte) (string, int, string, error) { <-release; return "", 0, "", nil }
	schedule(t, d, job(sealTo, "0 3 * * *"))
	now := func() proto.Result {
		c, _ := proto.NewCommand("n", proto.CmdCronRunNow, proto.CronRemoveArgs{ID: "svc_1"})
		return d.Handle(context.Background(), c)
	}
	if res := now(); !res.OK {
		t.Fatalf("%+v", res)
	}
	waitRunning(t, d)
	if res := now(); res.OK || res.Cause != "A run of report is still going." {
		t.Fatalf("%+v", res)
	}
	c, _ := proto.NewCommand("r", proto.CmdCronRuns, proto.CronRemoveArgs{ID: "svc_1"})
	if res := d.Handle(context.Background(), c); !res.OK || !strings.Contains(string(res.Data), `"Running":true`) {
		t.Fatalf("%+v", res)
	}
	close(release)
	d.CronWait()
	if !proto.Unrecorded(proto.CmdCronRuns) || !proto.Unrecorded(proto.CmdCronRunNow) {
		t.Fatal("CronRuns and CronRunNow must be unrecorded")
	}
}

func TestCronLongRunKeptPastSkips(t *testing.T) {
	d, rt, _, sealTo := cronSetup(t)
	release := make(chan struct{})
	rt.OnRun = func(runtime.ContainerSpec, []byte) (string, int, string, error) { <-release; return "", 7, "", nil }
	schedule(t, d, job(sealTo, "* * * * *"))
	d.CronTick(context.Background(), t10)
	waitRunning(t, d)
	for i := 1; i <= 25; i++ {
		d.CronTick(context.Background(), t10.Add(time.Duration(i)*time.Minute))
	}
	close(release)
	d.CronWait()
	runs, _ := d.St.CronRuns("svc_1", 100)
	found := false
	for _, r := range runs {
		if !r.Skipped && r.Exit == 7 && r.FinishedAt > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("the long run is gone: %d rows", len(runs))
	}
}

func TestCronReportsOutsideTheLock(t *testing.T) {
	d, rt, _, sealTo := cronSetup(t)
	release := make(chan struct{})
	rt.OnRun = func(runtime.ContainerSpec, []byte) (string, int, string, error) { <-release; return "", 0, "", nil }
	slow := make(chan struct{})
	d.Report = func(_ context.Context, ev string, _ proto.CronJob, _ proto.CronRun) {
		if ev == "skip" {
			<-slow
		}
	}
	schedule(t, d, job(sealTo, "* * * * *"))
	d.CronTick(context.Background(), t10)
	waitRunning(t, d)
	go d.CronTick(context.Background(), t10.Add(time.Minute))
	time.Sleep(50 * time.Millisecond)
	done := make(chan proto.Result, 1)
	go func() {
		c, _ := proto.NewCommand("r", proto.CmdCronRuns, proto.CronRemoveArgs{ID: "svc_1"})
		done <- d.Handle(context.Background(), c)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("CronRuns waited for a report to the watcher")
	}
	close(slow)
	close(release)
	d.CronWait()
}

func TestCronDueMinutes(t *testing.T) {
	got := dueMinutes(t10, t10.Add(3*time.Minute+20*time.Second))
	if len(got) != 3 || !got[0].Equal(t10.Add(time.Minute)) || !got[2].Equal(t10.Add(3*time.Minute)) {
		t.Fatalf("%v", got)
	}
	if got := dueMinutes(t10, t10.Add(-5*time.Minute)); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if got := dueMinutes(t10, t10.Add(30*time.Minute)); len(got) != 10 {
		t.Fatalf("%d", len(got))
	}
}

func TestCronStopMidRun(t *testing.T) {
	d, rt, rep, sealTo := cronSetup(t)
	rt.OnRun = func(runtime.ContainerSpec, []byte) (string, int, string, error) {
		time.Sleep(time.Second)
		return "", 0, "", nil
	}
	schedule(t, d, job(sealTo, "* * * * *"))
	ctx, cancel := context.WithCancel(context.Background())
	d.CronTick(ctx, t10)
	waitRunning(t, d)
	cancel()
	d.CronWait()
	runs, _ := d.St.CronRuns("svc_1", 20)
	if runs[0].FinishedAt != 0 {
		t.Fatalf("finished on stop: %+v", runs[0])
	}
	if err := d.CronStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, _ = d.St.CronRuns("svc_1", 20)
	if runs[0].Cause != "The agent restarted during this run." {
		t.Fatalf("%+v", runs[0])
	}
	if got := rep.list(); got[len(got)-1] != "finish svc_1 -1" {
		t.Fatalf("reports %v", got)
	}
}

func TestCronRunsDataMasksSecrets(t *testing.T) {
	d, rt, _, sealTo := cronSetup(t)
	j := job(sealTo, "* * * * *")
	j.Vars = []proto.SealedVar{{Key: "API_TOKEN", Box: sealTo("s3cr3t-token-42")}}
	rt.OnRun = func(runtime.ContainerSpec, []byte) (string, int, string, error) {
		return "API_TOKEN=s3cr3t-token-42\n", 0, "", nil
	}
	schedule(t, d, j)
	d.CronTick(context.Background(), t10)
	d.CronWait()
	stored, _ := d.St.CronRuns("svc_1", 1)
	if len(stored) != 1 || strings.Contains(stored[0].Output, "s3cr3t-token-42") {
		t.Fatalf("stored %+v", stored)
	}
	old := stored[0]
	old.Output = "API_TOKEN=s3cr3t-token-42"
	d.St.FinishCronRun(old)
	c, _ := proto.NewCommand("k2", proto.CmdCronRuns, proto.CronRemoveArgs{ID: "svc_1"})
	res := d.Handle(context.Background(), c)
	var data proto.CronRunsData
	json.Unmarshal(res.Data, &data)
	if !res.OK || len(data.Runs) != 1 || strings.Contains(data.Runs[0].Output, "s3cr3t-token-42") || !strings.HasPrefix(data.Runs[0].Output, "API_TOKEN=") {
		t.Fatalf("%+v %s", res, res.Data)
	}
}
