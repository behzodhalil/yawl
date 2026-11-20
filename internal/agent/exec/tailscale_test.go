package exec

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestTailscale(t *testing.T) {
	rt := &runtime.Fake{}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	kr, seal := keyring(t)
	d.Keys = kr
	d.TailnetAddr = func() string {
		if len(rt.Started) > 0 {
			return "100.101.102.103"
		}
		return ""
	}
	res := d.Handle(context.Background(), cmd(t, proto.CmdTailscaleUp, proto.TailscaleUpArgs{AuthKey: seal("tskey-auth-xyz"), Hostname: "hel1"}))
	if !res.OK || len(rt.Started) != 1 {
		t.Fatalf("%+v", res)
	}
	s := rt.Started[0]
	if s.Name != "yawl-tailscale" || s.Image != "yawl/tailscale:stable" || !slices.Equal(rt.Pulled, []string{"tailscale/tailscale:stable yawl/tailscale:stable"}) ||
		!s.HostNetwork ||
		!slices.Contains(s.CapAdd, "NET_ADMIN") || !slices.Contains(s.Devices, "/dev/net/tun") ||
		!slices.Contains(s.Env, "TS_AUTHKEY=tskey-auth-xyz") || !slices.Contains(s.Env, "TS_HOSTNAME=hel1") ||
		!slices.Contains(s.Env, "TS_AUTH_ONCE=true") ||
		len(s.Mounts) != 1 || s.Mounts[0].Source != "yawl-tailscale-state" {
		t.Fatalf("%+v", s)
	}
	res = d.Handle(context.Background(), cmd(t, proto.CmdTailscaleStatus, struct{}{}))
	var st proto.TailscaleStatus
	json.Unmarshal(res.Data, &st)
	if !st.Running || st.Address != "100.101.102.103" {
		t.Fatalf("%+v", st)
	}
	if res := d.Handle(context.Background(), cmd(t, proto.CmdTailscaleDown, struct{}{})); !res.OK {
		t.Fatalf("%+v", res)
	}
	if _, err := rt.Inspect(context.Background(), "yawl-tailscale"); err == nil {
		t.Fatal("the container stayed")
	}
	out := &sent{}
	rt.States = map[string]runtime.State{"ts": {Running: true, Name: "yawl-tailscale"}}
	d.Terminals().Open(context.Background(), proto.TermOpen{ID: "t", Container: "ts"}, out.send)
	for i := 0; i < 100 && !out.has("term.end"); i++ {
		waitShort()
	}
	if !out.has("term.end not a Yawl service's container") {
		t.Fatalf("%v", out.msgs)
	}
}

func waitShort() { <-time.After(10 * time.Millisecond) }

func TestTailscaleOutside(t *testing.T) {
	rt := &runtime.Fake{}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	kr, seal := keyring(t)
	d.Keys = kr
	d.TailnetAddr = func() string { return "100.64.9.9" }
	res := d.Handle(context.Background(), cmd(t, proto.CmdTailscaleUp, proto.TailscaleUpArgs{AuthKey: seal("k"), Hostname: "hel1"}))
	if res.OK || !strings.Contains(res.Cause, "outside Yawl") || len(rt.Started) != 0 {
		t.Fatalf("%+v %d", res, len(rt.Started))
	}
	res = d.Handle(context.Background(), cmd(t, proto.CmdTailscaleStatus, struct{}{}))
	var st proto.TailscaleStatus
	json.Unmarshal(res.Data, &st)
	if st.Running || !st.Outside || st.Address != "100.64.9.9" {
		t.Fatalf("%+v", st)
	}
}

type failStartRT struct{ *runtime.Fake }

func (r failStartRT) Start(ctx context.Context, s runtime.ContainerSpec) (string, error) {
	id, _ := r.Fake.Start(ctx, s)
	return id, errors.New("error gathering device information")
}

func TestTailscaleFailedStart(t *testing.T) {
	rt := &runtime.Fake{}
	d := NewDispatcher(failStartRT{rt}, &fakeProxy{}, &fakeBuilder{})
	kr, seal := keyring(t)
	d.Keys = kr
	d.TailnetAddr = func() string { return "" }
	res := d.Handle(context.Background(), cmd(t, proto.CmdTailscaleUp, proto.TailscaleUpArgs{AuthKey: seal("k"), Hostname: "hel1"}))
	if res.OK || rt.Volumes["yawl-tailscale-state"] {
		t.Fatalf("%+v %v", res, rt.Volumes)
	}
	if _, err := rt.Inspect(context.Background(), "yawl-tailscale"); err == nil {
		t.Fatal("the container stayed")
	}
}
