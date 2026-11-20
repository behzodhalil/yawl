//go:build linux

package fw

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"

	"github.com/behzodhalil/yawl/internal/proto"
)

type Nft struct{}

var _ NFT = Nft{}

const ipsDstNAT = 1 << 5

var table = &nftables.Table{Family: nftables.TableFamilyINet, Name: "yawl"}

func (Nft) Available() bool {
	c, err := nftables.New()
	if err != nil {
		return false
	}
	_, err = c.ListTables()
	return err == nil
}

func (Nft) Apply(rules []Rule) error {
	c, err := nftables.New()
	if err != nil {
		return err
	}
	c.AddTable(table)
	c.DelTable(table)
	t := c.AddTable(table)
	sets := map[string]*nftables.Set{}
	for _, s := range []*nftables.Set{
		{Name: "ssh_ban4", KeyType: nftables.TypeIPAddr, HasTimeout: true},
		{Name: "ssh_ban6", KeyType: nftables.TypeIP6Addr, HasTimeout: true},
		{Name: "ssh_meter4", KeyType: nftables.TypeIPAddr, Dynamic: true, HasTimeout: true, Timeout: 10 * time.Minute},
		{Name: "ssh_meter6", KeyType: nftables.TypeIP6Addr, Dynamic: true, HasTimeout: true, Timeout: 10 * time.Minute},
	} {
		s.Table = t
		if err := c.AddSet(s, nil); err != nil {
			return err
		}
		sets[s.Name] = s
	}
	drop, accept := nftables.ChainPolicyDrop, nftables.ChainPolicyAccept
	chains := map[Chain]*nftables.Chain{
		ChainInput: c.AddChain(&nftables.Chain{Name: "input", Table: t, Type: nftables.ChainTypeFilter,
			Hooknum: nftables.ChainHookInput, Priority: nftables.ChainPriorityRef(-1), Policy: &drop}),
		ChainForward: c.AddChain(&nftables.Chain{Name: "forward", Table: t, Type: nftables.ChainTypeFilter,
			Hooknum: nftables.ChainHookForward, Priority: nftables.ChainPriorityRef(-1), Policy: &accept}),
	}
	for _, r := range rules {
		exprs, err := compile(r, sets)
		if err != nil {
			return fmt.Errorf("%s: %w", r, err)
		}
		c.AddRule(&nftables.Rule{Table: t, Chain: chains[r.Chain], Exprs: exprs})
	}
	return c.Flush()
}

func (Nft) Present() bool {
	c, err := nftables.New()
	if err != nil {
		return false
	}
	ts, err := c.ListTablesOfFamily(nftables.TableFamilyINet)
	for _, t := range ts {
		if t.Name == table.Name {
			return true
		}
	}
	return err != nil
}

func (Nft) Delete() error {
	c, err := nftables.New()
	if err != nil {
		return err
	}
	c.AddTable(table) // so the delete never fails on a table that was not there
	c.DelTable(table)
	return c.Flush()
}

func banSetOf(a netip.Addr) string {
	if a.Is4() {
		return "ssh_ban4"
	}
	return "ssh_ban6"
}

func (Nft) Ban(a netip.Addr, d time.Duration) error {
	return elems(a, func(c *nftables.Conn, s *nftables.Set, e []nftables.SetElement) error {
		e[0].Timeout = d
		return c.SetAddElements(s, e)
	})
}

func (Nft) Unban(a netip.Addr) error {
	return elems(a, func(c *nftables.Conn, s *nftables.Set, e []nftables.SetElement) error {
		return c.SetDeleteElements(s, e)
	})
}

func elems(a netip.Addr, do func(*nftables.Conn, *nftables.Set, []nftables.SetElement) error) error {
	c, err := nftables.New()
	if err != nil {
		return err
	}
	s, err := c.GetSetByName(table, banSetOf(a))
	if err != nil {
		return err
	}
	if err := do(c, s, []nftables.SetElement{{Key: a.AsSlice()}}); err != nil {
		return err
	}
	return c.Flush()
}

func (Nft) Bans() ([]proto.FwBan, error) {
	c, err := nftables.New()
	if err != nil {
		return nil, err
	}
	now, out := time.Now(), []proto.FwBan{}
	for _, name := range []string{"ssh_ban4", "ssh_ban6"} {
		s, err := c.GetSetByName(table, name)
		if err != nil {
			return nil, err
		}
		es, err := c.GetSetElements(s)
		if err != nil {
			return nil, err
		}
		for _, e := range es {
			if a, ok := netip.AddrFromSlice(e.Key); ok {
				out = append(out, proto.FwBan{IP: a.String(), Until: now.Add(e.Expires).UnixMilli()})
			}
		}
	}
	return out, nil
}

func compile(r Rule, sets map[string]*nftables.Set) ([]expr.Any, error) {
	var x []expr.Any
	cmp := func(op expr.CmpOp, data []byte) { x = append(x, &expr.Cmp{Op: op, Register: 1, Data: data}) }
	ctBits := func(key expr.CtKey, bits uint32) {
		x = append(x, &expr.Ct{Register: 1, Key: key},
			&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4, Mask: binaryutil.NativeEndian.PutUint32(bits), Xor: make([]byte, 4)})
		cmp(expr.CmpOpNeq, make([]byte, 4))
	}
	family := func(fam int) {
		p := byte(unix.NFPROTO_IPV4)
		if fam == 6 {
			p = unix.NFPROTO_IPV6
		}
		x = append(x, &expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1})
		cmp(expr.CmpOpEq, []byte{p})
	}
	saddr := func(fam int) {
		off, n := uint32(12), uint32(4)
		if fam == 6 {
			off, n = 8, 16
		}
		x = append(x, &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: off, Len: n})
	}
	l4 := func(p byte) {
		x = append(x, &expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1})
		cmp(expr.CmpOpEq, []byte{p})
	}
	dport := func(port int) {
		x = append(x, &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2})
		cmp(expr.CmpOpEq, binaryutil.BigEndian.PutUint16(uint16(port)))
	}

	if r.IIF != "" {
		x = append(x, &expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1})
		if n := len(r.IIF); n > 1 && r.IIF[n-1] == '*' {
			cmp(expr.CmpOpEq, []byte(r.IIF[:n-1])) // a prefix: only those bytes are compared
		} else {
			cmp(expr.CmpOpEq, append([]byte(r.IIF), 0))
		}
	}
	if r.Meter != "" {
		ctBits(expr.CtKeySTATE, expr.CtStateBitNEW)
		family(r.Fam)
		l4(unix.IPPROTO_TCP)
		dport(r.Port)
		saddr(r.Fam)
		m, b := sets[r.Meter], sets[r.Ban]
		x = append(x,
			&expr.Dynset{SrcRegKey: 1, SetName: m.Name, SetID: m.ID, Operation: unix.NFT_DYNSET_OP_ADD,
				Exprs: []expr.Any{&expr.Limit{Type: expr.LimitTypePkts, Rate: sshMeterRate, Unit: expr.LimitTimeMinute, Over: true}}},
			&expr.Dynset{SrcRegKey: 1, SetName: b.Name, SetID: b.ID, Operation: unix.NFT_DYNSET_OP_ADD, Timeout: banHours * time.Hour})
		return append(x, verdict(r.Verdict)), nil
	}
	switch r.State {
	case CtStateAny:
	case CtStateEstablishedRelated:
		ctBits(expr.CtKeySTATE, expr.CtStateBitESTABLISHED|expr.CtStateBitRELATED)
	case CtStateInvalid:
		ctBits(expr.CtKeySTATE, expr.CtStateBitINVALID)
	default:
		return nil, fmt.Errorf("state %q", r.State)
	}
	if r.Src.IsValid() {
		fam := 4
		if r.Src.Addr().Is6() {
			fam = 6
		}
		family(fam)
		saddr(fam)
		addr := r.Src.Addr().AsSlice()
		if bits := r.Src.Bits(); bits < len(addr)*8 {
			m := make([]byte, len(addr))
			for i := 0; i < bits; i++ {
				m[i/8] |= 0x80 >> (i % 8)
			}
			x = append(x, &expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: uint32(len(addr)), Mask: m, Xor: make([]byte, len(addr))})
		}
		cmp(expr.CmpOpEq, addr)
	}
	if r.SrcSet != "" {
		s := sets[r.SrcSet]
		family(r.Fam)
		saddr(r.Fam)
		x = append(x, &expr.Lookup{SourceRegister: 1, SetName: s.Name, SetID: s.ID})
	}
	if r.DNAT {
		ctBits(expr.CtKeySTATUS, ipsDstNAT)
	}
	switch r.Proto {
	case ProtoAny:
	case ProtoICMP:
		l4(unix.IPPROTO_ICMP)
	case ProtoICMPv6:
		l4(unix.IPPROTO_ICMPV6)
		x = append(x, &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 0, Len: 1})
		cmp(expr.CmpOpEq, []byte{byte(r.ICMPv6)})
	case ProtoTCP, ProtoUDP:
		l4(map[Proto]byte{ProtoTCP: unix.IPPROTO_TCP, ProtoUDP: unix.IPPROTO_UDP}[r.Proto])
		if r.DNAT {
			x = append(x, &expr.Ct{Register: 1, Key: expr.CtKeyPROTODST}) // direction 0, original: the port asked for
			cmp(expr.CmpOpEq, binaryutil.BigEndian.PutUint16(uint16(r.Port)))
		} else {
			dport(r.Port)
		}
	default:
		return nil, fmt.Errorf("protocol %q", r.Proto)
	}
	return append(x, verdict(r.Verdict)), nil
}

func verdict(v Verdict) expr.Any {
	if v == VerdictAccept {
		return &expr.Verdict{Kind: expr.VerdictAccept}
	}
	return &expr.Verdict{Kind: expr.VerdictDrop}
}
