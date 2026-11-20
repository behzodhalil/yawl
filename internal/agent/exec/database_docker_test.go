//go:build docker

package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/moby/moby/client"
)

func TestEnsurePostgresReal(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	project := fmt.Sprintf("t%d", time.Now().UnixNano()%1_000_000)
	args, _, _ := ensureArgs(t, "")
	args.Project = project
	container, volume, network, _ := dbNames("", project, "production", "acme-db", 17)
	t.Cleanup(func() {
		rt.Remove(ctx, container)
		rt.C.VolumeRemove(ctx, volume, client.VolumeRemoveOptions{Force: true})
		rt.C.NetworkRemove(ctx, network, client.NetworkRemoveOptions{}) // one per run: do not use up the address pools
	})
	res := d.Handle(ctx, cmd(t, proto.CmdEnsureDatabase, args))
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	var data proto.EnsureDatabaseData
	json.Unmarshal(res.Data, &data)
	url, _ := kr.Open(data.URLBox)
	exit, stderr, err := rt.RunOnce(ctx, runtime.ContainerSpec{Image: "postgres:17", Network: "yawl-" + project + "-production",
		Cmd: []string{"psql", string(url), "-c", "select 1"}}, nil, nil)
	if err != nil || exit != 0 {
		t.Fatalf("psql exit %d: %s %v", exit, stderr, err)
	}
}
