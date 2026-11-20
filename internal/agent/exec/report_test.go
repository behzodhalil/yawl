package exec

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestReportListsRunning(t *testing.T) {
	rt := &runtime.Fake{}
	ctx := context.Background()
	app := map[string]string{"yawl.project": "acme", "yawl.env": "production", "yawl.service": "api", "yawl.deploy": "dep_81"}
	rt.Start(ctx, runtime.ContainerSpec{Name: "yawl-acme-production-api-dep_81", Image: "yawl/acme-production-api:dep_81", Labels: app})
	old := map[string]string{"yawl.project": "acme", "yawl.env": "production", "yawl.service": "api", "yawl.deploy": "dep_79"}
	id, _ := rt.Start(ctx, runtime.ContainerSpec{Name: "yawl-acme-production-api-dep_79", Image: "yawl/acme-production-api:dep_79", Labels: old})
	rt.Stop(ctx, id, 0)
	rt.Set(id, runtime.State{Running: false})
	rt.Start(ctx, runtime.ContainerSpec{Name: "yawl-acme-production-db", Image: "postgres:17", Labels: map[string]string{"yawl.project": "acme", "yawl.env": "production", "yawl.service": "db"}})
	rt.Start(ctx, runtime.ContainerSpec{Name: "caddy", Image: "caddy:2"})

	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	res := d.Handle(ctx, proto.Command{Name: proto.CmdReport, Args: json.RawMessage(`{}`)})
	var data proto.ReportData
	json.Unmarshal(res.Data, &data)
	want := proto.Running{Project: "acme", Env: "production", Service: "api", Deploy: "dep_81", Image: "yawl/acme-production-api:dep_81", Container: "yawl-acme-production-api-dep_81"}
	if res.Cause != "" || len(data.Running) != 1 || data.Running[0] != want {
		t.Fatalf("%+v %+v", res, data)
	}
	if !proto.Unrecorded(proto.CmdReport) {
		t.Fatal("Report would be answered from the store after the first time")
	}
}

func TestReportOneContainerPerService(t *testing.T) {
	rt := &runtime.Fake{}
	ctx := context.Background()
	for _, dep := range []string{"dep_1", "dep_2"} {
		rt.Start(ctx, runtime.ContainerSpec{Name: "yawl-acme-production-api-" + dep, Image: "yawl/acme-production-api:" + dep,
			Labels: map[string]string{"yawl.project": "acme", "yawl.env": "production", "yawl.service": "api", "yawl.deploy": dep}})
	}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	res := d.Handle(ctx, proto.Command{Name: proto.CmdReport, Args: json.RawMessage(`{}`)})
	var data proto.ReportData
	json.Unmarshal(res.Data, &data)
	if len(data.Running) != 1 || data.Running[0].Deploy != "dep_2" {
		t.Fatalf("%+v", data.Running)
	}
}
