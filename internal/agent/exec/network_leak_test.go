package exec

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestTrashCommandsLeaveEnvNetwork(t *testing.T) {
	rt := &runtime.Fake{}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Clock = fakeClock()
	ctx := context.Background()
	res := d.Handle(ctx, cmd(t, proto.CmdStart, proto.StartArgs{Project: "cells", Env: "churn1", Service: "panel", Deploy: "dep_1", Image: "x", Kind: "web", Port: 7000}))
	if !res.OK {
		t.Fatalf("start %+v", res)
	}
	c := rt.Started[0].Name
	for _, c := range []proto.Command{
		cmd(t, proto.CmdStop, proto.StopArgs{Container: c}),
		cmd(t, proto.CmdDrain, proto.DrainArgs{Container: c}),
		cmd(t, proto.CmdRemoveVolume, proto.RemoveVolumeArgs{Name: "yawl-cells-churn1-panel-data"}),
	} {
		if res := d.Handle(ctx, c); !res.OK {
			t.Fatalf("%s %+v", c.Name, res)
		}
	}
	if !slices.Contains(rt.Networks, "yawl-cells-churn1") {
		t.Fatalf("networks %q", rt.Networks)
	}
	var nets []string
	for m := range reflect.TypeFor[runtime.Runtime]().Methods() {
		if strings.Contains(m.Name, "Network") {
			nets = append(nets, m.Name)
		}
	}
	if !slices.Equal(nets, []string{"EnsureNetwork", "NetworkInfo"}) {
		t.Fatalf("Runtime's network methods %q", nets)
	}
}
