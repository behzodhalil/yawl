package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/cron"
	"github.com/behzodhalil/yawl/internal/proto"
)

type cronState struct {
	mu      sync.Mutex
	running map[string]string
	wg      sync.WaitGroup
}

const cronLines = 50

func (d *Dispatcher) cronBusy(id string) bool {
	d.cron.mu.Lock()
	defer d.cron.mu.Unlock()
	return d.cron.running[id] != ""
}

func (d *Dispatcher) CronJobs() map[string]proto.CronJob {
	jobs := map[string]proto.CronJob{}
	if b, err := os.ReadFile(d.CronFile); err == nil {
		json.Unmarshal(b, &jobs)
	}
	return jobs
}

func (d *Dispatcher) saveJobs(jobs map[string]proto.CronJob) error {
	b, _ := json.Marshal(jobs)
	if err := os.MkdirAll(filepath.Dir(d.CronFile), 0o700); err != nil {
		return err
	}
	tmp := d.CronFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.CronFile)
}

func (d *Dispatcher) cronCommand(ctx context.Context, c proto.Command) proto.Result {
	d.cron.mu.Lock()
	defer d.cron.mu.Unlock()
	if c.Name == proto.CmdCronSchedule {
		var j proto.CronJob
		if err := json.Unmarshal(c.Args, &j); err != nil {
			return bad(err)
		}
		if _, err := cron.Parse(j.Schedule); err != nil {
			return proto.Result{Cause: "bad schedule: " + err.Error()}
		}
		if _, err := cron.Zone(j.Timezone); err != nil {
			return proto.Result{Cause: "bad time zone " + j.Timezone}
		}
		jobs := d.CronJobs()
		jobs[j.ID] = j
		if err := d.saveJobs(jobs); err != nil {
			return proto.Result{Cause: err.Error()}
		}
		return proto.Result{OK: true}
	}
	var a proto.CronRemoveArgs
	if err := json.Unmarshal(c.Args, &a); err != nil {
		return bad(err)
	}
	jobs := d.CronJobs()
	switch c.Name {
	case proto.CmdCronRemove:
		delete(jobs, a.ID)
		if err := d.saveJobs(jobs); err != nil {
			return proto.Result{Cause: err.Error()}
		}
		return proto.Result{OK: true}
	case proto.CmdCronRuns:
		runs, err := d.St.CronRuns(a.ID, 20)
		if err != nil {
			return proto.Result{Cause: err.Error()}
		}
		if d.Keys != nil {
			m := d.Keys.Masker()
			for i := range runs {
				runs[i].Output, runs[i].Cause = m.Mask(runs[i].Output), m.Mask(runs[i].Cause)
			}
		}
		return ok(proto.CronRunsData{Running: d.cron.running[a.ID] != "", Runs: runs})
	default:
		j, found := jobs[a.ID]
		if !found {
			return proto.Result{Cause: "this agent has no cron job " + a.ID}
		}
		if d.cron.running[a.ID] != "" {
			return proto.Result{Cause: fmt.Sprintf("A run of %s is still going.", j.Service)}
		}
		d.startRunLocked(context.WithoutCancel(ctx), j, d.Clock.Now().Truncate(time.Second))
		return proto.Result{OK: true}
	}
}

func (d *Dispatcher) CronStart(ctx context.Context) error {
	type fin struct {
		j proto.CronJob
		r proto.CronRun
	}
	var fins []fin
	defer func() {
		for _, f := range fins {
			d.cronReport(ctx, "finish", f.j, f.r)
		}
	}()
	d.cron.mu.Lock()
	defer d.cron.mu.Unlock()
	going := map[string]bool{}
	for _, id := range d.cron.running {
		going[id] = true
	}
	stale, err := d.St.StaleCronRuns()
	if err != nil {
		return err
	}
	now := d.Clock.Now().UnixMilli()
	jobs := d.CronJobs()
	for id, runs := range stale {
		for _, r := range runs {
			if going[r.ID] {
				continue
			}
			r.FinishedAt, r.Exit, r.Cause = now, -1, "The agent restarted during this run."
			if err := d.St.FinishCronRun(r); err != nil {
				return err
			}
			if j, ok := jobs[id]; ok {
				fins = append(fins, fin{j, r})
			}
		}
	}
	left, err := d.rt.List(ctx, map[string]string{"yawl.cron": "1"})
	if err != nil {
		return err
	}
	for _, c := range left {
		if !going[c.Name] {
			d.rt.Remove(ctx, c.ID)
		}
	}
	return nil
}

func (d *Dispatcher) CronLoop(ctx context.Context) {
	if err := d.CronStart(ctx); err != nil {
		slog.Warn("cron: start", "err", err)
	}
	last := d.Clock.Now().Truncate(time.Minute)
	for {
		now := d.Clock.Now()
		next := now.Truncate(time.Minute).Add(time.Minute)
		select {
		case <-ctx.Done():
			return
		case <-d.Clock.After(next.Sub(now)):
		}
		for _, m := range dueMinutes(last, d.Clock.Now()) {
			d.CronTick(ctx, m)
			last = m
		}
	}
}

func dueMinutes(last, now time.Time) []time.Time {
	var out []time.Time
	for m := last.Truncate(time.Minute).Add(time.Minute); !m.After(now); m = m.Add(time.Minute) {
		out = append(out, m)
	}
	if len(out) > 10 {
		out = out[len(out)-10:]
	}
	return out
}

func (d *Dispatcher) CronTick(ctx context.Context, m time.Time) {
	type skip struct {
		j proto.CronJob
		r proto.CronRun
	}
	var skips []skip
	defer func() {
		for _, s := range skips {
			go d.cronReport(ctx, "skip", s.j, s.r)
		}
	}()
	d.cron.mu.Lock()
	defer d.cron.mu.Unlock()
	for _, j := range d.CronJobs() {
		s, err := cron.Parse(j.Schedule)
		loc, zerr := cron.Zone(j.Timezone)
		if err != nil || zerr != nil || !s.Matches(m.In(loc)) {
			continue
		}
		if d.cron.running[j.ID] != "" {
			r := proto.CronRun{ID: runID(j, m) + "-skip", ScheduledAt: m.UnixMilli(), StartedAt: 0, FinishedAt: d.Clock.Now().UnixMilli(),
				Skipped: true, Cause: "Skipped: the run before was still going."}
			d.St.AddCronRun(j.ID, r)
			skips = append(skips, skip{j, r})
			continue
		}
		d.startRunLocked(ctx, j, m)
	}
}

func (d *Dispatcher) CronWait() { d.cron.wg.Wait() }

func runID(j proto.CronJob, at time.Time) string {
	return fmt.Sprintf("yawl-%s-%s-%s-cron-%d", j.Project, j.Env, j.Service, at.Unix())
}

func (d *Dispatcher) cronReport(ctx context.Context, ev string, j proto.CronJob, r proto.CronRun) {
	if d.Report != nil {
		d.Report(ctx, ev, j, r)
	}
}

func (d *Dispatcher) startRunLocked(ctx context.Context, j proto.CronJob, at time.Time) {
	if d.cron.running == nil {
		d.cron.running = map[string]string{}
	}
	r := proto.CronRun{ID: runID(j, at), ScheduledAt: at.UnixMilli(), StartedAt: d.Clock.Now().UnixMilli()}
	d.cron.running[j.ID] = r.ID
	if err := d.St.AddCronRun(j.ID, r); err != nil {
		slog.Warn("cron: record", "err", err)
	}
	d.cron.wg.Add(1)
	go func() {
		defer d.cron.wg.Done()
		r, done := d.runCron(ctx, j, r)
		d.cron.mu.Lock()
		delete(d.cron.running, j.ID)
		d.cron.mu.Unlock()
		if done {
			d.cronReport(ctx, "finish", j, r)
		}
	}()
}

func (d *Dispatcher) runCron(ctx context.Context, j proto.CronJob, r proto.CronRun) (proto.CronRun, bool) {
	name := r.ID
	d.cronReport(ctx, "start", j, r)
	finish := func() proto.CronRun {
		r.FinishedAt = d.Clock.Now().UnixMilli()
		if d.Keys != nil {
			m := d.Keys.Masker()
			r.Output, r.Cause = m.Mask(r.Output), m.Mask(r.Cause)
		}
		if err := d.St.FinishCronRun(r); err != nil {
			slog.Warn("cron: record", "err", err)
		}
		return r
	}
	env, cause := d.env(j.Vars)
	if cause != "" {
		r.Exit, r.Cause = -1, cause
		return finish(), true
	}
	network := "yawl-" + j.Project + "-" + j.Env
	if err := d.rt.EnsureNetwork(ctx, network); err != nil {
		r.Exit, r.Cause = -1, err.Error()
		return finish(), true
	}
	rctx, cancel := context.WithTimeout(ctx, j.Timeout)
	defer cancel()
	out := &tail{max: 64 << 10}
	if cause := yawlNetworks(j.Networks); cause != "" {
		r.Exit, r.Cause = -1, cause
		return finish(), true
	}
	if cause := checkCPU(j.Service, j.CPU, goruntime.NumCPU()); cause != "" {
		r.Exit, r.Cause = -1, cause
		return finish(), true
	}
	exit, stderr, err := d.rt.RunOnce(rctx, runtime.ContainerSpec{Name: name, Image: j.Image, Network: network, Env: env, Cmd: j.Cmd, Networks: j.Networks, CPU: j.CPU, Memory: j.Memory,
		Labels: map[string]string{"yawl.project": j.Project, "yawl.env": j.Env, "yawl.service": j.Service, "yawl.cron": "1"}}, nil, out)
	text := strings.TrimRight(out.String(), "\n")
	if stderr != "" {
		text = strings.TrimRight(text+"\n"+stderr, "\n")
	}
	if lines := strings.Split(text, "\n"); len(lines) > cronLines {
		text = strings.Join(lines[len(lines)-cronLines:], "\n")
	}
	if ctx.Err() != nil {
		return r, false
	}
	r.Output, r.Exit = text, exit
	switch {
	case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
		r.Exit, r.TimedOut = -1, true
	case err != nil:
		r.Exit, r.Cause = -1, err.Error()
	}
	return finish(), true
}
