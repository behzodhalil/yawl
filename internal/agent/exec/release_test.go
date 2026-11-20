package exec

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func releaseArgs(vars ...proto.SealedVar) proto.ReleaseArgs {
	return proto.ReleaseArgs{Project: "acme", Env: "production", Service: "api", Image: "yawl/acme-production-api:dep_1",
		Kind: "web", Port: 10000, Cmd: []string{"/app/pgnote", "migrate"}, Vars: vars, Timeout: time.Minute}
}

func TestReleaseExitCode(t *testing.T) {
	rt := &runtime.Fake{OnRun: func(s runtime.ContainerSpec, _ []byte) (string, int, string, error) { return "boom\n", 3, "", nil }}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	res := d.Handle(context.Background(), cmd(t, proto.CmdRelease, releaseArgs()))
	if res.OK || res.Cause != "Release command exited with code 3: boom" {
		t.Fatalf("%+v", res)
	}
	s := rt.Ran[0].Spec
	if s.Image != "yawl/acme-production-api:dep_1" || s.Network != "yawl-acme-production" || !slices.Equal(s.Cmd, []string{"/app/pgnote", "migrate"}) {
		t.Fatalf("release container %+v", s)
	}
}

func TestReleaseEnvAndMask(t *testing.T) {
	const secret = "stripe-secret-987654"
	rt := &runtime.Fake{OnRun: func(s runtime.ContainerSpec, _ []byte) (string, int, string, error) {
		return "using " + secret + "\nmigrated\n", 0, "", nil
	}}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdRelease, releaseArgs(proto.SealedVar{Key: "STRIPE", Box: sealTo(secret)})))
	if !res.OK || strings.Contains(res.Output, secret) || !strings.Contains(res.Output, "using ••••") {
		t.Fatalf("%+v", res)
	}
	env := rt.Ran[0].Spec.Env
	if !slices.Contains(env, "STRIPE="+secret) || !slices.Contains(env, "PORT=10000") {
		t.Fatalf("env %v", env)
	}
}

func TestReleaseTimeout(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	rt := &runtime.Fake{OnRun: func(s runtime.ContainerSpec, _ []byte) (string, int, string, error) { <-block; return "", 0, "", nil }}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	a := releaseArgs()
	a.Timeout = 50 * time.Millisecond
	res := d.Handle(context.Background(), cmd(t, proto.CmdRelease, a))
	if res.OK || res.Cause != "Release command did not finish in 50ms." {
		t.Fatalf("%+v", res)
	}
}

func TestReleaseAdoptsOrphan(t *testing.T) {
	ran := 0
	rt := &runtime.Fake{
		OnRun:   func(s runtime.ContainerSpec, _ []byte) (string, int, string, error) { ran++; return "", 0, "", nil },
		OnStart: func(string, runtime.ContainerSpec) runtime.State { return runtime.State{Running: false, ExitCode: 0} },
	}
	name := "yawl-acme-production-api-release-dep_1"
	rt.Start(context.Background(), runtime.ContainerSpec{Name: name})
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	a := releaseArgs()
	a.Deploy = "dep_1"
	res := d.Handle(context.Background(), cmd(t, proto.CmdRelease, a))
	if !res.OK || ran != 0 {
		t.Fatalf("%+v, ran %d more", res, ran)
	}
	if _, err := rt.Inspect(context.Background(), name); err == nil {
		t.Fatal("the orphan was not removed")
	}
	d.Handle(context.Background(), cmd(t, proto.CmdRelease, a))
	if ran != 1 || rt.Ran[0].Spec.Name != name {
		t.Fatalf("ran %d, %+v", ran, rt.Ran)
	}
}

func TestReleaseMountsVolumes(t *testing.T) {
	rt := &runtime.Fake{}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	a := releaseArgs()
	a.Volumes = []proto.Volume{{Name: "data", Path: "/app/data"}}
	if res := d.Handle(context.Background(), cmd(t, proto.CmdRelease, a)); !res.OK {
		t.Fatalf("%+v", res)
	}
	if m := rt.Ran[0].Spec.Mounts; len(m) != 1 || m[0] != (runtime.Mount{Source: "yawl_acme_production_api_data", Target: "/app/data", Volume: true}) {
		t.Fatalf("%+v", m)
	}
}
