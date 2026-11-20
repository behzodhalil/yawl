package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"time"
)

const DefaultAPI = "https://api.cloudflare.com/client/v4"

type Cloudflare struct {
	API, Token, Account string
	HTTP                *http.Client
}

type StepError struct{ Step, Msg string }

func (e *StepError) Error() string { return "Could not " + e.Step + ": " + e.Msg }

type envelope struct {
	Success bool                       `json:"success"`
	Errors  []struct{ Message string } `json:"errors"`
	Result  json.RawMessage            `json:"result"`
}

func (c Cloudflare) do(ctx context.Context, method, path string, body io.Reader, ctype string, out any) error {
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.API+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := hc.Do(req)
	if err != nil {
		if ue, ok := err.(*url.Error); ok {
			return fmt.Errorf("Cloudflare did not answer (%v)", ue.Err)
		}
		return err
	}
	defer res.Body.Close()
	var env envelope
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&env); err != nil {
		return fmt.Errorf("Cloudflare answered %s", res.Status)
	}
	if !env.Success {
		if len(env.Errors) > 0 && env.Errors[0].Message != "" {
			return fmt.Errorf("%s", env.Errors[0].Message)
		}
		return fmt.Errorf("Cloudflare answered %s", res.Status)
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

func (c Cloudflare) acct() string { return "/accounts/" + url.PathEscape(c.Account) }

func (c Cloudflare) Verify(ctx context.Context) error {
	var r struct{ Status string }
	err := c.do(ctx, "GET", "/user/tokens/verify", nil, "", &r)
	if err != nil {
		if err2 := c.do(ctx, "GET", c.acct()+"/tokens/verify", nil, "", &r); err2 != nil {
			return err
		}
	}
	if r.Status != "active" {
		return fmt.Errorf("the token is %s", r.Status)
	}
	return nil
}

func (c Cloudflare) EnsureD1(ctx context.Context, name string) (string, error) {
	var list []struct{ UUID, Name string }
	if err := c.do(ctx, "GET", c.acct()+"/d1/database?name="+url.QueryEscape(name), nil, "", &list); err != nil {
		return "", err
	}
	for _, d := range list {
		if d.Name == name {
			return d.UUID, nil
		}
	}
	var made struct{ UUID string }
	if err := c.do(ctx, "POST", c.acct()+"/d1/database", jsonBody(map[string]string{"name": name}), "application/json", &made); err != nil {
		return "", err
	}
	return made.UUID, nil
}

func (c Cloudflare) Query(ctx context.Context, id, sql string) error {
	return c.do(ctx, "POST", c.acct()+"/d1/database/"+url.PathEscape(id)+"/query", jsonBody(map[string]string{"sql": sql}), "application/json", nil)
}

// PutScript uploads the module with its D1 binding, secrets and plain
// vars in one request. The secrets go in every upload, so none is dropped.
func (c Cloudflare) PutScript(ctx context.Context, name string, code []byte, dbID string, secrets, vars map[string]string) error {
	binds := []map[string]string{{"type": "d1", "name": "DB", "database_id": dbID}}
	for _, k := range sortedKeys(secrets) {
		binds = append(binds, map[string]string{"type": "secret_text", "name": k, "text": secrets[k]})
	}
	for _, k := range sortedKeys(vars) {
		binds = append(binds, map[string]string{"type": "plain_text", "name": k, "text": vars[k]})
	}
	meta, _ := json.Marshal(map[string]any{"main_module": "index.js", "compatibility_date": "2026-10-01", "bindings": binds})
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="metadata"`)
	h.Set("Content-Type", "application/json")
	pw, _ := mw.CreatePart(h)
	pw.Write(meta)
	h = textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="index.js"; filename="index.js"`)
	h.Set("Content-Type", "application/javascript+module")
	pw, _ = mw.CreatePart(h)
	pw.Write(code)
	mw.Close()
	return c.do(ctx, "PUT", c.acct()+"/workers/scripts/"+url.PathEscape(name), &buf, mw.FormDataContentType(), nil)
}

func (c Cloudflare) PutSchedule(ctx context.Context, name string) error {
	return c.do(ctx, "PUT", c.acct()+"/workers/scripts/"+url.PathEscape(name)+"/schedules",
		bytes.NewReader([]byte(`[{"cron":"* * * * *"}]`)), "application/json", nil)
}

func (c Cloudflare) EnsureSubdomain(ctx context.Context, want string) (string, error) {
	var r struct{ Subdomain string }
	if err := c.do(ctx, "GET", c.acct()+"/workers/subdomain", nil, "", &r); err == nil && r.Subdomain != "" {
		return r.Subdomain, nil
	}
	if err := c.do(ctx, "PUT", c.acct()+"/workers/subdomain", jsonBody(map[string]string{"subdomain": want}), "application/json", &r); err != nil {
		return "", err
	}
	return r.Subdomain, nil
}

func (c Cloudflare) EnableSubdomain(ctx context.Context, name string) error {
	return c.do(ctx, "POST", c.acct()+"/workers/scripts/"+url.PathEscape(name)+"/subdomain", jsonBody(map[string]bool{"enabled": true}), "application/json", nil)
}
