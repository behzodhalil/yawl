package exec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestBuildPushes(t *testing.T) {
	rt := &runtime.Fake{}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	res := d.Handle(context.Background(), cmd(t, proto.CmdBuild, proto.BuildArgs{Repo: "https://github.com/acme/web", Commit: "c0ffee", Image: "yawl/acme-production-web:dep_1",
		Push: &proto.PushTo{Ref: "ghcr.io/acme/builds:dep_1", Auth: &proto.RegistryAuth{Username: "bot", Token: "ghp_x"}}}))
	var b proto.BuildData
	json.Unmarshal(res.Data, &b)
	if !res.OK || len(rt.Pushed) != 1 || rt.Pushed[0] != "yawl/acme-production-web:dep_1 ghcr.io/acme/builds:dep_1 bot ghp_x" || b.Image != "ghcr.io/acme/builds:dep_1" {
		t.Fatalf("%+v %v %+v", res, rt.Pushed, b)
	}
	rt.PushErr = errors.New("denied: requested access to the resource is denied")
	res = d.Handle(context.Background(), cmd(t, proto.CmdBuild, proto.BuildArgs{Repo: "https://github.com/acme/web", Commit: "c0ffee", Image: "yawl/acme-production-web:dep_2",
		Push: &proto.PushTo{Ref: "ghcr.io/acme/builds:dep_2"}}))
	if res.OK || !strings.Contains(res.Cause, "ghcr.io refused the push") {
		t.Fatalf("%+v", res)
	}
}
