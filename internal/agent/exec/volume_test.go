package exec

import (
	"context"
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/agent/wg"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestRemoveVolumeMissing(t *testing.T) {
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	c, _ := proto.NewCommand("v", proto.CmdRemoveVolume, proto.RemoveVolumeArgs{Name: "yawl-acme-production-db-data"})
	if res := d.Handle(context.Background(), c); !res.OK {
		t.Fatalf("%+v", res)
	}
	c, _ = proto.NewCommand("v2", proto.CmdRemoveVolume, proto.RemoveVolumeArgs{Name: "someone-elses"})
	if res := d.Handle(context.Background(), c); res.OK {
		t.Fatal("removed a volume Yawl did not make")
	}
}

func TestDispatchNetwork(t *testing.T) {
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	c, _ := proto.NewCommand("n1", proto.CmdNetwork, proto.NetworkArgs{Address: "10.73.0.1", ListenPort: 51820})
	if res := d.Handle(context.Background(), c); res.OK || res.Cause != "this agent has no WireGuard key; rejoin it to use the private network" {
		t.Fatalf("%+v", res)
	}
	k, _ := wgtypes.GeneratePrivateKey()
	l := wg.NewFake()
	d.Net = &wg.Mesh{Link: l, Key: k}
	if res := d.Handle(context.Background(), c); !res.OK || l.Addr != "10.73.0.1/16" {
		t.Fatalf("%+v", res)
	}
}
