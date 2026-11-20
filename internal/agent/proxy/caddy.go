package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
)

type Proxy interface {
	Ensure(ctx context.Context) error
	SetRoute(ctx context.Context, host string, upstreams []string, o Options) error
	Routes(ctx context.Context) (map[string]string, error)
	RemoveRoute(ctx context.Context, host string) error
	SetRedirect(ctx context.Context, host string) error
}

type Options struct {
	NoIndex     bool
	Maintenance string
	BasicUser   string
	BasicHash   string
	Redirect    map[string]string
	Status      string
}

const (
	ContainerName = "yawl-caddy"
	Image         = "caddy:2"
	DefaultSocket = "/run/yawl/caddy-admin.sock"
)

var servers = []string{"https", "http"}

type Caddy struct {
	rt                 runtime.Runtime
	socket             string
	hc                 *http.Client
	Image              string
	Wildcard, DNSToken string
}

func NewCaddy(rt runtime.Runtime, socket string) *Caddy {
	return &Caddy{rt: rt, socket: socket, hc: &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}},
	}}
}

func initialConfig(socket string) []byte {
	b, _ := json.Marshal(map[string]any{
		"admin": map[string]any{"listen": "unix/" + socket},
		"apps": map[string]any{"http": map[string]any{"servers": map[string]any{
			"https": map[string]any{"listen": []string{":443"}, "routes": []any{},
				"automatic_https": map[string]any{"disable_redirects": true}},
			"http": map[string]any{"listen": []string{":80"}, "routes": []any{},
				"automatic_https": map[string]any{"disable": true}},
		}}},
	})
	return b
}

// Ensure starts the Caddy container unless it is already running. Its config
// autosaves to the data volume, so routes survive a Caddy restart (--resume).
func (c *Caddy) Ensure(ctx context.Context) error {
	if st, err := c.rt.Inspect(ctx, ContainerName); err == nil && st.Running {
		return nil
	} else if err == nil {
		c.rt.Remove(ctx, ContainerName)
	}
	dir := filepath.Dir(c.socket)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "caddy.json"), initialConfig(c.socket), 0o640); err != nil {
		return err
	}
	if _, err := c.rt.Start(ctx, c.spec(dir)); err != nil {
		return err
	}
	for i := 0; i < 50; i++ {
		if _, err := c.Routes(ctx); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("caddy admin: socket %s never answered", c.socket)
}

// spec is Caddy's container. The DNS token goes in its environment, which
// the policy names, so it is never in the config Caddy saves to disk.
func (c *Caddy) spec(dir string) runtime.ContainerSpec {
	s := runtime.ContainerSpec{
		Name: ContainerName, Image: Image, HostNetwork: true,
		Cmd: []string{"caddy", "run", "--config", filepath.Join(dir, "caddy.json"), "--resume"},
		Mounts: []runtime.Mount{
			{Source: dir, Target: dir},
			{Source: "yawl-caddy-data", Target: "/data", Volume: true},
			{Source: "yawl-caddy-config", Target: "/config", Volume: true},
		},
	}
	if c.Image != "" {
		s.Image = c.Image
	}
	if c.Wildcard != "" && c.DNSToken != "" {
		s.Env = []string{"CLOUDFLARE_API_TOKEN=" + c.DNSToken}
	}
	return s
}

const wildcardID = "wildcard"

// EnsureWildcard has Caddy get one certificate for Wildcard by ACME DNS-01
// through Cloudflare and use it for every host under it, none of which then
// gets one of its own (record 67, S9). The tls app is Yawl's alone: it is
// set whole. A route that matches the wildcard and does nothing shows
// automatic HTTPS the name; it is not terminal, so every host's own route
// still answers. Off (no Wildcard or DNSToken), a wildcard left in Caddy's
// saved config is taken out, since its token is gone and it could not renew.
func (c *Caddy) EnsureWildcard(ctx context.Context) error {
	if c.Wildcard == "" || c.DNSToken == "" {
		code, err := c.do(ctx, http.MethodDelete, "/id/"+wildcardID, nil)
		if err != nil || code >= 300 {
			return err
		}
		if code, err = c.do(ctx, http.MethodDelete, "/config/apps/tls", nil); err != nil || code >= 300 {
			return fmt.Errorf("caddy admin: removing the wildcard's policy returned %d: %v", code, err)
		}
		_, err = c.do(ctx, http.MethodPost, "/config/apps/http/servers/https/automatic_https/prefer_wildcard", false)
		return err
	}
	issuer := map[string]any{"module": "acme", "challenges": map[string]any{"dns": map[string]any{
		"provider": map[string]any{"name": "cloudflare", "api_token": "{env.CLOUDFLARE_API_TOKEN}"}}}}
	tls := map[string]any{"automation": map[string]any{"policies": []any{map[string]any{"subjects": []string{c.Wildcard}, "issuers": []any{issuer}}}}}
	route := map[string]any{"@id": wildcardID, "match": []any{map[string]any{"host": []string{c.Wildcard}}}}
	for _, step := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/config/apps/tls", tls},
		{http.MethodPost, "/config/apps/http/servers/https/automatic_https/prefer_wildcard", true},
		{http.MethodPatch, "/id/" + wildcardID, route},
	} {
		code, text, err := c.send(ctx, step.method, step.path, step.body)
		if err == nil && code == http.StatusNotFound && step.method == http.MethodPatch {
			code, text, err = c.send(ctx, http.MethodPost, "/config/apps/http/servers/https/routes", route)
		}
		if err != nil {
			return err
		}
		if code >= 300 {
			return fmt.Errorf("caddy admin: %s %s returned %d: %s", step.method, step.path, code, text)
		}
	}
	return nil
}

func (c *Caddy) send(ctx context.Context, method, path string, body any) (int, string, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, method, "http://caddy"+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	res, err := c.hc.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("caddy admin: %w", err)
	}
	defer res.Body.Close()
	text, _ := io.ReadAll(io.LimitReader(res.Body, 2<<10))
	return res.StatusCode, strings.TrimSpace(string(text)), nil
}

// A "domains" entry is a host or a host and a path, acme.uz/api (design
// record 65, R1). An id keeps its path with ~ for /, which no host or path
// has, so it stays one segment of the admin API's /id/ and unique per entry.
func routeID(server, host string) string {
	return "route-" + server + "-" + strings.ReplaceAll(host, "/", "~")
}

func match(entry string) []any {
	host, path, ok := strings.Cut(entry, "/")
	m := map[string]any{"host": []string{host}}
	if ok {
		m["path"] = []string{"/" + path, "/" + path + "/*"}
	}
	return []any{m}
}

// SetRoute points host at upstreams (a service's copies, record 43) on both
// servers. Caddy applies a config change gracefully, so requests already in
// flight finish on the old upstream. A new route with a path goes in at the
// front, ahead of every host's own (record 65, R3); one already there keeps
// its place, and a host's own route is always added at the end.
func (c *Caddy) SetRoute(ctx context.Context, host string, upstreams []string, o Options) error {
	for _, srv := range servers {
		route := map[string]any{
			"@id":      routeID(srv, host),
			"match":    match(host),
			"handle":   handleFor(srv, host, upstreams, o),
			"terminal": true,
		}
		code, err := c.do(ctx, http.MethodPatch, "/id/"+routeID(srv, host), route)
		if err != nil {
			return err
		}
		if code == http.StatusNotFound {
			method, at := http.MethodPost, "/config/apps/http/servers/"+srv+"/routes"
			if strings.Contains(host, "/") { // PUT on an index inserts there
				method, at = http.MethodPut, at+"/0"
			}
			if code, err = c.do(ctx, method, at, route); err != nil {
				return err
			}
		}
		if code >= 300 {
			return fmt.Errorf("caddy admin: setting route for %s on %s returned %d", host, srv, code)
		}
	}
	return nil
}

func handleFor(srv, host string, upstreams []string, o Options) []any {
	if to := o.Redirect[host]; to != "" {
		return redirectHandle(to)
	}
	if srv == "http" && o.BasicHash != "" {
		return []any{map[string]any{"handler": "static_response", "status_code": 308,
			"headers": map[string]any{"Location": []string{"https://{http.request.host}{http.request.uri}"}}}}
	}
	h := routeHandle(upstreams, o)
	_, path, ok := strings.Cut(host, "/")
	if !ok || o.Maintenance != "" {
		return h
	}
	rp := h[len(h)-1].(map[string]any)
	rp["headers"] = map[string]any{"request": map[string]any{"set": map[string]any{"X-Forwarded-Prefix": []string{"/" + path}}}}
	return append([]any{map[string]any{"handler": "rewrite", "strip_path_prefix": "/" + path}}, h...)
}

func routeHandle(upstreams []string, o Options) []any {
	if o.Status != "" {
		return statusHandle(o.Status)
	}
	if o.Maintenance != "" {
		return []any{map[string]any{"handler": "static_response", "status_code": 503,
			"headers": map[string]any{"Content-Type": []string{"text/html; charset=utf-8"}, "Retry-After": []string{"300"}},
			"body":    "<!doctype html><title>Maintenance</title><p style=\"font:16px system-ui;margin:20vh auto;max-width:32em\">" + html.EscapeString(o.Maintenance) + " is down for maintenance. It will be back soon.</p>"}}
	}
	var h []any
	if o.BasicUser != "" && o.BasicHash != "" {
		h = append(h, map[string]any{"handler": "authentication", "providers": map[string]any{"http_basic": map[string]any{
			"hash": map[string]any{"algorithm": "bcrypt"}, "hash_cache": map[string]any{}, // bcrypt once per password, not per request (Plan 38 minor)
			"accounts": []any{map[string]any{"username": o.BasicUser, "password": o.BasicHash}}}}})
	}
	if o.NoIndex {
		h = append(h, map[string]any{"handler": "headers", "response": map[string]any{"set": map[string]any{"X-Robots-Tag": []string{"noindex"}}}})
	}
	var ups []any
	for _, u := range upstreams {
		ups = append(ups, map[string]any{"dial": u})
	}
	rp := map[string]any{"handler": "reverse_proxy", "upstreams": ups}
	if len(ups) > 1 {
		// Round robin over the copies; one that refuses is marked down for 10 s
		// and the request tried on another for up to 5 s (record 43, R3). Not
		// for one copy: marking the only upstream down would refuse everyone.
		rp["load_balancing"] = map[string]any{"selection_policy": map[string]any{"policy": "round_robin"}, "try_duration": "5s"}
		rp["health_checks"] = map[string]any{"passive": map[string]any{"fail_duration": "10s"}}
	}
	return append(h, rp)
}

func statusHandle(file string) []any {
	return []any{
		map[string]any{"handler": "headers", "response": map[string]any{"set": map[string]any{
			"Cache-Control":           []string{"public, max-age=30"},
			"Content-Security-Policy": []string{"default-src 'none'; style-src 'unsafe-inline'"},
			"X-Content-Type-Options":  []string{"nosniff"},
			"Referrer-Policy":         []string{"no-referrer"},
		}}},
		map[string]any{"handler": "rewrite", "uri": "/" + filepath.Base(file)},
		map[string]any{"handler": "file_server", "root": filepath.Dir(file)},
	}
}

// redirectHandle sends a host to another, path and query kept (www, record 38).
// A 301 is kept by browsers with no end; an hour's max-age lets them follow
// the choice when it flips (Plan 38 minor).
func redirectHandle(to string) []any {
	return []any{map[string]any{"handler": "static_response", "status_code": 301,
		"headers": map[string]any{"Location": []string{"https://" + to + "{http.request.uri}"}, "Cache-Control": []string{"max-age=3600"}}}}
}

func redirectID(host string) string { return "redirect-" + strings.ReplaceAll(host, "/", "~") }

func (c *Caddy) SetRedirect(ctx context.Context, host string) error {
	route := map[string]any{
		"@id":   redirectID(host),
		"match": match(host),
		"handle": []any{map[string]any{"handler": "static_response", "status_code": 308,
			"headers": map[string]any{"Location": []string{"https://{http.request.host}{http.request.uri}"}}}},
		"terminal": true,
	}
	code, err := c.do(ctx, http.MethodPatch, "/id/"+redirectID(host), route)
	if err == nil && code == http.StatusNotFound { // PUT on an index inserts there: ahead of every route
		code, err = c.do(ctx, http.MethodPut, "/config/apps/http/servers/http/routes/0", route)
	}
	if err != nil {
		return err
	}
	if code >= 300 {
		return fmt.Errorf("caddy admin: setting redirect for %s returned %d", host, code)
	}
	return nil
}

func (c *Caddy) RemoveRoute(ctx context.Context, host string) error {
	for _, id := range []string{routeID("https", host), routeID("http", host), redirectID(host)} {
		code, err := c.do(ctx, http.MethodDelete, "/id/"+id, nil)
		if err != nil {
			return err
		}
		if code >= 300 && code != http.StatusNotFound {
			return fmt.Errorf("caddy admin: removing %s returned %d", id, code)
		}
	}
	return nil
}

func (c *Caddy) Routes(ctx context.Context) (map[string]string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://caddy/config/apps/http/servers/https/routes", nil)
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("caddy admin: %w", err)
	}
	defer res.Body.Close()
	var routes []struct {
		Match  []struct{ Host []string }
		Handle []struct{ Upstreams []struct{ Dial string } }
	}
	if err := json.NewDecoder(res.Body).Decode(&routes); err != nil {
		return nil, fmt.Errorf("caddy admin: %w", err)
	}
	out := map[string]string{}
	for _, r := range routes {
		if len(r.Match) > 0 && len(r.Match[0].Host) > 0 && len(r.Handle) > 0 && len(r.Handle[0].Upstreams) > 0 {
			out[r.Match[0].Host[0]] = r.Handle[0].Upstreams[0].Dial
		}
	}
	return out, nil
}

func (c *Caddy) do(ctx context.Context, method, path string, body any) (int, error) {
	code, _, err := c.send(ctx, method, path, body)
	return code, err
}

func (c *Caddy) EnableMetrics(ctx context.Context) error {
	code, err := c.do(ctx, http.MethodPost, "/config/apps/http/metrics", map[string]any{"per_host": true})
	if err != nil {
		return err
	}
	if code/100 != 2 {
		return fmt.Errorf("caddy admin: turning on metrics returned %d", code)
	}
	return nil
}

func (c *Caddy) Metrics(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://caddy/metrics", nil)
	res, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("caddy admin: %w", err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if res.StatusCode != 200 {
		return "", fmt.Errorf("caddy admin: /metrics returned %d", res.StatusCode)
	}
	return string(b), err
}

var _ Proxy = (*Caddy)(nil)

func (c *Caddy) AccessLog() string { return filepath.Join(filepath.Dir(c.socket), "access.log") }

func (c *Caddy) EnsureAccessLog(ctx context.Context) error {
	const name = "http.log.access.yawl"
	logging := map[string]any{"logs": map[string]any{
		"default": map[string]any{"exclude": []string{name}},
		"yawl-access": map[string]any{
			"writer":  map[string]any{"output": "file", "filename": c.AccessLog(), "roll_size_mb": 5, "roll_keep": 1},
			"encoder": map[string]any{"format": "json"},
			"include": []string{name},
		},
	}}
	if code, err := c.do(ctx, http.MethodPost, "/config/logging", logging); err != nil {
		return err
	} else if code >= 300 {
		return fmt.Errorf("caddy admin: setting the access log returned %d", code)
	}
	for _, srv := range servers {
		code, err := c.do(ctx, http.MethodPost, "/config/apps/http/servers/"+srv+"/logs", map[string]any{"default_logger_name": "yawl"})
		if err != nil {
			return err
		}
		if code >= 300 {
			return fmt.Errorf("caddy admin: logging %s returned %d", srv, code)
		}
	}
	return nil
}
