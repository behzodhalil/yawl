package wg

import (
	"fmt"
	"net"
	"net/netip"
	"slices"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/behzodhalil/yawl/internal/proto"
)

const (
	Interface = "yawl0"
	keepalive = 25 * time.Second
)

var mesh = netip.MustParsePrefix("10.73.0.0/16")

type Link interface {
	Ensure(name string, addr netip.Prefix) (created bool, err error)
	Configure(name string, key wgtypes.Key, port int, peers []wgtypes.PeerConfig) error
	Handshakes(name string) (map[wgtypes.Key]time.Time, error)
}

type Mesh struct {
	Link Link
	Key  wgtypes.Key
	Name string
}

func (m *Mesh) name() string {
	if m.Name == "" {
		return Interface
	}
	return m.Name
}

func (m *Mesh) Apply(a proto.NetworkArgs) (proto.NetworkData, error) {
	addr, err := netip.ParseAddr(a.Address)
	if err != nil || !mesh.Contains(addr) {
		return proto.NetworkData{}, fmt.Errorf("address %q is not in %s", a.Address, mesh)
	}
	ka := keepalive
	var peers []wgtypes.PeerConfig
	for _, p := range a.Peers {
		pk, err := wgtypes.ParseKey(p.PublicKey)
		if err != nil {
			return proto.NetworkData{}, fmt.Errorf("peer key %q: %w", p.PublicKey, err)
		}
		allowed, err := netip.ParsePrefix(p.AllowedIP)
		if err != nil || allowed.Bits() != 32 || !mesh.Contains(allowed.Addr()) {
			return proto.NetworkData{}, fmt.Errorf("peer %s: allowed %q must be one address of %s", p.PublicKey, p.AllowedIP, mesh)
		}
		ep, err := net.ResolveUDPAddr("udp", p.Endpoint)
		if err != nil {
			return proto.NetworkData{}, fmt.Errorf("peer %s: endpoint %q: %w", p.PublicKey, p.Endpoint, err)
		}
		peers = append(peers, wgtypes.PeerConfig{PublicKey: pk, Endpoint: ep, PersistentKeepaliveInterval: &ka,
			ReplaceAllowedIPs: true, AllowedIPs: []net.IPNet{{IP: allowed.Addr().AsSlice(), Mask: net.CIDRMask(32, 32)}}})
	}
	if _, err := m.Link.Ensure(m.name(), netip.PrefixFrom(addr, mesh.Bits())); err != nil {
		return proto.NetworkData{}, fmt.Errorf("interface %s: %w", m.name(), err)
	}
	// Like `wg syncconf`, never a replace: a replaced peer loses its session,
	// so every minute's command would reset it and read handshake 0 (review C1).
	have, err := m.Link.Handshakes(m.name())
	if err != nil {
		return proto.NetworkData{}, err
	}
	update := peers
	for k := range have {
		if !slices.ContainsFunc(peers, func(p wgtypes.PeerConfig) bool { return p.PublicKey == k }) {
			update = append(update, wgtypes.PeerConfig{PublicKey: k, Remove: true})
		}
	}
	if err := m.Link.Configure(m.name(), m.Key, a.ListenPort, update); err != nil {
		return proto.NetworkData{}, fmt.Errorf("configure %s: %w", m.name(), err)
	}
	seen, err := m.Link.Handshakes(m.name())
	if err != nil {
		return proto.NetworkData{}, err
	}
	d := proto.NetworkData{Handshakes: map[string]int64{}}
	for _, p := range peers {
		var ms int64
		if t, ok := seen[p.PublicKey]; ok && !t.IsZero() {
			ms = t.UnixMilli()
		}
		d.Handshakes[p.PublicKey.String()] = ms
	}
	return d, nil
}
