package analytics

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func line(host, ip, ua, method, uri string, status int, ctype, ref string, size int) string {
	return fmt.Sprintf(`{"level":"info","logger":"http.log.access.yawl","msg":"handled request","request":{"client_ip":%q,"method":%q,"host":%q,"uri":%q,"headers":{"User-Agent":[%q],"Referer":[%q]}},"size":%d,"status":%d,"resp_headers":{"Content-Type":[%q]}}`,
		ip, method, host, uri, ua, ref, size, status, ctype)
}

func TestCounter(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	c := New()
	ff := "Mozilla/5.0 Firefox"
	for _, l := range []string{
		line("shop.example", "1.1.1.1", ff, "GET", "/?utm=x", 200, "text/html; charset=utf-8", "https://news.example/item?id=1", 1000),
		line("shop.example", "1.1.1.1", ff, "GET", "/cart", 200, "text/html", "https://shop.example/", 500),
		line("shop.example", "2.2.2.2", ff, "GET", "/", 200, "text/html", "", 1000),
		line("shop.example", "3.3.3.3", "Googlebot/2.1", "GET", "/", 200, "text/html", "", 1000),
		line("shop.example", "2.2.2.2", ff, "GET", "/app.js", 200, "application/javascript", "", 3000),
		line("shop.example", "2.2.2.2", ff, "POST", "/cart", 302, "", "", 0),
		line("shop.example", "4.4.4.4", ff, "GET", "/missing", 404, "text/html", "", 10),
		line("shop.example", "4.4.4.4", ff, "GET", "/boom", 502, "text/html", "", 10),
		"not json", `{"logger":"http.log.error"}`,
	} {
		c.Line(l, now)
	}
	got := c.Take(now)
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	v := got[0]
	if v.Host != "shop.example" || v.S2 != 5 || v.S3 != 1 || v.S4 != 1 || v.S5 != 1 || v.Bytes != 6520 {
		t.Fatalf("%+v", v)
	}
	if v.Views != 3 || v.Visitors != 2 || v.Day != "2026-10-30" {
		t.Fatalf("%+v", v)
	}
	if v.Paths["/"] != 2 || v.Paths["/cart"] != 1 || v.Refs["news.example"] != 1 || len(v.Refs) != 1 {
		t.Fatalf("%+v %+v", v.Paths, v.Refs)
	}
	c.Line(line("shop.example", "1.1.1.1", ff, "GET", "/", 200, "text/html", "", 1), now.Add(time.Minute))
	if g := c.Take(now.Add(time.Minute)); g[0].Views != 1 || g[0].Visitors != 2 {
		t.Fatalf("%+v", g)
	}
	next := now.Add(24 * time.Hour)
	c.Line(line("shop.example", "1.1.1.1", ff, "GET", "/", 200, "text/html", "", 1), next)
	if g := c.Take(next); g[0].Visitors != 1 || g[0].Day != "2026-10-31" {
		t.Fatalf("%+v", g)
	}
	if g := c.Take(next); len(g) != 0 {
		t.Fatalf("an idle minute: %+v", g)
	}
}

func TestVisitorCap(t *testing.T) {
	defer func(c int) { visitorsExactBelow = c }(visitorsExactBelow)
	visitorsExactBelow = 3
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	c := New()
	for i := 0; i < 10; i++ {
		c.Line(line("a.example", "1.1.1.1", fmt.Sprintf("Mozilla/%d", i), "GET", "/", 200, "text/html", "", 1), now)
	}
	if g := c.Take(now); g[0].Visitors != 3 || g[0].Views != 10 {
		t.Fatalf("%+v", g[0])
	}
}

func TestFollowRolled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	os.WriteFile(path, []byte("before\n"), 0o600)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan string, 10)
	go Follow(ctx, path, 50*time.Millisecond, func(l string) { got <- l })
	time.Sleep(120 * time.Millisecond)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("last of old\n")
	f.Close()
	os.Rename(path, filepath.Join(dir, "access-1.log"))
	os.WriteFile(path, []byte("first of new, a longer line than the old file was\n"), 0o600)
	var lines []string
	for deadline := time.After(2 * time.Second); len(lines) < 2; {
		select {
		case l := <-got:
			lines = append(lines, l)
		case <-deadline:
			t.Fatalf("%q", lines)
		}
	}
	if lines[0] != "last of old" || !strings.HasPrefix(lines[1], "first of new") {
		t.Fatalf("%q", lines)
	}
}

func TestCounterMidnight(t *testing.T) {
	c := New()
	ff := "Mozilla/5.0 Firefox"
	c.Line(line("shop.example", "1.1.1.1", ff, "GET", "/", 200, "text/html", "", 10), time.Date(2026, 10, 30, 23, 59, 40, 0, time.UTC))
	got := c.Take(time.Date(2026, 10, 31, 0, 0, 5, 0, time.UTC))
	if len(got) != 1 || got[0].Day != "2026-10-30" || got[0].Visitors != 1 || got[0].Views != 1 {
		t.Fatalf("%+v", got)
	}
	c.Line(line("shop.example", "1.1.1.1", ff, "GET", "/", 200, "text/html", "", 10), time.Date(2026, 10, 30, 23, 59, 50, 0, time.UTC))
	c.Line(line("shop.example", "2.2.2.2", ff, "GET", "/", 200, "text/html", "", 10), time.Date(2026, 10, 31, 0, 0, 10, 0, time.UTC))
	got = c.Take(time.Date(2026, 10, 31, 0, 1, 0, 0, time.UTC))
	if len(got) != 2 || got[0].Day != "2026-10-30" || got[0].Views != 1 || got[1].Day != "2026-10-31" || got[1].Visitors != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestCounterSkipsHTTPSRedirect(t *testing.T) {
	c := New()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	c.Line(`{"logger":"http.log.access.yawl","request":{"client_ip":"1.1.1.1","method":"GET","host":"shop.example","uri":"/"},"size":0,"status":308,"resp_headers":{"Location":["https://shop.example/"]}}`, now)
	if got := c.Take(now); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestKeepRetries(t *testing.T) {
	n := 0
	Keep(context.Background(), func(context.Context) error {
		if n++; n < 3 {
			return fmt.Errorf("caddy is starting")
		}
		return nil
	}, time.Millisecond)
	if n != 3 {
		t.Fatalf("%d tries", n)
	}
}
