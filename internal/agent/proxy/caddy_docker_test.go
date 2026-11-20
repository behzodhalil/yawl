//go:build docker

package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"encoding/base64"
	"golang.org/x/crypto/bcrypt"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
)

func TestCaddyServes(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	p := NewCaddy(rt, DefaultSocket)
	if err := p.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	rt.EnsureNetwork(ctx, "yawl-test-net-px")
	id, err := rt.Start(ctx, runtime.ContainerSpec{Name: fmt.Sprintf("yawl-test-who-%d", time.Now().UnixNano()), Image: "traefik/whoami", Network: "yawl-test-net-px"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, id) })
	st, _ := rt.Inspect(ctx, id)
	if err := p.SetRoute(ctx, "yawl-test.localhost", []string{st.IP.String() + ":80"}, Options{}); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", "http://127.0.0.1/", nil)
	req.Host = "yawl-test.localhost"
	// whoami may bind a moment after "running" (502 until then), so poll.
	var res *http.Response
	var b []byte
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if res, err = http.DefaultClient.Do(req); err != nil {
			t.Fatal(err)
		}
		b, _ = io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode == 200 || time.Now().After(deadline) {
			break
		}
	}
	if res.StatusCode != 200 {
		t.Fatalf("status %d: %s", res.StatusCode, b)
	}
}

func TestCaddyRedirect(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	p := NewCaddy(rt, DefaultSocket)
	if err := p.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	host := fmt.Sprintf("yawl-redirect-%d.localhost", time.Now().UnixNano())
	t.Cleanup(func() { p.RemoveRoute(ctx, host) })
	if err := p.SetRoute(ctx, host, []string{"127.0.0.1:1"}, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := p.SetRedirect(ctx, host); err != nil {
		t.Fatal(err)
	}
	if err := p.SetRoute(ctx, host, []string{"127.0.0.1:2"}, Options{}); err != nil {
		t.Fatal(err)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("GET", "http://127.0.0.1/x?y=1", nil)
	req.Host = host
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 308 || res.Header.Get("Location") != "https://"+host+"/x?y=1" {
		t.Fatalf("status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
	if err := p.RemoveRoute(ctx, host); err != nil {
		t.Fatal(err)
	}
	if rs, _ := p.Routes(ctx); rs[host] != "" {
		t.Fatal("route left after RemoveRoute")
	}
	res, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode == 308 {
		t.Fatal("redirect left after RemoveRoute")
	}
}

func TestCaddyMetricsLive(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	p := NewCaddy(rt, DefaultSocket)
	if err := p.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.EnableMetrics(ctx); err != nil {
		t.Fatal(err)
	}
	rt.EnsureNetwork(ctx, "yawl-test-net-px")
	id, err := rt.Start(ctx, runtime.ContainerSpec{Name: fmt.Sprintf("yawl-test-who-%d", time.Now().UnixNano()), Image: "traefik/whoami", Network: "yawl-test-net-px"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, id) })
	st, _ := rt.Inspect(ctx, id)
	if err := p.SetRoute(ctx, "yawl-metrics.localhost", []string{st.IP.String() + ":80"}, Options{}); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", "http://127.0.0.1/", nil)
	req.Host = "yawl-metrics.localhost"
	for i := 0; i < 5; i++ {
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	text, err := p.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `caddy_http_request_duration_seconds_count{`) || !strings.Contains(text, `host="yawl-metrics.localhost"`) {
		t.Fatalf("no per-host request metrics in:\n%s", grepLines(text, "caddy_http_request"))
	}
}

func grepLines(text, sub string) string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestCaddyRouteOptions(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	p := NewCaddy(rt, DefaultSocket)
	if err := p.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	rt.EnsureNetwork(ctx, "yawl-test-net-px")
	id, err := rt.Start(ctx, runtime.ContainerSpec{Name: fmt.Sprintf("yawl-test-who-%d", time.Now().UnixNano()), Image: "traefik/whoami", Network: "yawl-test-net-px"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, id) })
	st, _ := rt.Inspect(ctx, id)
	up := st.IP.String() + ":80"
	hash, _ := bcrypt.GenerateFromPassword([]byte("s3cret-pass"), bcrypt.MinCost)
	host := "yawl-opts.localhost"
	t.Cleanup(func() { p.RemoveRoute(ctx, host); p.RemoveRoute(ctx, "www."+host) })
	get := func(h, user, pw string) (int, http.Header, string) {
		req, _ := http.NewRequest("GET", "http://127.0.0.1/x?y=1", nil)
		req.Host = h
		if user != "" {
			req.SetBasicAuth(user, pw)
		}
		c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
			res, err := c.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != 502 || time.Now().After(deadline) {
				return res.StatusCode, res.Header, string(b)
			}
		}
	}
	o := Options{NoIndex: true, BasicUser: "ops", BasicHash: base64.StdEncoding.EncodeToString(hash), Redirect: map[string]string{"www." + host: host}}
	for _, h := range []string{host, "www." + host} {
		if err := p.SetRoute(ctx, h, []string{up}, o); err != nil {
			t.Fatal(err)
		}
	}
	if code, hd, _ := get(host, "ops", "s3cret-pass"); code != 308 || hd.Get("Location") != "https://"+host+"/x?y=1" {
		t.Fatalf("password over http: %d %v", code, hd)
	}
	if code, hd, _ := get("www."+host, "", ""); code != 301 || hd.Get("Location") != "https://"+host+"/x?y=1" {
		t.Fatalf("www: %d %v", code, hd)
	}
	if err := p.SetRoute(ctx, host, []string{up}, Options{NoIndex: true}); err != nil {
		t.Fatal(err)
	}
	if code, hd, _ := get(host, "", ""); code != 200 || hd.Get("X-Robots-Tag") != "noindex" {
		t.Fatalf("noindex: %d %v", code, hd)
	}
	if err := p.SetRoute(ctx, host, []string{up}, Options{Maintenance: "shop"}); err != nil {
		t.Fatal(err)
	}
	if code, _, body := get(host, "", ""); code != 503 || !strings.Contains(body, "shop is down for maintenance") {
		t.Fatalf("maintenance: %d %s", code, body)
	}
}

func TestCaddyAccessLog(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	p := NewCaddy(rt, DefaultSocket)
	if err := p.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.EnsureAccessLog(ctx); err != nil {
		t.Fatal(err)
	}
	rt.EnsureNetwork(ctx, "yawl-test-net-px")
	id, err := rt.Start(ctx, runtime.ContainerSpec{Name: fmt.Sprintf("yawl-test-who-%d", time.Now().UnixNano()), Image: "traefik/whoami", Network: "yawl-test-net-px"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, id) })
	st, _ := rt.Inspect(ctx, id)
	host := fmt.Sprintf("yawl-log-%d.localhost", time.Now().UnixNano())
	if err := p.SetRoute(ctx, host, []string{st.IP.String() + ":80"}, Options{}); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", "http://127.0.0.1/hello?x=1", nil)
	req.Host = host
	req.Header.Set("User-Agent", "Mozilla/5.0 yawl-test")
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
		}
		if b, _ := os.ReadFile(p.AccessLog()); strings.Contains(string(b), `"host":"`+host+`"`) && strings.Contains(string(b), `"User-Agent":["Mozilla/5.0 yawl-test"]`) {
			return
		}
	}
	b, _ := os.ReadFile(p.AccessLog())
	t.Fatalf("no line for %s in %s:\n%.2000s", host, p.AccessLog(), b)
}

func TestCaddyStatusPage(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	p := NewCaddy(rt, DefaultSocket)
	if err := p.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	dir := "/run/yawl/status"
	os.MkdirAll(dir, 0o755)
	file := dir + "/sp_dockertest.html"
	host := "yawl-status.localhost"
	t.Cleanup(func() { p.RemoveRoute(ctx, host); os.Remove(file) })
	if err := p.SetRoute(ctx, host, nil, Options{Status: file}); err != nil {
		t.Fatal(err)
	}
	get := func() (int, http.Header, string) {
		req, _ := http.NewRequest("GET", "http://127.0.0.1/anything?x=1", nil)
		req.Host = host
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header, string(b)
	}
	if code, _, _ := get(); code != 404 {
		t.Fatalf("no file yet: %d", code)
	}
	for _, body := range []string{"<p>one</p>", "<p>two</p>"} {
		os.WriteFile(file, []byte(body), 0o644)
		code, hd, got := get()
		if code != 200 || got != body || hd.Get("Cache-Control") != "public, max-age=30" || !strings.Contains(hd.Get("Content-Security-Policy"), "default-src 'none'") {
			t.Fatalf("%d %v %q", code, hd, got)
		}
	}
}
