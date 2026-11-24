package exec

import (
	"context"
	"encoding/json"
	"net/netip"
	"slices"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

type fakeResolver map[string][]string

func (r fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	var out []netip.Addr
	for _, a := range r[host] {
		out = append(out, netip.MustParseAddr(a))
	}
	return out, nil
}

func domainDispatcher(t *testing.T, addrs []string, issued bool) (*Dispatcher, *fakeProxy) {
	rt := &runtime.Fake{States: map[string]runtime.State{"c1": {Running: true, IP: netip.MustParseAddr("172.18.0.5")}}}
	px := &fakeProxy{}
	d := NewDispatcher(rt, px, &fakeBuilder{})
	d.Resolver = fakeResolver{"shop.acme.uz": addrs}
	d.CertCheck = func(context.Context, string) bool { return issued }
	return d, px
}

func ensureDomain(t *testing.T, d *Dispatcher) proto.EnsureDomainData {
	t.Helper()
	res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDomain, proto.EnsureDomainArgs{Host: "shop.acme.uz", ExpectIP: "5.6.7.8", Container: "c1", Port: 10000}))
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	var data proto.EnsureDomainData
	json.Unmarshal(res.Data, &data)
	return data
}

func TestDomainDNS(t *testing.T) {
	cases := []struct {
		addrs       []string
		dns, detail string
	}{
		{[]string{"5.6.7.8"}, "ok", ""},
		{[]string{"1.2.3.4"}, "wrong", "shop.acme.uz points to 1.2.3.4. Set an A record: shop.acme.uz → 5.6.7.8."},
		{nil, "wrong", "shop.acme.uz has no A record yet. Set one: shop.acme.uz → 5.6.7.8."},
	}
	for _, c := range cases {
		d, _ := domainDispatcher(t, c.addrs, false)
		if got := ensureDomain(t, d); got.DNS != c.dns || got.DNSDetail != c.detail {
			t.Fatalf("%v: %+v", c.addrs, got)
		}
	}
}

func TestDomainDNSMixed(t *testing.T) {
	d, px := domainDispatcher(t, []string{"5.6.7.8", "9.9.9.9"}, true)
	if got := ensureDomain(t, d); got.DNS != "wrong" || got.DNSDetail != "shop.acme.uz points to 9.9.9.9. Set an A record: shop.acme.uz → 5.6.7.8." {
		t.Fatalf("%+v", got)
	}
	d, _ = domainDispatcher(t, []string{"5.6.7.8", "2001:db8::1"}, true)
	if got := ensureDomain(t, d); got.DNS != "wrong" || got.DNSDetail != "shop.acme.uz has an AAAA record 2001:db8::1 that is not this server. Remove it, and keep the A record shop.acme.uz → 5.6.7.8." {
		t.Fatalf("%+v", got)
	}
	if len(px.routes) != 0 {
		t.Fatal("route added with wrong DNS")
	}
}

func TestNoRouteBeforeDNS(t *testing.T) {
	d, px := domainDispatcher(t, []string{"1.2.3.4"}, true)
	ensureDomain(t, d)
	if len(px.routes) != 0 || len(px.redirects) != 0 {
		t.Fatalf("routes %v redirects %v", px.routes, px.redirects)
	}
}

func TestDomainIssuedRedirects(t *testing.T) {
	d, px := domainDispatcher(t, []string{"5.6.7.8"}, true)
	if got := ensureDomain(t, d); got.Cert != "issued" || px.routes["shop.acme.uz"] != "172.18.0.5:10000" || !slices.Equal(px.redirects, []string{"shop.acme.uz"}) {
		t.Fatalf("%+v routes %v redirects %v", got, px.routes, px.redirects)
	}
	d, px = domainDispatcher(t, []string{"5.6.7.8"}, false)
	if got := ensureDomain(t, d); got.Cert != "pending" || len(px.redirects) != 0 || px.routes["shop.acme.uz"] == "" {
		t.Fatalf("%+v redirects %v", got, px.redirects)
	}
}

func TestSwitchAllHostsAndRemove(t *testing.T) {
	d, px := domainDispatcher(t, nil, false)
	res := d.Handle(context.Background(), cmd(t, proto.CmdSwitchRoute, proto.SwitchArgs{Hosts: []string{"a.sslip.io", "shop.acme.uz"}, Container: "c1", Port: 10000}))
	if !res.OK || len(px.routes) != 2 {
		t.Fatalf("%+v %v", res, px.routes)
	}
	res = d.Handle(context.Background(), cmd(t, proto.CmdRemoveRoute, proto.RemoveRouteArgs{Host: "shop.acme.uz"}))
	if !res.OK || !slices.Equal(px.removed, []string{"shop.acme.uz"}) {
		t.Fatalf("%+v %v", res, px.removed)
	}
}

func TestSwitchWaitsForCertificates(t *testing.T) {
	d, _ := domainDispatcher(t, nil, false)
	var asked []string
	d.CertCheck = func(_ context.Context, host string) bool { asked = append(asked, host); return host == "a.sslip.io" }
	hosts := []string{"a.sslip.io", "shop.acme.uz", "shop.acme.uz/api"}
	res := d.Handle(context.Background(), cmd(t, proto.CmdSwitchRoute, proto.SwitchArgs{Hosts: hosts, Container: "c1", Port: 10000}))
	if !res.OK || len(asked) != 0 {
		t.Fatalf("without WaitCert: %+v asked %v", res, asked)
	}
	res = d.Handle(context.Background(), cmd(t, proto.CmdSwitchRoute, proto.SwitchArgs{Hosts: hosts, Container: "c1", Port: 10000, WaitCert: true}))
	if !res.OK || !slices.Equal(asked, []string{"a.sslip.io", "shop.acme.uz"}) || res.Output != "Live, but the certificate for shop.acme.uz is not issued yet. HTTPS answers once it is." {
		t.Fatalf("%+v asked %v", res, asked)
	}
}

func TestEnsureDomainUpstream(t *testing.T) {
	px := &fakeProxy{}
	d := NewDispatcher(&runtime.Fake{}, px, &fakeBuilder{})
	d.Resolver = fakeResolver{"panel.acme.uz": {"5.6.7.8"}}
	d.CertCheck = func(context.Context, string) bool { return false }
	args, _ := json.Marshal(proto.EnsureDomainArgs{Host: "panel.acme.uz", ExpectIP: "5.6.7.8", Upstream: "127.0.0.1:7000"})
	res := d.Handle(context.Background(), proto.Command{Name: proto.CmdEnsureDomain, Args: args})
	if res.Cause != "" || px.routes["panel.acme.uz"] != "127.0.0.1:7000" {
		t.Fatalf("%+v %v", res, px.routes)
	}
}

func TestDomainWithPath(t *testing.T) {
	d, px := domainDispatcher(t, []string{"5.6.7.8"}, false)
	var asked string
	d.CertCheck = func(_ context.Context, host string) bool { asked = host; return true }
	res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDomain, proto.EnsureDomainArgs{Host: "shop.acme.uz/api", ExpectIP: "5.6.7.8", Container: "c1", Port: 10000}))
	var data proto.EnsureDomainData
	json.Unmarshal(res.Data, &data)
	if !res.OK || data.DNS != "ok" || data.Cert != "issued" || asked != "shop.acme.uz" || px.routes["shop.acme.uz/api"] == "" || !slices.Equal(px.redirects, []string{"shop.acme.uz/api"}) {
		t.Fatalf("%+v %+v asked %q routes %v redirects %v", res, data, asked, px.routes, px.redirects)
	}
	d.Resolver = fakeResolver{}
	res = d.Handle(context.Background(), cmd(t, proto.CmdEnsureDomain, proto.EnsureDomainArgs{Host: "shop.acme.uz/api", ExpectIP: "5.6.7.8", Container: "c1", Port: 10000}))
	json.Unmarshal(res.Data, &data)
	if data.DNSDetail != "shop.acme.uz has no A record yet. Set one: shop.acme.uz → 5.6.7.8." {
		t.Fatalf("%+v", data)
	}
}

func TestDomainDNSOwnAAAA(t *testing.T) {
	d, _ := domainDispatcher(t, []string{"5.6.7.8", "2a01:4f8::1"}, true)
	d.OwnAddrs = func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("2a01:4f8::1")} }
	if got := ensureDomain(t, d); got.DNS != "ok" {
		t.Fatalf("%+v", got)
	}
	d.OwnAddrs = func() []netip.Addr { return nil }
	if got := ensureDomain(t, d); got.DNS != "wrong" {
		t.Fatalf("an AAAA that is not ours: %+v", got)
	}
}
