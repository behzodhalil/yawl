//go:build docker

package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/moby/moby/client"
)

func psqlFromHost(t *testing.T, rt *runtime.Docker, port int, password, sslmode string) (string, bool) {
	t.Helper()
	ctx := context.Background()
	var out strings.Builder
	exit, stderr, err := rt.RunOnce(ctx, runtime.ContainerSpec{Image: "postgres:17", HostNetwork: true,
		Env: []string{"PGPASSWORD=" + password, "PGCONNECT_TIMEOUT=5"},
		Cmd: []string{"psql", fmt.Sprintf("host=127.0.0.1 port=%d user=app dbname=app sslmode=%s", port, sslmode), "-tAc", "SHOW ssl"}}, nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out.String() + stderr), exit == 0
}

func TestDockerOutsideAccess(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys, d.DBDir = kr, t.TempDir()
	project := fmt.Sprintf("t%d", time.Now().UnixNano()%1_000_000)
	args, _, _ := ensureArgs(t, "")
	args.Project = project
	args.OutsidePort, args.Allow, args.PublicIP = 20000+rand.IntN(20000), []string{"192.0.2.1/32"}, "127.0.0.1"
	container, volume, network, _ := dbNames("", project, "production", "acme-db", 17)
	t.Cleanup(func() {
		rt.Remove(ctx, container)
		rt.C.VolumeRemove(ctx, volume, client.VolumeRemoveOptions{Force: true})
		rt.C.NetworkRemove(ctx, network, client.NetworkRemoveOptions{})
	})
	run := func() proto.EnsureDatabaseData {
		res := d.Handle(ctx, cmd(t, proto.CmdEnsureDatabase, args))
		if !res.OK {
			t.Fatalf("%+v", res)
		}
		var data proto.EnsureDatabaseData
		json.Unmarshal(res.Data, &data)
		return data
	}
	data := run()
	pw, _ := kr.Open(data.PasswordBox)
	if out, ok := psqlFromHost(t, rt, args.OutsidePort, string(pw), "require"); ok || !strings.Contains(out, "pg_hba.conf rejects connection") {
		t.Fatalf("not on the allowlist, yet: ok=%v %s", ok, out)
	}
	_, gw, err := rt.NetworkInfo(ctx, network)
	if err != nil {
		t.Fatal(err)
	}
	args.Password, args.Allow = data.PasswordBox, []string{gw.String() + "/32"}
	run()
	if out, ok := psqlFromHost(t, rt, args.OutsidePort, string(pw), "require"); !ok || out != "on" {
		t.Fatalf("allowed over TLS: ok=%v %q", ok, out)
	}
	if out, ok := psqlFromHost(t, rt, args.OutsidePort, string(pw), "disable"); ok {
		t.Fatalf("without TLS, yet: %s", out)
	}
}

func TestDockerPostgres18(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys, d.DBDir = kr, t.TempDir()
	args, _, _ := ensureArgs(t, "")
	args.Project, args.Version = fmt.Sprintf("t%d", time.Now().UnixNano()%1_000_000), 18
	container, volume, network, _ := dbNames("", args.Project, "production", "acme-db", 18)
	t.Cleanup(func() {
		rt.Remove(ctx, container)
		rt.C.VolumeRemove(ctx, volume, client.VolumeRemoveOptions{Force: true})
		rt.C.NetworkRemove(ctx, network, client.NetworkRemoveOptions{})
	})
	if res := d.Handle(ctx, cmd(t, proto.CmdEnsureDatabase, args)); !res.OK {
		t.Fatalf("%+v", res)
	}
	res, err := rt.C.ContainerInspect(ctx, container, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	anon := ""
	for _, m := range res.Container.Mounts {
		if m.Destination == "/var/lib/postgresql" {
			anon = m.Name
		}
	}
	if anon == "" {
		t.Fatalf("no anonymous volume to check: %+v", res.Container.Mounts)
	}
	if err := rt.Remove(ctx, container); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.C.VolumeInspect(ctx, anon, client.VolumeInspectOptions{}); err == nil {
		t.Fatal("the anonymous volume outlived its container")
	}
	if _, err := rt.C.VolumeInspect(ctx, volume, client.VolumeInspectOptions{}); err != nil {
		t.Fatalf("the data volume went with it: %v", err)
	}
}
