//go:build docker

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDockerPull(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rt, err := NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := rt.Pull(ctx, "alpine:3", "yawl/test-pull:t1", "", "")
	if err != nil || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("%q %v", digest, err)
	}
	if _, err := rt.C.ImageInspect(ctx, "yawl/test-pull:t1"); err != nil {
		t.Fatalf("not tagged: %v", err)
	}
}
