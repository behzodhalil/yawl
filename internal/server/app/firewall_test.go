package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestFirewallArgs(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_2", Name: "fsn1", PublicIp: "5.6.7.8", CreatedAt: 1, UpdatedAt: 1})
	st.DB.ExecContext(ctx, "UPDATE servers SET wg_pubkey = 'k2', private_ip = '10.73.0.2' WHERE id = 'srv_2'")
	st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_3", Name: "nbg1", PublicIp: "9.9.9.9", CreatedAt: 1, UpdatedAt: 1})
	st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_db", EnvironmentID: "env_1", Name: "acme-db", Kind: "postgres",
		OutsidePort: 6543, AllowJson: `["203.0.113.0/24"]`, CreatedAt: 1, UpdatedAt: 1})
	st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_db2", EnvironmentID: "env_1", Name: "open-db", Kind: "postgres",
		OutsidePort: 6544, AllowJson: `[]`, CreatedAt: 1, UpdatedAt: 1})
	rules := FirewallRules{On: true, Allow: []proto.FwRule{{Proto: "tcp", Port: 8080}}, Block: []string{"198.51.100.7"}, SSHFrom: []string{"192.0.2.10"}}
	got, err := a.FirewallArgs(ctx, "srv_1", rules)
	if err != nil {
		t.Fatal(err)
	}
	want := proto.FirewallArgs{On: true, Allow: rules.Allow, Block: rules.Block, SSHFrom: rules.SSHFrom, Peers: []string{"5.6.7.8"},
		Kept: []proto.FwRule{{Proto: "tcp", Port: 6543, From: []string{"203.0.113.0/24"}}, {Proto: "tcp", Port: 6544}}}
	v := got.Version
	got.Version = ""
	if !reflect.DeepEqual(got, want) || len(v) != 16 {
		t.Fatalf("got  %+v\nwant %+v (version %q)", got, want, v)
	}
	again, _ := a.FirewallArgs(ctx, "srv_1", rules)
	rules.Allow = []proto.FwRule{{Proto: "tcp", Port: 8081}}
	other, _ := a.FirewallArgs(ctx, "srv_1", rules)
	if again.Version != v || other.Version == v {
		t.Fatal("the version does not follow the args")
	}
}

func TestFirewallRulesStored(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	if r, _ := a.FirewallRulesOf(ctx, "srv_1"); r.On || r.Allow == nil {
		t.Fatalf("default %+v", r)
	}
	if err := a.SetFirewallTrial(ctx, "srv_1", FirewallRules{On: true, Allow: []proto.FwRule{{Proto: "udp", Port: 9}}}, 1000, "v1"); err != nil {
		t.Fatal(err)
	}
	tr, ok := a.FirewallTrialOf(ctx, "srv_1")
	if !ok || tr.UntilMs != 1000 || tr.Version != "v1" {
		t.Fatalf("trial %+v %v", tr, ok)
	}
	if err := a.KeepFirewallTrial(ctx, "srv_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.FirewallTrialOf(ctx, "srv_1"); ok {
		t.Fatal("trial left after keep")
	}
	if r, _ := a.FirewallRulesOf(ctx, "srv_1"); !r.On || r.Allow[0].Port != 9 {
		t.Fatalf("kept %+v", r)
	}
}

func TestCheckFirewallRules(t *testing.T) {
	for _, r := range []FirewallRules{
		{Allow: []proto.FwRule{{Proto: "tcp", Port: 0}}},
		{Allow: []proto.FwRule{{Proto: "icmp", Port: 1}}},
		{Block: []string{"nope"}},
		{SSHFrom: []string{"1.2.3.4/40"}},
	} {
		if err := CheckFirewallRules(r); err == nil {
			t.Errorf("%+v passed", r)
		}
	}
	if err := CheckFirewallRules(FirewallRules{Allow: []proto.FwRule{{Proto: "both", Port: 53, From: []string{"10.0.0.0/8", "2001:db8::1"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := CheckFirewallRules(FirewallRules{SSHFrom: []string{"::ffff:203.0.113.0/120"}}); err == nil || err.Error() != "::ffff:203.0.113.0/120 is IPv4 written as IPv6; write 203.0.113.0/24." {
		t.Fatalf("mapped CIDR: %v", err)
	}
}

func TestBlocksAddress(t *testing.T) {
	r := FirewallRules{On: true, SSHFrom: []string{"198.51.100.0/24"}}
	if !r.LocksOut("203.0.113.7") || r.LocksOut("198.51.100.4") {
		t.Fatal("SSHFrom")
	}
	r = FirewallRules{On: true, Block: []string{"203.0.113.0/24"}}
	if !r.LocksOut("203.0.113.7") || (FirewallRules{On: false, Block: r.Block}).LocksOut("203.0.113.7") {
		t.Fatal("Block")
	}
}
