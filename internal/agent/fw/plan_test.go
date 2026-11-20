package fw

import (
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
)

func plan(t *testing.T, a proto.FirewallArgs, meter bool) string {
	t.Helper()
	a.On = true
	rules, err := Plan(a, []int{22}, meter)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, r := range rules {
		b.WriteString(r.String() + "\n")
	}
	return b.String()
}

func has(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(text, p) {
			t.Errorf("rules lack %q:\n%s", p, text)
		}
	}
}

func TestPlanBase(t *testing.T) {
	r := plan(t, proto.FirewallArgs{}, false)
	has(t, r, `input: iifname "lo" accept`, "input: ct state established,related accept", "input: ct state invalid drop",
		`input: iifname "docker0" accept`, `input: iifname "br-*" accept`, `input: iifname "yawl0" accept`,
		"input: meta l4proto icmp accept", "input: icmpv6 type 135 accept", "input: icmpv6 type 136 accept",
		"input: icmpv6 type 133 accept", "input: icmpv6 type 134 accept", "input: icmpv6 type 128 accept", "input: icmpv6 type 2 accept",
		"input: tcp dport 80 accept", "input: tcp dport 443 accept", "input: udp dport 443 accept",
		"input: ip saddr @ssh_ban4 tcp dport 22 drop", "input: ip6 saddr @ssh_ban6 tcp dport 22 drop", "input: tcp dport 22 accept",
		"forward: ct state established,related accept", `forward: iifname "br-*" accept`, "forward: ct status dnat drop",
		`forward: iifname "yawl0" accept`,
		"input: ip6 saddr fe80::/10 udp dport 546 accept",
		"input: icmpv6 type 130 accept", "input: icmpv6 type 143 accept",
		`input: iifname "tailscale0" accept`)
	if strings.Contains(r, "meter") {
		t.Error("a meter without asking")
	}
}

func TestPlanRules(t *testing.T) {
	r := plan(t, proto.FirewallArgs{
		Allow:   []proto.FwRule{{Proto: "tcp", Port: 8080, From: []string{"203.0.113.0/24", "2001:db8::/32"}}, {Proto: "both", Port: 9000}},
		Block:   []string{"198.51.100.7", "2001:db8:dead::1"},
		SSHFrom: []string{"192.0.2.10"},
		Peers:   []string{"192.0.2.20"},
	}, false)
	has(t, r, "input: ip saddr 203.0.113.0/24 tcp dport 8080 accept", "input: ip6 saddr 2001:db8::/32 tcp dport 8080 accept",
		"input: tcp dport 9000 accept", "input: udp dport 9000 accept",
		"input: ip saddr 198.51.100.7 drop", "forward: ip saddr 198.51.100.7 drop", "input: ip6 saddr 2001:db8:dead::1 drop",
		"input: ip saddr 192.0.2.10 tcp dport 22 accept", "input: ip saddr 192.0.2.20 udp dport 51820 accept")
	if strings.Contains(r, "input: tcp dport 22 accept") {
		t.Error("SSH open to everyone with SSHFrom set")
	}
	if strings.Index(r, "input: ip saddr 198.51.100.7 drop") > strings.Index(r, "input: ct state established") {
		t.Error("blocks come after established")
	}
}

func TestPlanForward(t *testing.T) {
	r := plan(t, proto.FirewallArgs{Kept: []proto.FwRule{{Proto: "tcp", Port: 6543, From: []string{"203.0.113.0/24"}}}}, false)
	has(t, r, "forward: ip saddr 203.0.113.0/24 ct status dnat meta l4proto tcp ct original proto-dst 6543 accept")
	if strings.Index(r, "proto-dst 6543") > strings.Index(r, "forward: ct status dnat drop") {
		t.Error("the allow comes after the drop")
	}
}

func TestPlanMeter(t *testing.T) {
	r := plan(t, proto.FirewallArgs{SSHFrom: []string{"192.0.2.10"}}, true)
	has(t, r, "input: ip saddr 192.0.2.10 tcp dport 22 accept",
		"input: ct state new ip saddr tcp dport 22 meter @ssh_meter4 over 10/minute ban @ssh_ban4 1h drop",
		"input: ct state new ip6 saddr tcp dport 22 meter @ssh_meter6 over 10/minute ban @ssh_ban6 1h drop")
	if strings.Index(r, "meter @ssh_meter4") < strings.Index(r, "input: ip saddr 192.0.2.10 tcp dport 22 accept") {
		t.Error("an allowed address is metered")
	}
}

func TestPlanRejects(t *testing.T) {
	for _, a := range []proto.FirewallArgs{
		{Allow: []proto.FwRule{{Proto: "tcp", Port: 0}}},
		{Allow: []proto.FwRule{{Proto: "tcp", Port: 70000}}},
		{Allow: []proto.FwRule{{Proto: "sctp", Port: 80}}},
		{Block: []string{"not-an-ip"}},
		{SSHFrom: []string{"10.0.0.0/33"}},
	} {
		a.On = true
		if _, err := Plan(a, []int{22}, false); err == nil {
			t.Errorf("%+v planned", a)
		}
	}
	_, err := Plan(proto.FirewallArgs{On: true, Allow: []proto.FwRule{{Proto: "tcp", Port: 80, From: []string{"::ffff:203.0.113.0/120"}}}}, []int{22}, false)
	if err == nil || !strings.Contains(err.Error(), "write 203.0.113.0/24") {
		t.Errorf("mapped CIDR: %v", err)
	}
}

func TestPlanSeveralSSHPorts(t *testing.T) {
	rules, err := Plan(proto.FirewallArgs{On: true}, []int{22, 2222}, true)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, r := range rules {
		b.WriteString(r.String() + "\n")
	}
	has(t, b.String(), "input: tcp dport 22 accept", "input: tcp dport 2222 accept",
		"input: ip saddr @ssh_ban4 tcp dport 2222 drop", "input: ip6 saddr @ssh_ban6 tcp dport 2222 drop",
		"input: ct state new ip saddr tcp dport 2222 meter @ssh_meter4")
}

func TestPlanAcceptsBridgeInputBeforeAllows(t *testing.T) {
	r := plan(t, proto.FirewallArgs{SSHFrom: []string{"203.0.113.0/24"}, Kept: []proto.FwRule{{Proto: "tcp", Port: 6543, From: []string{"203.0.113.0/24"}}}}, false)
	bridge := strings.Index(r, `input: iifname "br-*" accept`)
	for _, later := range []string{"input: ip saddr @ssh_ban4 tcp dport 22 drop", "input: ip saddr 203.0.113.0/24 tcp dport 22 accept"} {
		if i := strings.Index(r, later); bridge < 0 || i < 0 || i < bridge {
			t.Errorf("%q at %d, bridge accept at %d", later, i, bridge)
		}
	}
	if f, d := strings.Index(r, `forward: iifname "br-*" accept`), strings.Index(r, "forward: ct status dnat drop"); f < 0 || d < f {
		t.Errorf("forward bridge accept at %d, dnat drop at %d", f, d)
	}
}
