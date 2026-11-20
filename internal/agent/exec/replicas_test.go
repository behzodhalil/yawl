package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func startCopies(t *testing.T, rt *runtime.Fake, n int) (string, proto.Result) {
	t.Helper()
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	res := d.Handle(context.Background(), cmd(t, proto.CmdStart, proto.StartArgs{
		Project: "acme", Env: "production", Service: "api", Deploy: "dep_1", Image: "img", Kind: "web", Port: 10000, Replicas: n}))
	var data proto.StartData
	json.Unmarshal(res.Data, &data)
	return data.Container, res
}

func TestStartReplicas(t *testing.T) {
	rt := &runtime.Fake{}
	first, res := startCopies(t, rt, 3)
	if !res.OK || len(rt.Started) != 3 || first != "c1" {
		t.Fatalf("%+v %d %q", res, len(rt.Started), first)
	}
	for i, s := range rt.Started {
		name := "yawl-acme-production-api-dep_1"
		if i > 0 {
			name += fmt.Sprintf("-%d", i+1)
		}
		if s.Name != name || s.Labels["yawl.deploy"] != "dep_1" || s.Labels["yawl.replica"] != fmt.Sprint(i+1) || !slices.Equal(s.Aliases, []string{"api"}) {
			t.Fatalf("copy %d: %+v", i+1, s)
		}
	}
	rt1 := &runtime.Fake{}
	startCopies(t, rt1, 0)
	if len(rt1.Started) != 1 || rt1.Started[0].Name != "yawl-acme-production-api-dep_1" {
		t.Fatalf("%+v", rt1.Started)
	}
}

func TestStartReplicasFailureCleansUp(t *testing.T) {
	rt := &runtime.Fake{}
	rt.StartHook = func(s runtime.ContainerSpec) error {
		if strings.HasSuffix(s.Name, "-3") {
			return errors.New("port in use")
		}
		return nil
	}
	_, res := startCopies(t, rt, 3)
	if res.OK || !strings.Contains(res.Cause, "port in use") {
		t.Fatalf("%+v", res)
	}
	for _, id := range []string{"c1", "c2"} {
		if _, err := rt.Inspect(context.Background(), id); err == nil {
			t.Errorf("%s left behind", id)
		}
	}
}

func TestStartReplicasFailureRemovesHalfMade(t *testing.T) {
	rt := &runtime.Fake{FailAfterCreate: func(s runtime.ContainerSpec) error {
		if strings.HasSuffix(s.Name, "-3") {
			return errors.New("copy files: no space")
		}
		return nil
	}}
	if _, res := startCopies(t, rt, 3); res.OK {
		t.Fatalf("%+v", res)
	}
	for _, id := range []string{"c1", "c2", "c3"} {
		if _, err := rt.Inspect(context.Background(), id); err == nil {
			t.Errorf("%s left behind", id)
		}
	}
}

func TestReplicaGroup(t *testing.T) {
	rt, px := &runtime.Fake{}, &fakeProxy{}
	first, _ := startCopies(t, rt, 2)
	rt.Set("c1", runtime.State{Running: true, IP: netip.MustParseAddr("172.18.0.5"), Labels: rt.Started[0].Labels})
	rt.Set("c2", runtime.State{Running: true, IP: netip.MustParseAddr("172.18.0.6"), Labels: rt.Started[1].Labels})
	d := NewDispatcher(rt, px, &fakeBuilder{})
	d.Clock = fakeClock()
	ctx := context.Background()
	if res := d.Handle(ctx, cmd(t, proto.CmdSwitchRoute, proto.SwitchArgs{Hosts: []string{"api.example"}, Container: first, Port: 10000})); !res.OK ||
		px.routes["api.example"] != "172.18.0.5:10000,172.18.0.6:10000" {
		t.Fatalf("%+v %v", res, px.routes)
	}
	d.Get = func(url string) (int, error) {
		if strings.Contains(url, "172.18.0.6") {
			return 502, nil
		}
		return 200, nil
	}
	if res := d.Handle(ctx, cmd(t, proto.CmdProbe, proto.ProbeArgs{Container: first, Kind: "web", Path: "/", Port: 10000})); res.OK || !strings.HasPrefix(res.Cause, "copy 2: ") {
		t.Fatalf("probe %+v", res)
	}
	if res := d.Handle(ctx, cmd(t, proto.CmdDrain, proto.DrainArgs{Container: first})); !res.OK || !slices.Contains(rt.Stopped, "c1") || !slices.Contains(rt.Stopped, "c2") {
		t.Fatalf("drain %+v %v", res, rt.Stopped)
	}
}

func TestUpstreamsSkipCopyWithoutAddress(t *testing.T) {
	rt := &runtime.Fake{}
	first, _ := startCopies(t, rt, 2)
	rt.Set("c1", runtime.State{Running: true, IP: netip.MustParseAddr("172.18.0.5"), Labels: rt.Started[0].Labels})
	rt.Set("c2", runtime.State{Restarting: true, Labels: rt.Started[1].Labels})
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	ups, err := d.upstreams(context.Background(), first, 80)
	if err != nil || !slices.Equal(ups, []string{"172.18.0.5:80"}) {
		t.Fatalf("%v %v", ups, err)
	}
}

func TestUpstreamsOnlyCopyDown(t *testing.T) {
	rt := &runtime.Fake{}
	first, _ := startCopies(t, rt, 1)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	for _, st := range []runtime.State{{Restarting: true}, {Running: false, IP: netip.MustParseAddr("172.18.0.5")}} {
		st.Labels = rt.Started[0].Labels
		rt.Set("c1", st)
		if ups, err := d.upstreams(context.Background(), first, 80); err == nil || err.Error() != "no copy of "+first+" has an address" {
			t.Fatalf("%+v: %v %v", st, ups, err)
		}
	}
}

func TestEachCopyParallelFirstLast(t *testing.T) {
	rt := &runtime.Fake{}
	first, _ := startCopies(t, rt, 4)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	var mu sync.Mutex
	var order []string
	start := time.Now()
	res := d.eachCopy(context.Background(), first, func(id string) proto.Result {
		time.Sleep(200 * time.Millisecond)
		mu.Lock()
		order = append(order, id)
		mu.Unlock()
		return proto.Result{OK: true}
	})
	if !res.OK || time.Since(start) > 600*time.Millisecond || len(order) != 4 || order[3] != first {
		t.Fatalf("%+v %v %v", res, time.Since(start), order)
	}
	order = nil
	res = d.eachCopy(context.Background(), first, func(id string) proto.Result {
		mu.Lock()
		order = append(order, id)
		mu.Unlock()
		if id == "c3" {
			return proto.Result{Cause: "boom"}
		}
		return proto.Result{OK: true}
	})
	if res.OK || res.Cause != "copy 3: boom" || slices.Contains(order, first) {
		t.Fatalf("%+v %v", res, order)
	}
}
