package cloud

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestCellNameRefusesForeignRecord(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	recs := map[string]map[string]any{
		"rec-docs": {"name": "docs.app.useyawl.com", "type": "A", "content": "198.51.100.7"},
		"rec-web":  {"name": "web.app.useyawl.com", "type": "AAAA", "content": "2001:db8::1"},
		"rec-old":  {"name": "old.app.useyawl.com", "type": "A", "content": "203.0.113.2"},
	}
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		id := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/zones/z/dns_records"), "/")
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		list := []any{}
		switch q := r.URL.Query(); r.Method {
		case "GET":
			for id, rec := range recs {
				if rec["name"] == q.Get("name") && (q.Get("type") == "" || q.Get("type") == rec["type"]) {
					list = append(list, map[string]any{"id": id, "type": rec["type"], "content": rec["content"], "comment": rec["comment"]})
				}
			}
		case "POST":
			recs[fmt.Sprint("new-", len(recs))] = in
		case "PUT":
			recs[id] = in
		case "DELETE":
			delete(recs, id)
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": list})
	}))
	t.Cleanup(cf.Close)
	f := newFront(t, func(c *Config) {
		c.CellHost = "{name}.app.useyawl.com"
		c.Servers = []CellServer{{Name: "home", Cap: 0}, {Name: "b", Cap: 5, IP: "203.0.113.2"}}
		c.DNS = Cloudflare{API: cf.URL, Token: "tok", Zone: "z"}
	})
	p := f.person(t, "c1")
	for _, name := range []string{"docs", "web"} {
		if res, page := p.post("/new", url.Values{"name": {name}, "terms": {"on"}}); res.StatusCode != 400 || !strings.Contains(page, name+" is taken.") {
			t.Fatalf("%s: %d %s", name, res.StatusCode, page)
		}
		if _, has := f.st(t).cellOf(1); has {
			t.Fatalf("%s kept for the account", name)
		}
	}
	mu.Lock()
	if len(calls) != 2 || calls[0] != "GET /zones/z/dns_records" || calls[1] != calls[0] || len(f.cells.made) != 0 {
		t.Fatalf("%v %v", calls, f.cells.made)
	}
	mu.Unlock()

	if res, _ := p.post("/new", url.Values{"name": {"old"}, "terms": {"on"}}); res.StatusCode != 303 {
		t.Fatalf("old: %d", res.StatusCode)
	}
	mu.Lock()
	if old := recs["rec-old"]; old["comment"] != cellMark || old["content"] != "203.0.113.2" {
		t.Fatalf("%v", recs)
	}
	recs["rec-more"] = map[string]any{"name": "old.app.useyawl.com", "type": "A", "content": "198.51.100.8"}
	mu.Unlock()
	if res, _ := p.post("/me/delete", url.Values{"confirm": {"old"}}); res.StatusCode != 303 {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := recs["rec-old"]; ok || recs["rec-more"] == nil || recs["rec-docs"] == nil || recs["rec-web"] == nil ||
		slices.Contains(calls, "DELETE /zones/z/dns_records/rec-more") {
		t.Fatalf("%v %v", calls, recs)
	}
}
