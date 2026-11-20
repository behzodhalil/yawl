//go:build wglive

package wg

import (
	"net"
	"testing"
	"time"

	"github.com/vishvananda/netlink"

	"github.com/behzodhalil/yawl/internal/proto"
)

func TestWireGuardLinkLive(t *testing.T) {
	me, peer := key(t), key(t)
	m := &Mesh{Link: NewLink(), Key: me}
	a := proto.NetworkArgs{Address: "10.73.0.1", ListenPort: 51820,
		Peers: []proto.Peer{{PublicKey: peer.PublicKey().String(), Endpoint: "127.0.0.1:51821", AllowedIP: "10.73.0.2/32"}}}
	for i := 0; i < 2; i++ {
		d, err := m.Apply(a)
		if err != nil {
			t.Fatal(err)
		}
		if ms, ok := d.Handshakes[peer.PublicKey().String()]; !ok || ms != 0 {
			t.Fatalf("%+v", d)
		}
	}
	t.Cleanup(func() {
		if l, err := netlink.LinkByName(Interface); err == nil {
			netlink.LinkDel(l)
		}
	})
	l, err := netlink.LinkByName(Interface)
	if err != nil || l.Type() != "wireguard" || l.Attrs().Flags&net.FlagUp == 0 {
		t.Fatalf("%v %+v", err, l)
	}
	addrs, _ := netlink.AddrList(l, netlink.FAMILY_V4)
	if len(addrs) != 1 || addrs[0].IPNet.String() != "10.73.0.1/16" {
		t.Fatalf("%v", addrs)
	}
	a.Peers = nil
	if d, err := m.Apply(a); err != nil || len(d.Handshakes) != 0 {
		t.Fatalf("peer not removed: %+v %v", d, err)
	}
}

func TestHandshakeSurvivesReapply(t *testing.T) {
	ka, kb := key(t), key(t)
	a := &Mesh{Link: NewLink(), Key: ka, Name: "yawlt0"}
	b := &Mesh{Link: NewLink(), Key: kb, Name: "yawlt1"}
	t.Cleanup(func() {
		for _, n := range []string{"yawlt0", "yawlt1"} {
			if l, err := netlink.LinkByName(n); err == nil {
				netlink.LinkDel(l)
			}
		}
	})
	argsA := proto.NetworkArgs{Address: "10.73.1.1", ListenPort: 51830, Peers: []proto.Peer{{PublicKey: kb.PublicKey().String(), Endpoint: "127.0.0.1:51831", AllowedIP: "10.73.1.2/32"}}}
	argsB := proto.NetworkArgs{Address: "10.73.1.2", ListenPort: 51831, Peers: []proto.Peer{{PublicKey: ka.PublicKey().String(), Endpoint: "127.0.0.1:51830", AllowedIP: "10.73.1.1/32"}}}
	if _, err := b.Apply(argsB); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(argsA); err != nil {
		t.Fatal(err)
	}
	var seen time.Time
	for i := 0; i < 40 && seen.IsZero(); i++ {
		time.Sleep(250 * time.Millisecond)
		hs, err := a.Link.Handshakes("yawlt0")
		if err != nil {
			t.Fatal(err)
		}
		seen = hs[kb.PublicKey()]
	}
	if seen.IsZero() {
		t.Fatal("no handshake between two local interfaces")
	}
	time.Sleep(1100 * time.Millisecond)
	d, err := a.Apply(argsA)
	if err != nil || d.Handshakes[kb.PublicKey().String()] != seen.UnixMilli() {
		t.Fatalf("the session was reset by a re-apply: handshake %d, was %d (%v)", d.Handshakes[kb.PublicKey().String()], seen.UnixMilli(), err)
	}
}
