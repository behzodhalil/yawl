//go:build docker

package runtime

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestDockerPush(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	rt, err := NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	id, err := rt.Start(ctx, ContainerSpec{Name: "yawl-test-registry", Image: "registry:2", HostNetwork: true, Env: []string{"REGISTRY_HTTP_ADDR=127.0.0.1:15000"}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Remove(context.Background(), id)
	for i := 0; i < 50; i++ {
		if res, err := http.Get("http://127.0.0.1:15000/v2/"); err == nil {
			res.Body.Close()
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := rt.Pull(ctx, "alpine:3", "yawl/test-push:built", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := rt.PushAndUntag(ctx, "yawl/test-push:built", "localhost:15000/builds:dep_1", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.C.ImageInspect(ctx, "yawl/test-push:built"); err == nil {
		t.Fatal("the built tag stayed on the build server")
	}
	if _, err := rt.Pull(ctx, "localhost:15000/builds:dep_1", "yawl/test-push:pulled", "", ""); err != nil {
		t.Fatalf("the target could not pull it: %v", err)
	}
}
