//go:build docker

package runtime

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestDockerWatchOOM(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rt, err := NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	rt.EnsureNetwork(ctx, "yawl-test-net-rt")
	label := fmt.Sprintf("oom-%d", time.Now().UnixNano())
	heard := make(chan string, 4)
	go rt.WatchOOM(ctx, func(l map[string]string) {
		if l["yawl.test"] == label {
			heard <- l["yawl.service"]
		}
	})
	time.Sleep(time.Second)
	id, err := rt.Start(ctx, ContainerSpec{Name: "yawl-test-" + label, Image: "alpine:3", Network: "yawl-test-net-rt", Memory: 16e6,
		Labels: map[string]string{"yawl.test": label, "yawl.service": "hog"}, Cmd: []string{"tail", "/dev/zero"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(context.Background(), id) })
	select {
	case svc := <-heard:
		if svc != "hog" {
			t.Fatalf("labels %q", svc)
		}
	case <-ctx.Done():
		t.Fatal("no oom event within 60 s")
	}
}
