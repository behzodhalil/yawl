//go:build !linux

package wg

import (
	"errors"
	"net/netip"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type unsupported struct{}

func NewLink() Link { return unsupported{} }

var errNoLinux = errors.New("the private network needs Linux with WireGuard")

func (unsupported) Ensure(string, netip.Prefix) (bool, error) { return false, errNoLinux }
func (unsupported) Configure(string, wgtypes.Key, int, []wgtypes.PeerConfig) error {
	return errNoLinux
}
func (unsupported) Handshakes(string) (map[wgtypes.Key]time.Time, error) { return nil, errNoLinux }
