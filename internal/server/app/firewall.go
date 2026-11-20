package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type FirewallRules struct {
	On      bool           `json:"on"`
	Allow   []proto.FwRule `json:"allow"`
	Block   []string       `json:"block"`
	SSHFrom []string       `json:"ssh_from"`
}

type FirewallTrial struct {
	Rules   FirewallRules `json:"rules"`
	UntilMs int64         `json:"until"`
	Version string        `json:"version"`
}

func firewallKey(serverID string) string { return "firewall." + serverID }

const FirewallTelegramKey = "firewall.telegram"

func (a *App) FirewallRulesOf(ctx context.Context, serverID string) (FirewallRules, error) {
	r := FirewallRules{Allow: []proto.FwRule{}, Block: []string{}, SSHFrom: []string{}}
	v, err := a.St.Q.GetSetting(ctx, firewallKey(serverID))
	if err != nil {
		return r, nil
	}
	return r, json.Unmarshal([]byte(v), &r)
}

func (a *App) FirewallTrialOf(ctx context.Context, serverID string) (FirewallTrial, bool) {
	var t FirewallTrial
	v, err := a.St.Q.GetSetting(ctx, firewallKey(serverID)+".trial")
	return t, err == nil && json.Unmarshal([]byte(v), &t) == nil
}

func (a *App) SetFirewallTrial(ctx context.Context, serverID string, r FirewallRules, until int64, version string) error {
	b, _ := json.Marshal(FirewallTrial{Rules: r, UntilMs: until, Version: version})
	return a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: firewallKey(serverID) + ".trial", Value: string(b)})
}

func (a *App) DropFirewallTrial(ctx context.Context, serverID string) error {
	return a.St.Q.DeleteSettingKey(ctx, firewallKey(serverID)+".trial")
}

func (a *App) SetFirewallRules(ctx context.Context, serverID string, r FirewallRules) error {
	b, _ := json.Marshal(r)
	return a.St.Tx(ctx, func(q *db.Queries) error {
		if err := q.SetSetting(ctx, db.SetSettingParams{Key: firewallKey(serverID), Value: string(b)}); err != nil {
			return err
		}
		return q.DeleteSettingKey(ctx, firewallKey(serverID)+".trial")
	})
}

func (a *App) KeepFirewallTrial(ctx context.Context, serverID string) error {
	t, ok := a.FirewallTrialOf(ctx, serverID)
	if !ok {
		return fmt.Errorf("There is no change waiting to be kept; it went back.")
	}
	return a.SetFirewallRules(ctx, serverID, t.Rules)
}

func (a *App) FirewallArgs(ctx context.Context, serverID string, r FirewallRules) (proto.FirewallArgs, error) {
	out := proto.FirewallArgs{On: r.On, Allow: r.Allow, Block: r.Block, SSHFrom: r.SSHFrom}
	servers, err := a.St.Q.ListServers(ctx)
	if err != nil {
		return out, err
	}
	for _, s := range servers {
		if s.ID != serverID && s.WgPubkey != "" && s.PublicIp != "" {
			out.Peers = append(out.Peers, s.PublicIp)
		}
	}
	ports, err := a.St.Q.OutsidePortsOnServer(ctx, serverID)
	if err != nil {
		return out, err
	}
	for _, p := range ports {
		var from []string
		json.Unmarshal([]byte(p.AllowJson), &from)
		if len(from) == 0 {
			from = nil
		}
		out.Kept = append(out.Kept, proto.FwRule{Proto: "tcp", Port: int(p.OutsidePort), From: from})
	}
	b, _ := json.Marshal(out)
	sum := sha256.Sum256(b)
	out.Version = hex.EncodeToString(sum[:8])
	return out, nil
}

func CheckFirewallRules(r FirewallRules) error {
	for _, x := range r.Allow {
		if x.Port < 1 || x.Port > 65535 {
			return fmt.Errorf("Port %d is not between 1 and 65535.", x.Port)
		}
		if x.Proto != "tcp" && x.Proto != "udp" && x.Proto != "both" {
			return fmt.Errorf("Port %d: the protocol is TCP, UDP or both.", x.Port)
		}
		if err := checkAddrs(x.From); err != nil {
			return err
		}
	}
	if err := checkAddrs(r.Block); err != nil {
		return err
	}
	return checkAddrs(r.SSHFrom)
}

func checkAddrs(addrs []string) error {
	for _, s := range addrs {
		p, err := parsePrefix(s)
		if err != nil {
			return fmt.Errorf("%s is not an IP address or a range like 203.0.113.0/24.", s)
		}
		if p.Addr().Is4In6() {
			return fmt.Errorf("%s is IPv4 written as IPv6; write %s.", s, netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96))
		}
	}
	return nil
}

func parsePrefix(s string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()), nil
}

func anyContains(addrs []string, a netip.Addr) bool {
	for _, s := range addrs {
		if p, err := parsePrefix(s); err == nil && p.Contains(a) {
			return true
		}
	}
	return false
}

func (r FirewallRules) LocksOut(addr string) bool {
	a, err := netip.ParseAddr(addr)
	if !r.On || err != nil {
		return false
	}
	a = a.Unmap()
	return anyContains(r.Block, a) || (len(r.SSHFrom) > 0 && !anyContains(r.SSHFrom, a))
}
