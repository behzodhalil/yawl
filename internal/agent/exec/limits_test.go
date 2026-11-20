package exec

import (
	"context"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestStartCarriesLimits(t *testing.T) {
	rt := &runtime.Fake{}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	res := d.Handle(context.Background(), cmd(t, proto.CmdStart, proto.StartArgs{Project: "a", Env: "p", Service: "api", Kind: "web", Port: 10000, CPU: 0.5, Memory: 512e6}))
	if !res.OK || rt.Started[0].CPU != 0.5 || rt.Started[0].Memory != 512e6 {
		t.Fatalf("%+v %+v", res, rt.Started)
	}
}

func TestEnsureCarriesLimits(t *testing.T) {
	rt := &runtime.Fake{}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	args, _, _ := ensureArgs(t, "")
	args.CPU, args.Memory = 1, 1e9
	if res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, args)); !res.OK {
		t.Fatalf("%+v", res)
	}
	if s := rt.Started[len(rt.Started)-1]; s.CPU != 1 || s.Memory != 1e9 {
		t.Fatalf("%+v", s)
	}
}

func TestCheckCPU(t *testing.T) {
	if got := checkCPU("api", 8, 4); got != "api asks for 8 CPUs; this server has 4." {
		t.Fatal(got)
	}
	if checkCPU("api", 0.5, 4) != "" || checkCPU("api", 0, 4) != "" {
		t.Fatal("refused a fitting limit")
	}
}

func TestReleaseAndCronCheckCPU(t *testing.T) {
	rt := &runtime.Fake{}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	a := releaseArgs()
	a.CPU = 1e6
	if res := d.Handle(context.Background(), cmd(t, proto.CmdRelease, a)); res.OK || !strings.Contains(res.Cause, "api asks for 1e+06 CPUs; this server has") || len(rt.Ran) != 0 {
		t.Fatalf("release: %+v", res)
	}
	d, rt, _, sealTo := cronSetup(t)
	j := job(sealTo, "* * * * *")
	j.CPU = 1e6
	schedule(t, d, j)
	d.CronTick(context.Background(), t10)
	d.CronWait()
	runs, _ := d.St.CronRuns("svc_1", 20)
	if len(runs) != 1 || !strings.Contains(runs[0].Cause, "report asks for 1e+06 CPUs; this server has") || len(rt.Ran) != 0 {
		t.Fatalf("cron: %+v", runs)
	}
}

func TestEnsureRecreatesForNewLimits(t *testing.T) {
	rt := &runtime.Fake{}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	args, _, _ := ensureArgs(t, "")
	d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, args))
	again := func(key string, mem int64) {
		args.Password, args.Memory = sealTo("an-existing-password"), mem
		c := cmd(t, proto.CmdEnsureDatabase, args)
		c.Key = key
		if res := d.Handle(context.Background(), c); !res.OK {
			t.Fatalf("%+v", res)
		}
	}
	again("dep_2:ensure", 0)
	if len(rt.Started) != 1 {
		t.Fatalf("unchanged limits recreated it: %d starts", len(rt.Started))
	}
	again("dep_3:ensure", 512e6)
	if len(rt.Started) != 2 || rt.Started[1].Memory != 512e6 {
		t.Fatalf("new limit: %d starts %+v", len(rt.Started), rt.Started)
	}
	again("dep_4:ensure", 0)
	if len(rt.Started) != 3 || rt.Started[2].Memory != 0 {
		t.Fatalf("removed limit: %d starts", len(rt.Started))
	}
}

func TestStartAliasesServiceName(t *testing.T) {
	rt := &runtime.Fake{}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	if res := d.Handle(context.Background(), cmd(t, proto.CmdStart, proto.StartArgs{Project: "a", Env: "p", Service: "loki", Kind: "worker"})); !res.OK {
		t.Fatalf("%+v", res)
	}
	if a := rt.Started[0].Aliases; len(a) != 1 || a[0] != "loki" {
		t.Fatalf("aliases %v", a)
	}
}
