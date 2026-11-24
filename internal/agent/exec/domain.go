package exec

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/behzodhalil/yawl/internal/agent/proxy"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
)

type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

const certWait = 60 * time.Second

// ensureDomain routes a custom domain only once its DNS points here, so the
// certificate Caddy then asks for can be issued (and a wrong record never
// burns Let's Encrypt's rate limit).
func (d *Dispatcher) ensureDomain(ctx context.Context, a proto.EnsureDomainArgs) proto.Result {
	want, err := netip.ParseAddr(a.ExpectIP)
	if err != nil {
		return bad(err)
	}
	r := d.Resolver
	if r == nil {
		r = net.DefaultResolver
	}
	host, _, _ := strings.Cut(a.Host, "/")
	addrs, err := r.LookupNetIP(ctx, "ip", host)
	var dnsErr *net.DNSError
	if err != nil && !(errors.As(err, &dnsErr) && dnsErr.IsNotFound) {
		return ok(proto.EnsureDomainData{DNS: "wrong", Cert: "pending", DNSDetail: fmt.Sprintf("Yawl could not look up %s: %v.", host, err)})
	}
	own := d.OwnAddrs
	if own == nil {
		own = interfaceAddrs
	}
	if detail := dnsWrong(host, want, own(), addrs); detail != "" {
		return ok(proto.EnsureDomainData{DNS: "wrong", Cert: "pending", DNSDetail: detail})
	}
	data := proto.EnsureDomainData{DNS: "ok", Cert: "pending"}
	opts := proxy.Options(a.Options)
	ups := []string{a.Upstream}
	if a.Options.Status != "" {
		if opts.Status = d.statusFile(a.Options.Status); opts.Status == "" {
			return bad(errors.New("a status page id is lowercase letters, digits and _"))
		}
		ups = nil
	} else if a.Upstream == "" {
		if a.Container == "" {
			return ok(data)
		}
		var err error
		if ups, err = d.upstreams(ctx, a.Container, a.Port); err != nil {
			return proto.Result{Cause: err.Error()}
		}
	}
	if err := d.px.SetRoute(ctx, a.Host, ups, opts); err != nil {
		return proto.Result{Cause: err.Error()}
	}
	check := d.CertCheck
	if check == nil {
		check = waitCert
	}
	if check(ctx, host) {
		data.Cert = "issued"
		if err := d.px.SetRedirect(ctx, a.Host); err != nil {
			return proto.Result{Cause: err.Error()}
		}
	}
	return ok(data)
}

func dnsWrong(host string, want netip.Addr, own, addrs []netip.Addr) string {
	if len(addrs) == 0 {
		return fmt.Sprintf("%s has no A record yet. Set one: %s → %s.", host, host, want)
	}
	for _, a := range addrs {
		a = a.Unmap()
		if a == want || slices.Contains(own, a) {
			continue
		}
		if a.Is6() && want.Is4() {
			return fmt.Sprintf("%s has an AAAA record %s that is not this server. Remove it, and keep the A record %s → %s.", host, a, host, want)
		}
		return fmt.Sprintf("%s points to %s. Set an A record: %s → %s.", host, a, host, want)
	}
	return ""
}

func interfaceAddrs() []netip.Addr {
	as, _ := net.InterfaceAddrs()
	var out []netip.Addr
	for _, a := range as {
		if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().IsGlobalUnicast() && !p.Addr().IsPrivate() {
			out = append(out, p.Addr().Unmap())
		}
	}
	return out
}

func (d *Dispatcher) waitCerts(ctx context.Context, hosts []string) string {
	check := d.CertCheck
	if check == nil {
		check = waitCert
	}
	ctx, cancel := context.WithTimeout(ctx, certWait)
	defer cancel()
	var seen, pending []string
	for _, h := range hosts {
		host, _, _ := strings.Cut(h, "/")
		if slices.Contains(seen, host) {
			continue
		}
		seen = append(seen, host)
		if !check(ctx, host) {
			pending = append(pending, host)
		}
	}
	if len(pending) == 0 {
		return ""
	}
	return fmt.Sprintf("Live, but the certificate for %s is not issued yet. HTTPS answers once it is.", strings.Join(pending, ", "))
}

func waitCert(ctx context.Context, host string) bool {
	deadline := time.Now().Add(certWait)
	for {
		dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		conn, err := (&tls.Dialer{Config: &tls.Config{ServerName: host}}).DialContext(dctx, "tcp", "127.0.0.1:443")
		cancel()
		if err == nil {
			conn.Close()
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
}
