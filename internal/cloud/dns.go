package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DNS interface {
	SetA(ctx context.Context, host, ip string) error
	Remove(ctx context.Context, host, ip string) error
}

const cellMark = "yawl cloud cell"

var errNotOurs = errors.New("a DNS record yawl did not make has this name")

type Cloudflare struct {
	API, Token, Zone string
	HTTP             *http.Client
}

func (c Cloudflare) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.API, "/")+"/zones/"+url.PathEscape(c.Zone)+"/dns_records"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare: %w", err)
	}
	defer res.Body.Close()
	var v struct {
		Success bool
		Errors  []struct{ Message string }
		Result  json.RawMessage
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&v); err != nil || !v.Success {
		var why []string
		for _, e := range v.Errors {
			why = append(why, e.Message)
		}
		return fmt.Errorf("cloudflare answered %d: %s", res.StatusCode, strings.Join(why, "; "))
	}
	if out != nil {
		return json.Unmarshal(v.Result, out)
	}
	return nil
}

type record struct{ ID, Type, Content, Comment string }

func (c Cloudflare) records(ctx context.Context, host string) ([]record, error) {
	var recs []record
	return recs, c.do(ctx, "GET", "?name="+url.QueryEscape(host), nil, &recs)
}

func (r record) ours(ip string) bool {
	return r.Type == "A" && (r.Comment == cellMark || r.Content == ip)
}

// Not proxied: Caddy on the cell server answers TLS itself.
func (c Cloudflare) SetA(ctx context.Context, host, ip string) error {
	recs, err := c.records(ctx, host)
	if err != nil {
		return err
	}
	id := ""
	for _, r := range recs {
		if !r.ours(ip) {
			return fmt.Errorf("%s: %w", host, errNotOurs)
		}
		id = r.ID
	}
	rec := map[string]any{"type": "A", "name": host, "content": ip, "ttl": 1, "proxied": false, "comment": cellMark}
	if id == "" {
		return c.do(ctx, "POST", "", rec, nil)
	}
	return c.do(ctx, "PUT", "/"+url.PathEscape(id), rec, nil)
}

func (c Cloudflare) Remove(ctx context.Context, host, ip string) error {
	recs, err := c.records(ctx, host)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if !r.ours(ip) {
			err = fmt.Errorf("%s: %w; left as it is", host, errNotOurs)
			continue
		}
		if err := c.do(ctx, "DELETE", "/"+url.PathEscape(r.ID), nil, nil); err != nil {
			return err
		}
	}
	return err
}
