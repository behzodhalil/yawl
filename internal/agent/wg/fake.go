package wg

import (
	"net/netip"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type Fake struct {
	Addr       string
	Up         bool
	Created    int
	PrivateKey wgtypes.Key
	Port       int
	Peers      []wgtypes.PeerConfig
	Current    map[wgtypes.Key]bool
	Removed    []wgtypes.Key
	Seen       map[wgtypes.Key]time.Time
}

func NewFake() *Fake { return &Fake{} }

func (f *Fake) Ensure(_ string, addr netip.Prefix) (bool, error) {
	created := !f.Up
	if created {
		f.Created++
	}
	f.Addr, f.Up = addr.String(), true
	return created, nil
}

func (f *Fake) Configure(_ string, key wgtypes.Key, port int, peers []wgtypes.PeerConfig) error {
	f.PrivateKey, f.Port = key, port
	if f.Current == nil {
		f.Current = map[wgtypes.Key]bool{}
	}
	f.Peers = nil
	for _, p := range peers {
		if p.Remove {
			delete(f.Current, p.PublicKey)
			delete(f.Seen, p.PublicKey)
			f.Removed = append(f.Removed, p.PublicKey)
			continue
		}
		f.Current[p.PublicKey] = true
		f.Peers = append(f.Peers, p)
	}
	return nil
}

func (f *Fake) Handshakes(string) (map[wgtypes.Key]time.Time, error) {
	out := map[wgtypes.Key]time.Time{}
	for k := range f.Current {
		out[k] = f.Seen[k]
	}
	return out, nil
}
