//go:build docker

package exec

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/moby/moby/client"
)

func TestDockerRelease(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	project := fmt.Sprintf("t%d", time.Now().UnixNano()%1_000_000)
	t.Cleanup(func() { rt.C.NetworkRemove(ctx, "yawl-"+project+"-production", client.NetworkRemoveOptions{}) })
	res := d.Handle(ctx, cmd(t, proto.CmdRelease, proto.ReleaseArgs{Project: project, Env: "production", Service: "api",
		Image: "alpine:3", Kind: "worker", Cmd: []string{"sh", "-c", "echo hi; exit 2"}, Timeout: time.Minute}))
	if res.OK || res.Cause != "Release command exited with code 2: hi" || res.Output != "hi" {
		t.Fatalf("%+v", res)
	}
}
