//go:build docker

package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/moby/moby/client"
)

func TestEnsureValkeyReal(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	project := fmt.Sprintf("t%d", time.Now().UnixNano()%1_000_000)
	args := valkeyArgs(t)
	args.Project = project
	container, volume, network, image := dbNames("valkey", project, "production", "cache", 8)
	t.Cleanup(func() {
		rt.Remove(ctx, container)
		rt.C.VolumeRemove(ctx, volume, client.VolumeRemoveOptions{Force: true})
		rt.C.NetworkRemove(ctx, network, client.NetworkRemoveOptions{})
	})
	res := d.Handle(ctx, cmd(t, proto.CmdEnsureDatabase, args))
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	var data proto.EnsureDatabaseData
	json.Unmarshal(res.Data, &data)
	pw, _ := kr.Open(data.PasswordBox)
	cli := func(args ...string) string {
		var out bytes.Buffer
		exit, stderr, err := rt.RunOnce(ctx, runtime.ContainerSpec{Image: image, Network: network,
			Cmd: append([]string{"valkey-cli", "-h", "cache", "-a", string(pw), "--no-auth-warning"}, args...)}, nil, &out)
		if err != nil || exit != 0 {
			t.Fatalf("valkey-cli %v: exit %d %s %v", args, exit, stderr, err)
		}
		return strings.TrimSpace(out.String())
	}
	if got := cli("SET", "greeting", "salom"); got != "OK" {
		t.Fatalf("SET: %q", got)
	}
	time.Sleep(1500 * time.Millisecond)
	rt.Stop(ctx, container, 10*time.Second)
	rt.Remove(ctx, container)
	args.Password = data.PasswordBox
	if res := d.Handle(ctx, cmd(t, proto.CmdEnsureDatabase, args)); !res.OK {
		t.Fatalf("again: %+v", res)
	}
	if got := cli("GET", "greeting"); got != "salom" {
		t.Fatalf("after a restart: %q", got)
	}
	if got := cli("CONFIG", "GET", "maxmemory-policy"); !strings.Contains(got, "noeviction") {
		t.Fatalf("policy: %q", got)
	}
}
