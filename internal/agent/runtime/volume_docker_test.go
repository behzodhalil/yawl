//go:build docker

package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

func TestRemoveVolumeWithStoppedContainers(t *testing.T) {
	ctx := context.Background()
	rt, err := NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	vol := fmt.Sprintf("yawl_test_rm_%d", time.Now().UnixNano())
	spec := func(i int) ContainerSpec {
		return ContainerSpec{Name: fmt.Sprintf("%s-%d", vol, i), Image: "traefik/whoami", Network: "yawl-test-net-rt",
			Mounts: []Mount{{Source: vol, Target: "/data", Volume: true}}}
	}
	rt.EnsureNetwork(ctx, "yawl-test-net-rt")
	old, err := rt.Start(ctx, spec(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, old) })
	cur, err := rt.Start(ctx, spec(2))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, cur) })
	if err := rt.RemoveVolume(ctx, vol); err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("removed a volume a running container uses: %v", err)
	}
	rt.Stop(ctx, old, time.Second)
	rt.Stop(ctx, cur, time.Second)
	if err := rt.RemoveVolume(ctx, vol); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.C.VolumeInspect(ctx, vol, client.VolumeInspectOptions{}); err == nil {
		t.Fatal("the volume is still there")
	}
}
