package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestPlacement(t *testing.T) {
	st, err := openStore(t.TempDir() + "/cloud.db")
	if err != nil {
		t.Fatal(err)
	}
	servers := []CellServer{{Name: "home", Cap: 2}, {Name: "b", Cap: 4, IP: "203.0.113.2"}, {Name: "drained", Cap: 0, IP: "203.0.113.3"}}
	var got []string
	for i := range 6 {
		acct, _ := st.account(int64(100+i), fmt.Sprint("p", i), "")
		srv, err := st.reserve(fmt.Sprintf("cell%d", i), acct, "tok", "0.1.0", 50, "", false, servers)
		if err != nil {
			t.Fatalf("cell%d: %v", i, err)
		}
		got = append(got, srv)
	}
	if strings.Join(got, ",") != "home,b,b,home,b,b" {
		t.Fatalf("%v", got)
	}
	acct, _ := st.account(200, "late", "")
	if _, err := st.reserve("late", acct, "tok", "0.1.0", 50, "", false, servers); err != errFull {
		t.Fatalf("all full: %v", err)
	}
	if c, _ := st.cellOf(acct); c.Name != "" {
		t.Fatal("a refused cell was kept")
	}
	n, _ := st.perServer()
	if n["home"] != 2 || n["b"] != 4 || n["drained"] != 0 {
		t.Fatalf("%v", n)
	}
}

func TestPlacementOldCells(t *testing.T) {
	data := t.TempDir()
	st, err := openStore(data + "/cloud.db")
	if err != nil {
		t.Fatal(err)
	}
	acct, _ := st.account(1, "old", "")
	st.db.Exec(`INSERT INTO cells (name, account_id, made, created_at) VALUES ('old', ?, 1, 0)`, acct)
	st.db.Exec(`UPDATE cells SET server = ''`)
	st.db.Close()
	f := newFront(t, func(c *Config) {
		c.Data = data
		c.Servers = []CellServer{{Name: "srv1", Cap: 1}, {Name: "b", Cap: 5, IP: "203.0.113.2"}}
		c.DNS = &fakeDNS{}
	})
	if c, _ := f.st(t).cellOf(acct); c.Server != "srv1" {
		t.Fatalf("%q", c.Server)
	}
}

type fakeDNS struct {
	mu      sync.Mutex
	recs    map[string]string
	fail    error
	removed int
}

func (d *fakeDNS) SetA(_ context.Context, host, ip string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail != nil {
		return d.fail
	}
	if d.recs == nil {
		d.recs = map[string]string{}
	}
	d.recs[host] = ip
	return nil
}

func (d *fakeDNS) Remove(_ context.Context, host, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.removed++
	delete(d.recs, host)
	return nil
}

func TestCellsOnServers(t *testing.T) {
	dns := &fakeDNS{}
	f := newFront(t, func(c *Config) {
		c.CellHost = "{name}.app.useyawl.com"
		c.Servers = []CellServer{{Name: "home", Cap: 1}, {Name: "b", Cap: 1, IP: "203.0.113.2"}}
		c.DNS = dns
	})
	p1, p2 := f.person(t, "c1"), f.person(t, "c2")
	for i, p := range []*person{p1, p2} {
		n := []string{"acme", "beta"}[i]
		if res, _ := p.post("/new", url.Values{"name": {n}, "terms": {"on"}}); res.StatusCode != 303 {
			t.Fatalf("%s: %d", n, res.StatusCode)
		}
	}
	on := map[string]string{}
	for _, c := range f.cells.made {
		on[c.Name] = c.Server
	}
	if on["acme"] != "home" || on["beta"] != "b" || len(dns.recs) != 1 || dns.recs["beta.app.useyawl.com"] != "203.0.113.2" {
		t.Fatalf("%v %v", on, dns.recs)
	}
	if res, body := f.person(t, "c3").post("/new", url.Values{"name": {"gamma"}, "terms": {"on"}}); res.StatusCode != 400 || !strings.Contains(body, "Yawl Cloud is full for now.") {
		t.Fatalf("%d", res.StatusCode)
	}
	req, _ := http.NewRequest("GET", f.srv.URL+"/admin/servers", nil)
	req.Header.Set("Authorization", "Bearer admin-tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(b) != `[{"name":"home","cells":1,"cap":1},{"name":"b","cells":1,"cap":1}]`+"\n" {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, _ := p2.post("/me/delete", url.Values{"confirm": {"beta"}}); res.StatusCode != 303 || len(dns.recs) != 0 {
		t.Fatalf("%d %v", res.StatusCode, dns.recs)
	}
}

func TestCellDNSFails(t *testing.T) {
	dns := &fakeDNS{fail: fmt.Errorf("cloudflare is down")}
	f := newFront(t, func(c *Config) {
		c.Servers = []CellServer{{Name: "home", Cap: 0}, {Name: "b", Cap: 5, IP: "203.0.113.2"}}
		c.DNS = dns
	})
	p := f.person(t, "c1")
	if res, body := p.post("/new", url.Values{"name": {"acme"}, "terms": {"on"}}); res.StatusCode != 400 || !strings.Contains(body, "could not be made just now") || len(f.cells.made) != 0 {
		t.Fatalf("%d %d", res.StatusCode, len(f.cells.made))
	}
	dns.fail = nil
	if res, _ := p.post("/new", url.Values{"name": {"acme"}, "terms": {"on"}}); res.StatusCode != 303 || len(f.cells.made) != 1 || dns.recs["panel-acme.198-51-100-20.sslip.io"] != "203.0.113.2" {
		t.Fatalf("%d %v", res.StatusCode, dns.recs)
	}
}

func TestFrontServersNeedDNS(t *testing.T) {
	base := Config{PublicURL: "https://cloud.test", Data: t.TempDir(), CellHost: "{name}.app.useyawl.com", Image: "registry.test/yawl:0.1.0", MaxCells: 50, Cells: &fakeCells{}}
	for _, c := range []struct {
		servers []CellServer
		dns     DNS
		want    string
	}{
		{[]CellServer{{Name: "home", Cap: 5}, {Name: "b", Cap: 5, IP: "203.0.113.2"}}, nil, "YAWL_CLOUD_DNS_TOKEN"},
		{[]CellServer{{Name: "home", Cap: 5}, {Name: "b", Cap: 5}}, &fakeDNS{}, "b has no address"},
		{[]CellServer{{Name: "home", Cap: 5}, {Name: "home", Cap: 5, IP: "203.0.113.2"}}, &fakeDNS{}, "home is listed twice"},
	} {
		cfg := base
		cfg.Servers, cfg.DNS = c.servers, c.dns
		if _, err := NewFront(cfg); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v", c.servers, err)
		}
	}
}

func TestConfigServers(t *testing.T) {
	env := map[string]string{
		"YAWL_CLOUD_PUBLIC_URL": "https://cloud.test", "YAWL_CLOUD_CELL_HOST": "{name}.app.useyawl.com", "YAWL_CLOUD_IMAGE": "registry.test/yawl:0.1.0",
		"YAWL_CLOUD_GITHUB_ID": "id", "YAWL_CLOUD_GITHUB_SECRET": "sec", "YAWL_CLOUD_ADMIN_TOKEN": "adm", "YAWL_CLOUD_OPERATOR_URL": "https://op.test",
		"YAWL_CLOUD_OPERATOR_TOKEN": "op", "YAWL_CLOUD_SERVER": "srv1", "YAWL_CLOUD_MAX_CELLS": "20"}
	get := func(k string) string { return env[k] }
	if _, _, err := configFrom(nil, get); err == nil || !strings.Contains(err.Error(), "srv1 has none") {
		t.Fatalf("no address: %v", err)
	}
	env["YAWL_CLOUD_SERVERS"] = "srv1:150@203.0.113.1, cell-b:150@203.0.113.2, old:0@203.0.113.3"
	env["YAWL_CLOUD_DNS_TOKEN"], env["YAWL_CLOUD_DNS_ZONE"] = "cf-tok", "zone1"
	cfg, _, err := configFrom(nil, get)
	if err != nil || fmt.Sprint(cfg.Servers) != "[{srv1 203.0.113.1 150} {cell-b 203.0.113.2 150} {old 203.0.113.3 0}]" {
		t.Fatalf("%v %v", cfg.Servers, err)
	}
	if cf, ok := cfg.DNS.(Cloudflare); !ok || cf.Token != "cf-tok" || cf.Zone != "zone1" || cf.API != "https://api.cloudflare.com/client/v4" {
		t.Fatalf("%+v", cfg.DNS)
	}
	if op := cfg.Cells.(Operator); op.Server != "srv1" {
		t.Fatalf("%q", op.Server)
	}
	delete(env, "YAWL_CLOUD_SERVER")
	if _, _, err := configFrom(nil, get); err != nil {
		t.Fatal(err)
	}
	for v, want := range map[string]string{
		"YAWL_CLOUD_SERVERS=b:5, srv1:5":             "YAWL_CLOUD_SERVER srv1 must be the first",
		"YAWL_CLOUD_SERVERS=srv1:many":               "srv1:many",
		"YAWL_CLOUD_SERVERS=srv1:5@nope":             "srv1:5@nope",
		"YAWL_CLOUD_SERVERS=srv1:5, b:5@2001:db8::1": "b:5@2001:db8::1",
		"YAWL_CLOUD_DNS_ZONE=":                       "YAWL_CLOUD_DNS_ZONE",
	} {
		k, val, _ := strings.Cut(v, "=")
		env2 := map[string]string{}
		for a, b := range env {
			env2[a] = b
		}
		env2["YAWL_CLOUD_SERVER"], env2[k] = "srv1", val
		if _, _, err := configFrom(nil, func(k string) string { return env2[k] }); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", v, err)
		}
	}
}

func TestCloudflareRecords(t *testing.T) {
	var mu sync.Mutex
	recs := map[string]map[string]any{}
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer cf-tok" || !strings.HasPrefix(r.URL.Path, "/zones/zone1/dns_records") {
			w.WriteHeader(403)
			io.WriteString(w, `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`)
			return
		}
		id := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/zones/zone1/dns_records"), "/")
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		var out any
		switch r.Method {
		case "GET":
			list := []any{}
			for id, rec := range recs {
				if q := r.URL.Query(); rec["name"] == q.Get("name") && (q.Get("type") == "" || q.Get("type") == rec["type"]) {
					list = append(list, map[string]any{"id": id, "name": rec["name"], "type": rec["type"], "content": rec["content"], "comment": rec["comment"]})
				}
			}
			out = list
		case "POST":
			id = fmt.Sprint("r", len(recs)+1)
			recs[id] = in
		case "PUT":
			recs[id] = in
		case "DELETE":
			delete(recs, id)
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": out})
	}))
	defer srv.Close()
	cf := Cloudflare{API: srv.URL, Token: "cf-tok", Zone: "zone1"}
	ctx := context.Background()
	if err := cf.SetA(ctx, "beta.app.useyawl.com", "203.0.113.2"); err != nil {
		t.Fatal(err)
	}
	if err := cf.SetA(ctx, "beta.app.useyawl.com", "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs["r1"]["content"] != "203.0.113.9" || recs["r1"]["proxied"] != false || recs["r1"]["type"] != "A" || recs["r1"]["comment"] != cellMark {
		t.Fatalf("%v", recs)
	}
	if err := cf.Remove(ctx, "beta.app.useyawl.com", "203.0.113.9"); err != nil || len(recs) != 0 {
		t.Fatalf("%v %v", err, recs)
	}
	if err := cf.Remove(ctx, "beta.app.useyawl.com", "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	bad := Cloudflare{API: srv.URL, Token: "wrong", Zone: "zone1"}
	if err := bad.SetA(ctx, "x.app.useyawl.com", "203.0.113.2"); err == nil || !strings.Contains(err.Error(), "Authentication error") {
		t.Fatalf("%v", err)
	}
	if calls[0] != "GET /zones/zone1/dns_records" || calls[1] != "POST /zones/zone1/dns_records" {
		t.Fatalf("%v", calls)
	}
}

func TestBackupAt(t *testing.T) {
	seen := map[string]bool{}
	for i := range 1000 {
		at := backupAt(fmt.Sprint("cell", i))
		var h, m int
		if _, err := fmt.Sscanf(at, "%02d:%02d", &h, &m); err != nil || h > 5 || m > 59 || len(at) != 5 {
			t.Fatalf("%q", at)
		}
		seen[at] = true
	}
	if len(seen) < 300 || backupAt("acme") != backupAt("acme") {
		t.Fatalf("%d minutes used of 360", len(seen))
	}
}

func TestCellYAMLServer(t *testing.T) {
	o := Operator{Project: "cells", Server: "home"}
	if y := o.cellYAML(Cell{Name: "acme", Image: "i:1"}); !strings.Contains(y, "server: home\n") {
		t.Fatal(y)
	}
	if y := o.cellYAML(Cell{Name: "acme", Image: "i:1", Server: "cell-b"}); !strings.Contains(y, "server: cell-b\n") || !strings.Contains(y, `backup_at: "00:55"`) {
		t.Fatal(y)
	}
}
