package cloud

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/egress"
)

func TestCellGetsHostAddress(t *testing.T) {
	w := newOperator(t)
	ctx := context.Background()
	if err := w.op.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	c := acme()
	c.RefusedHostIP = "203.0.113.1"
	if err := w.op.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	var n int
	w.st.DB.QueryRow(`SELECT COUNT(*) FROM env_vars WHERE key = 'YAWL_HOST_ADDRS'`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d YAWL_HOST_ADDRS", n)
	}

	dns := &fakeDNS{}
	f := newFront(t, func(c *Config) {
		c.CellHost = "{name}.app.useyawl.com"
		c.Servers = []CellServer{{Name: "home", Cap: 1, IP: "203.0.113.1"}, {Name: "b", Cap: 1, IP: "203.0.113.2"}}
		c.DNS = dns
	})
	home := f.person(t, "c1")
	for _, p := range []struct {
		who      *person
		name, ip string
	}{{home, "one", "203.0.113.1"}, {f.person(t, "c2"), "two", "203.0.113.2"}} {
		if res, b := p.who.post("/new", url.Values{"name": {p.name}, "terms": {"on"}}); res.StatusCode != 303 {
			t.Fatalf("%s: %d %s", p.name, res.StatusCode, b)
		}
		if got := f.cells.made[len(f.cells.made)-1]; got.Name != p.name || got.RefusedHostIP != p.ip {
			t.Fatalf("%s: %+v", p.name, got)
		}
	}
	if res, _ := home.post("/me/delete", url.Values{"confirm": {"one"}}); res.StatusCode != 303 || dns.removed != 0 {
		t.Fatalf("delete %d, %d records removed", res.StatusCode, dns.removed)
	}
}

func TestHomeCellReachesItsFront(t *testing.T) {
	f := newFront(t, func(c *Config) {
		c.PublicURL = "https://203.0.113.1"
		c.Servers = []CellServer{{Name: "home", Cap: 1, IP: "203.0.113.1"}}
	})
	req, _ := http.NewRequest("POST", f.srv.URL+"/new", strings.NewReader(url.Values{"name": {"one"}, "terms": {"on"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://203.0.113.1")
	if res, err := f.person(t, "c1").c.Do(req); err != nil || res.StatusCode != 303 {
		t.Fatalf("%v %v", res, err)
	}
	c := f.cells.made[len(f.cells.made)-1]
	hosts, err := egress.HostAddrs(c.RefusedHostIP)
	if err != nil {
		t.Fatal(err)
	}
	was := egress.Host
	egress.Host = hosts
	t.Cleanup(func() { egress.Host = was })
	u, _ := url.Parse(c.BillingURL)
	if err := egress.Public("tcp", net.JoinHostPort(u.Hostname(), "443"), nil); err != nil {
		t.Fatalf("the cell cannot ask its front %s: %v", c.BillingURL, err)
	}
}
