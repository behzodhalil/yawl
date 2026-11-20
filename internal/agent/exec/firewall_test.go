package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/fw"
	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

type nftStub struct{ texts []string }

func (n *nftStub) Apply(r []fw.Rule) error             { n.texts = append(n.texts, fmt.Sprint(r)); return nil }
func (n *nftStub) Delete() error                       { return nil }
func (n *nftStub) Present() bool                       { return true }
func (n *nftStub) Available() bool                     { return true }
func (n *nftStub) Ban(netip.Addr, time.Duration) error { return nil }
func (n *nftStub) Unban(netip.Addr) error              { return nil }
func (n *nftStub) Bans() ([]proto.FwBan, error)        { return nil, nil }

func TestFirewallCommands(t *testing.T) {
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	ctx := context.Background()
	if res := d.Handle(ctx, cmd(t, proto.CmdFirewallStatus, struct{}{})); res.OK || res.Cause == "" {
		t.Fatalf("no firewall: %+v", res)
	}
	n := &nftStub{}
	d.FW = &fw.Firewall{NFT: n, Dir: t.TempDir(), Now: time.Now, SSHPorts: func() []int { return []int{22} }}
	res := d.Handle(ctx, cmd(t, proto.CmdFirewall, proto.FirewallArgs{On: true, Trial: true, Version: "v1"}))
	if !res.OK || len(n.texts) != 1 {
		t.Fatalf("apply: %+v", res)
	}
	var st proto.FirewallStatus
	res = d.Handle(ctx, cmd(t, proto.CmdFirewallStatus, struct{}{}))
	json.Unmarshal(res.Data, &st)
	if !st.On || st.TrialUntil == 0 || st.Version != "v1" {
		t.Fatalf("status: %+v", st)
	}
	if res := d.Handle(ctx, cmd(t, proto.CmdFirewallKeep, proto.FirewallKeepArgs{Version: "v1"})); !res.OK {
		t.Fatalf("keep: %+v", res)
	}
	res = d.Handle(ctx, cmd(t, proto.CmdFirewallStatus, struct{}{}))
	st = proto.FirewallStatus{}
	json.Unmarshal(res.Data, &st)
	if st.TrialUntil != 0 {
		t.Fatalf("after keep: %+v", st)
	}
}
