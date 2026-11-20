//go:build !linux

package fw

import (
	"net/netip"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
)

type Nft struct{}

func (Nft) Available() bool                     { return false }
func (Nft) Apply([]Rule) error                  { return ErrNoNft }
func (Nft) Delete() error                       { return nil }
func (Nft) Present() bool                       { return false }
func (Nft) Ban(netip.Addr, time.Duration) error { return ErrNoNft }
func (Nft) Unban(netip.Addr) error              { return ErrNoNft }
func (Nft) Bans() ([]proto.FwBan, error)        { return nil, ErrNoNft }
