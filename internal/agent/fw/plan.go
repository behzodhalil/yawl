package fw

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/behzodhalil/yawl/internal/proto"
)

type Chain string

const (
	ChainInput   Chain = "input"
	ChainForward Chain = "forward"
)

type CtState string

const (
	CtStateAny                CtState = ""
	CtStateEstablishedRelated CtState = "established,related"
	CtStateInvalid            CtState = "invalid"
)

type Proto string

const (
	ProtoAny    Proto = ""
	ProtoICMP   Proto = "icmp"
	ProtoICMPv6 Proto = "icmpv6"
	ProtoTCP    Proto = "tcp"
	ProtoUDP    Proto = "udp"
)

type Verdict string

const (
	VerdictAccept Verdict = "accept"
	VerdictDrop   Verdict = "drop"
)

type Rule struct {
	Chain   Chain
	IIF     string
	State   CtState
	Src     netip.Prefix
	Fam     int
	SrcSet  string
	DNAT    bool
	Proto   Proto
	Port    int
	ICMPv6  int
	Meter   string
	Ban     string
	Verdict Verdict
}

const (
	sshMeterRate = 10
	banHours     = 1
)

func ipKw(fam int) string {
	if fam == 6 {
		return "ip6"
	}
	return "ip"
}

func (r Rule) String() string {
	var p []string
	if r.IIF != "" {
		p = append(p, fmt.Sprintf("iifname %q", r.IIF))
	}
	if r.Meter != "" {
		return fmt.Sprintf("%s: ct state new %s saddr tcp dport %d meter @%s over %d/minute ban @%s %dh %s",
			r.Chain, ipKw(r.Fam), r.Port, r.Meter, sshMeterRate, r.Ban, banHours, r.Verdict)
	}
	if r.State != "" {
		p = append(p, "ct state "+string(r.State))
	}
	if r.Src.IsValid() {
		a := r.Src.String()
		if r.Src.IsSingleIP() {
			a = r.Src.Addr().String()
		}
		fam := 4
		if r.Src.Addr().Is6() {
			fam = 6
		}
		p = append(p, ipKw(fam)+" saddr "+a)
	}
	if r.SrcSet != "" {
		p = append(p, ipKw(r.Fam)+" saddr @"+r.SrcSet)
	}
	if r.DNAT {
		p = append(p, "ct status dnat")
	}
	switch {
	case r.Proto == ProtoICMP:
		p = append(p, "meta l4proto icmp")
	case r.Proto == ProtoICMPv6:
		p = append(p, fmt.Sprintf("icmpv6 type %d", r.ICMPv6))
	case r.Proto != ProtoAny && r.DNAT:
		p = append(p, fmt.Sprintf("meta l4proto %s ct original proto-dst %d", r.Proto, r.Port))
	case r.Proto != ProtoAny:
		p = append(p, fmt.Sprintf("%s dport %d", r.Proto, r.Port))
	}
	return string(r.Chain) + ": " + strings.Join(append(p, string(r.Verdict)), " ")
}

// icmpv6 are the types IPv6 needs to work (neighbour and router discovery,
// path MTU) and ping: unreachable, too big, time exceeded, parameter problem,
// echo request, router solicit and advert, neighbour solicit and advert.
var icmpv6 = []int{1, 2, 3, 4, 128, 130, 131, 132, 133, 134, 135, 136, 143} // with MLD (130 to 132, 143)

var bridges = []string{"docker0", "br-*"}

func Plan(a proto.FirewallArgs, sshPorts []int, meter bool) ([]Rule, error) {
	blocks, err := prefixes(a.Block)
	if err != nil {
		return nil, fmt.Errorf("block: %w", err)
	}
	var in, fwd []Rule
	for _, b := range blocks {
		in = append(in, Rule{Chain: ChainInput, Src: b, Verdict: VerdictDrop})
		fwd = append(fwd, Rule{Chain: ChainForward, Src: b, Verdict: VerdictDrop})
	}
	in = append(in,
		Rule{Chain: ChainInput, IIF: "lo", Verdict: VerdictAccept},
		Rule{Chain: ChainInput, State: CtStateEstablishedRelated, Verdict: VerdictAccept},
		Rule{Chain: ChainInput, State: CtStateInvalid, Verdict: VerdictDrop})
	for _, i := range append(bridges, "yawl0", "tailscale0") {
		in = append(in, Rule{Chain: ChainInput, IIF: i, Verdict: VerdictAccept})
	}
	in = append(in, Rule{Chain: ChainInput, Proto: ProtoICMP, Verdict: VerdictAccept})
	for _, t := range icmpv6 {
		in = append(in, Rule{Chain: ChainInput, Proto: ProtoICMPv6, ICMPv6: t, Verdict: VerdictAccept})
	}
	// DHCPv6 answers from the router's link-local address, which conntrack does not tie to the ask (review I7)
	in = append(in, Rule{Chain: ChainInput, Src: netip.MustParsePrefix("fe80::/10"), Proto: ProtoUDP, Port: 546, Verdict: VerdictAccept})
	var ssh []proto.FwRule
	for _, port := range sshPorts {
		for _, fam := range []int{4, 6} {
			in = append(in, Rule{Chain: ChainInput, Fam: fam, SrcSet: fmt.Sprintf("ssh_ban%d", fam), Proto: ProtoTCP, Port: port, Verdict: VerdictDrop})
		}
		ssh = append(ssh, proto.FwRule{Proto: "tcp", Port: port, From: a.SSHFrom})
	}
	if len(a.SSHFrom) > 0 {
		r, err := accepts(ChainInput, false, ssh...)
		if err != nil {
			return nil, fmt.Errorf("SSH: %w", err)
		}
		in = append(in, r...)
	}
	if meter {
		for _, port := range sshPorts {
			for _, fam := range []int{4, 6} {
				in = append(in, Rule{Chain: ChainInput, Fam: fam, Port: port, Meter: fmt.Sprintf("ssh_meter%d", fam), Ban: fmt.Sprintf("ssh_ban%d", fam), Verdict: VerdictDrop})
			}
		}
	}
	open := []proto.FwRule{{Proto: "tcp", Port: 80}, {Proto: "tcp", Port: 443}, {Proto: "udp", Port: 443}}
	if len(a.SSHFrom) == 0 {
		open = append(ssh, open...)
	}
	if len(a.Peers) > 0 {
		open = append(open, proto.FwRule{Proto: "udp", Port: 51820, From: a.Peers})
	}
	r, err := accepts(ChainInput, false, append(append(open, a.Kept...), a.Allow...)...)
	if err != nil {
		return nil, err
	}
	in = append(in, r...)

	// A port Docker publishes is DNAT'ed and forwarded, never seen by input:
	// it passes only when an allow names it (the ufw gap, record 28).
	fwd = append(fwd, Rule{Chain: ChainForward, State: CtStateEstablishedRelated, Verdict: VerdictAccept})
	for _, i := range append(bridges, "yawl0") {
		fwd = append(fwd, Rule{Chain: ChainForward, IIF: i, Verdict: VerdictAccept})
	}
	r, err = accepts(ChainForward, true, append(append([]proto.FwRule{}, a.Kept...), a.Allow...)...)
	if err != nil {
		return nil, err
	}
	fwd = append(append(fwd, r...), Rule{Chain: ChainForward, DNAT: true, Verdict: VerdictDrop})
	return append(in, fwd...), nil
}

func accepts(chain Chain, dnat bool, rules ...proto.FwRule) ([]Rule, error) {
	var out []Rule
	for _, r := range rules {
		if r.Port < 1 || r.Port > 65535 {
			return nil, fmt.Errorf("port %d is not between 1 and 65535", r.Port)
		}
		protos := map[string][]Proto{"tcp": {ProtoTCP}, "udp": {ProtoUDP}, "both": {ProtoTCP, ProtoUDP}}[r.Proto]
		if protos == nil {
			return nil, fmt.Errorf("port %d: protocol %q is not tcp, udp or both", r.Port, r.Proto)
		}
		srcs, err := prefixes(r.From)
		if err != nil {
			return nil, fmt.Errorf("port %d: %w", r.Port, err)
		}
		if len(srcs) == 0 {
			srcs = []netip.Prefix{{}}
		}
		for _, p := range protos {
			for _, s := range srcs {
				out = append(out, Rule{Chain: chain, Src: s, DNAT: dnat, Proto: p, Port: r.Port, Verdict: VerdictAccept})
			}
		}
	}
	return out, nil
}

func prefixes(addrs []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range addrs {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			a, err2 := netip.ParseAddr(s)
			if err2 != nil {
				return nil, fmt.Errorf("%q is not an IP address or CIDR", s)
			}
			p = netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen())
		}
		p = p.Masked()
		if a := p.Addr(); a.Is4In6() {
			return nil, fmt.Errorf("%q is IPv4 written as IPv6; write %s", s, netip.PrefixFrom(a.Unmap(), p.Bits()-96))
		}
		out = append(out, p)
	}
	return out, nil
}
