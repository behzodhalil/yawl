package egress

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"syscall"
)

var Control func(network, address string, c syscall.RawConn) error

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

var Host []netip.Addr

func HostAddrs(list string) ([]netip.Addr, error) {
	var out []netip.Addr
	for _, s := range strings.Split(list, ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("YAWL_HOST_ADDRS: %q is not an address", s)
		}
		out = append(out, a.Unmap())
	}
	return out, nil
}

const ownServerCaddyPort = 443

func Public(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	if ip := ap.Addr().Unmap(); !ip.IsGlobalUnicast() || ip.IsPrivate() || cgnat.Contains(ip) {
		return fmt.Errorf("%s is not a public address; a Yawl Cloud panel reaches only public ones", ip)
	} else if slices.Contains(Host, ip) && ap.Port() != ownServerCaddyPort {
		return fmt.Errorf("%s is this panel's own server; a Yawl Cloud panel dials it only on port 443", ip)
	}
	return nil
}
