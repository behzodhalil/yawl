//go:build fwlive

package fw

import (
	"net"
	"net/netip"
	"runtime"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"

	"github.com/behzodhalil/yawl/internal/proto"
)

type world struct {
	t    *testing.T
	peer netns.NsHandle
}

func newWorld(t *testing.T) *world {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	orig, _ := netns.Get()
	peer, err := netns.New()
	if err != nil {
		t.Fatal(err)
	}
	netns.Set(orig)
	la := netlink.NewLinkAttrs()
	la.Name = "v0"
	if err := netlink.LinkAdd(&netlink.Veth{LinkAttrs: la, PeerName: "v1"}); err != nil {
		t.Fatal(err)
	}
	v0, _ := netlink.LinkByName("v0")
	v1, _ := netlink.LinkByName("v1")
	netlink.LinkSetNsFd(v1, int(peer))
	a0, _ := netlink.ParseAddr("10.99.0.1/24")
	netlink.AddrAdd(v0, a0)
	netlink.LinkSetUp(v0)
	h, _ := netlink.NewHandleAt(peer)
	p1, _ := h.LinkByName("v1")
	a1, _ := netlink.ParseAddr("10.99.0.2/24")
	h.AddrAdd(p1, a1)
	h.LinkSetUp(p1)
	t.Cleanup(func() {
		Nft{}.Delete()
		netlink.LinkDel(v0)
		peer.Close()
	})
	return &world{t: t, peer: peer}
}

func (w *world) listen(port string) {
	l, err := net.Listen("tcp", "10.99.0.1:"+port)
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
}

func (w *world) reach(port string) bool {
	done := make(chan bool)
	go func() {
		runtime.LockOSThread() // never unlocked: the thread ends with the goroutine, ns and all
		netns.Set(w.peer)
		c, err := net.DialTimeout("tcp", "10.99.0.1:"+port, time.Second)
		if err == nil {
			c.Close()
		}
		done <- err == nil
	}()
	return <-done
}

func TestLiveRulesetsApply(t *testing.T) {
	for _, a := range []proto.FirewallArgs{
		{On: true},
		{On: true, Allow: []proto.FwRule{{Proto: "both", Port: 9000, From: []string{"203.0.113.0/24", "2001:db8::/32"}}},
			Block: []string{"198.51.100.7", "2001:db8:dead::/48"}, SSHFrom: []string{"192.0.2.10"}, Peers: []string{"192.0.2.20", "2001:db8::20"},
			Kept: []proto.FwRule{{Proto: "tcp", Port: 6543}}},
	} {
		for _, meter := range []bool{false, true} {
			rules, err := Plan(a, []int{2222}, meter)
			if err != nil {
				t.Fatal(err)
			}
			if err := (Nft{}).Apply(rules); err != nil {
				t.Fatalf("meter %v: %v", meter, err)
			}
		}
	}
	(Nft{}).Delete()
}

func TestLiveDropsAndReverts(t *testing.T) {
	w := newWorld(t)
	w.listen("7001")
	w.listen("7002")
	if !w.reach("7001") || !w.reach("7002") {
		t.Fatal("not reachable before the firewall")
	}
	now := time.Now()
	f := &Firewall{NFT: Nft{}, Dir: t.TempDir(), Now: func() time.Time { return now }, SSHPorts: func() []int { return []int{22} }}
	if err := f.Apply(proto.FirewallArgs{On: true, Version: "v1", Allow: []proto.FwRule{{Proto: "tcp", Port: 7002}}}); err != nil {
		t.Fatal(err)
	}
	if w.reach("7001") || !w.reach("7002") {
		t.Fatal("v1: 7001 must be closed, 7002 open")
	}
	if err := f.Apply(proto.FirewallArgs{On: true, Version: "v2", Trial: true, Allow: []proto.FwRule{{Proto: "tcp", Port: 7001, From: []string{"10.99.0.0/24"}}}}); err != nil {
		t.Fatal(err)
	}
	if !w.reach("7001") || w.reach("7002") {
		t.Fatal("trial v2: 7001 open to 10.99.0.0/24, 7002 closed")
	}
	now = now.Add(proto.FirewallTrial + time.Second)
	f.Check()
	if w.reach("7001") || !w.reach("7002") {
		t.Fatal("after the window: back to v1")
	}
	if err := f.Apply(proto.FirewallArgs{On: true, Version: "v3", Block: []string{"10.99.0.2"}, Allow: []proto.FwRule{{Proto: "tcp", Port: 7002}}}); err != nil {
		t.Fatal(err)
	}
	if w.reach("7002") {
		t.Fatal("a blocked address got through an allow")
	}
	if err := f.Apply(proto.FirewallArgs{On: false, Version: "off"}); err != nil {
		t.Fatal(err)
	}
	if !w.reach("7001") {
		t.Fatal("off: everything open again")
	}
}

func TestLiveMeterAndBan(t *testing.T) {
	w := newWorld(t)
	w.listen("7022")
	f := &Firewall{NFT: Nft{}, Dir: t.TempDir(), Now: time.Now, SSHPorts: func() []int { return []int{7022} }}
	if err := f.Apply(proto.FirewallArgs{On: true, Version: "m1"}); err != nil {
		t.Fatal(err)
	}
	refused := 0
	for range 30 {
		if !w.reach("7022") {
			refused++
		}
	}
	bans, err := Nft{}.Bans()
	if err != nil || refused == 0 || len(bans) != 1 || bans[0].IP != "10.99.0.2" || bans[0].Until < time.Now().Add(50*time.Minute).UnixMilli() {
		t.Fatalf("refused %d, bans %+v, %v", refused, bans, err)
	}
	if err := f.Unban("10.99.0.2"); err != nil {
		t.Fatal(err)
	}
	if bans, _ := (Nft{}).Bans(); len(bans) != 0 {
		t.Fatalf("still banned: %+v", bans)
	}
	if err := (Nft{}).Ban(netip.MustParseAddr("10.99.0.2"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if w.reach("7022") {
		t.Fatal("a banned address reached SSH")
	}
}
