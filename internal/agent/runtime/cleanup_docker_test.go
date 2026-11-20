//go:build docker

package runtime

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestCleanup(t *testing.T) {
	ctx := context.Background()
	rt, err := NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	tag := fmt.Sprint(time.Now().UnixNano())
	rt.EnsureNetwork(ctx, "yawl-test-net-once")
	yawl, err := rt.Start(ctx, ContainerSpec{Name: "yawl-test-clean-" + tag, Image: "alpine:3", Network: "yawl-test-net-once", Cmd: []string{"true"},
		Labels: map[string]string{"yawl.project": "cleanup-test"}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := rt.Start(ctx, ContainerSpec{Name: "not-yawl-" + tag, Image: "alpine:3", Network: "yawl-test-net-once", Cmd: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, other); rt.Remove(ctx, yawl) })
	rt.Stop(ctx, yawl, time.Second)
	rt.Stop(ctx, other, time.Second)
	vol := "yawl-test-unused-" + tag
	if _, err := rt.Start(ctx, ContainerSpec{Name: "yawl-test-vol-" + tag, Image: "alpine:3", Network: "yawl-test-net-once", Cmd: []string{"true"},
		Mounts: []Mount{{Source: vol, Target: "/v", Volume: true}}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	rt.Remove(ctx, "yawl-test-vol-"+tag)
	t.Cleanup(func() { rt.RemoveVolume(ctx, vol) })
	res, err := rt.pruneStopped(ctx, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Inspect(ctx, yawl); err == nil || res.Containers < 1 {
		t.Fatalf("the stopped Yawl container stayed: %+v", res)
	}
	if _, err := rt.Inspect(ctx, other); err != nil {
		t.Fatal("a container that is not Yawl's was removed")
	}
	unused, err := rt.UnusedVolumes(ctx)
	if err != nil || !slices.Contains(unused, vol) {
		t.Fatalf("%v %v", unused, err)
	}
	if ok, _ := rt.VolumeExists(ctx, vol); !ok {
		t.Fatal("the cleanup removed a volume")
	}
}
