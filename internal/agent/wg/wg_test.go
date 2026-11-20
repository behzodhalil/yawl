package wg

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/behzodhalil/yawl/internal/proto"
)

func key(t *testing.T) wgtypes.Key {
	k, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func args(peers ...wgtypes.Key) proto.NetworkArgs {
	a := proto.NetworkArgs{Address: "10.73.0.1", ListenPort: 51820}
	for i, p := range peers {
		a.Peers = append(a.Peers, proto.Peer{PublicKey: p.PublicKey().String(), Endpoint: "5.6.7." + string(rune('8'+i)) + ":51820", AllowedIP: "10.73.0." + string(rune('2'+i)) + "/32"})
	}
	return a
}

func TestApplyCreatesInterface(t *testing.T) {
	l, me, b := NewFake(), key(t), key(t)
	m := &Mesh{Link: l, Key: me}
	if _, err := m.Apply(args(b)); err != nil {
		t.Fatal(err)
	}
	if l.Addr != "10.73.0.1/16" || l.Port != 51820 || l.PrivateKey != me || !l.Up || len(l.Peers) != 1 {
		t.Fatalf("%+v", l)
	}
	p := l.Peers[0]
	if p.PublicKey != b.PublicKey() || p.Endpoint.String() != "5.6.7.8:51820" || len(p.AllowedIPs) != 1 || p.AllowedIPs[0].String() != "10.73.0.2/32" ||
		*p.PersistentKeepaliveInterval != 25*time.Second || !p.ReplaceAllowedIPs {
		t.Fatalf("%+v", p)
	}
}

func TestApplyReplacesPeers(t *testing.T) {
	l, b, c := NewFake(), key(t), key(t)
	m := &Mesh{Link: l, Key: key(t)}
	m.Apply(args(b, c))
	m.Apply(args(c))
	if len(l.Current) != 1 || !l.Current[c.PublicKey()] || len(l.Removed) != 1 || l.Removed[0] != b.PublicKey() {
		t.Fatalf("current %v, removed %v", l.Current, l.Removed)
	}
}

func TestApplyIdempotent(t *testing.T) {
	l, b := NewFake(), key(t)
	m := &Mesh{Link: l, Key: key(t)}
	m.Apply(args(b))
	m.Apply(args(b))
	if l.Created != 1 {
		t.Fatalf("created %d times", l.Created)
	}
}

func TestNetworkReportsHandshakes(t *testing.T) {
	l, b, c := NewFake(), key(t), key(t)
	at := time.UnixMilli(1_700_000_000_000)
	l.Seen = map[wgtypes.Key]time.Time{b.PublicKey(): at}
	m := &Mesh{Link: l, Key: key(t)}
	d, err := m.Apply(args(b, c))
	if err != nil || d.Handshakes[b.PublicKey().String()] != at.UnixMilli() || d.Handshakes[c.PublicKey().String()] != 0 {
		t.Fatalf("%+v %v", d, err)
	}
	if keys := slicesKeys(d.Handshakes); !slices.Contains(keys, c.PublicKey().String()) {
		t.Fatal("a peer never seen is reported as 0")
	}
}

func TestApplyRefusesBadInput(t *testing.T) {
	m := &Mesh{Link: NewFake(), Key: key(t)}
	for _, a := range []proto.NetworkArgs{{Address: "8.8.8.8", ListenPort: 51820}, {Address: "10.73.0.1", ListenPort: 51820, Peers: []proto.Peer{{PublicKey: "x", Endpoint: "1.2.3.4:51820", AllowedIP: "10.73.0.2/32"}}},
		{Address: "10.73.0.1", ListenPort: 51820, Peers: []proto.Peer{{PublicKey: key(t).PublicKey().String(), Endpoint: "1.2.3.4:51820", AllowedIP: "0.0.0.0/0"}}}} {
		if _, err := m.Apply(a); err == nil {
			t.Fatalf("%+v applied", a)
		}
	}
}

func slicesKeys(m map[string]int64) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestAllowNonlocalBind(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ip_nonlocal_bind")
	os.WriteFile(p, []byte("0\n"), 0o644)
	if err := AllowNonlocalBind(p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "1\n" {
		t.Fatalf("%q", b)
	}
}
