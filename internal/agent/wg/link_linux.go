//go:build linux

package wg

import (
	"errors"
	"net"
	"net/netip"
	"time"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type Kernel struct{}

func NewLink() Link { return Kernel{} }

func (Kernel) Ensure(name string, addr netip.Prefix) (bool, error) {
	created := false
	link, err := netlink.LinkByName(name)
	var notFound netlink.LinkNotFoundError
	if errors.As(err, &notFound) {
		if err := netlink.LinkAdd(&netlink.GenericLink{LinkAttrs: netlink.LinkAttrs{Name: name}, LinkType: "wireguard"}); err != nil {
			return false, err
		}
		created = true
		if link, err = netlink.LinkByName(name); err != nil {
			return false, err
		}
	} else if err != nil {
		return false, err
	}
	ipnet := &net.IPNet{IP: addr.Addr().AsSlice(), Mask: net.CIDRMask(addr.Bits(), 32)}
	if err := netlink.AddrReplace(link, &netlink.Addr{IPNet: ipnet}); err != nil {
		return created, err
	}
	return created, netlink.LinkSetUp(link)
}

func (Kernel) Configure(name string, key wgtypes.Key, port int, peers []wgtypes.PeerConfig) error {
	c, err := wgctrl.New()
	if err != nil {
		return err
	}
	defer c.Close()
	return c.ConfigureDevice(name, wgtypes.Config{PrivateKey: &key, ListenPort: &port, Peers: peers})
}

func (Kernel) Handshakes(name string) (map[wgtypes.Key]time.Time, error) {
	c, err := wgctrl.New()
	if err != nil {
		return nil, err
	}
	defer c.Close()
	d, err := c.Device(name)
	if err != nil {
		return nil, err
	}
	out := map[wgtypes.Key]time.Time{}
	for _, p := range d.Peers {
		out[p.PublicKey] = p.LastHandshakeTime
	}
	return out, nil
}
