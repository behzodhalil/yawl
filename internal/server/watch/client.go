package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/watchsig"
)

const AnswerAfter = 5 * time.Minute

var sharedHC = &http.Client{Timeout: 20 * time.Second}

type Client struct {
	URL  string
	Key  []byte
	HTTP *http.Client
	Now  func() time.Time
}

func (c Client) do(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		body, _ = json.Marshal(in)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	watchsig.Headers(req.Header, "panel", c.Key, method, req.URL.RequestURI(), now(), body)
	hc := c.HTTP
	if hc == nil {
		hc = sharedHC
	}
	res, err := hc.Do(req)
	if err != nil {
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err
		}
		return fmt.Errorf("the watcher did not answer (%v)", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode/100 != 2 {
		// The cause goes to Settings and to Telegram: keep a short plain
		// answer, never a page (a deleted Worker answers with HTML).
		msg := strings.TrimSpace(string(b))
		if strings.HasPrefix(msg, "<") || msg == "" {
			return fmt.Errorf("the watcher answered %d", res.StatusCode)
		}
		if len(msg) > 200 {
			msg = msg[:200] + "…"
		}
		return fmt.Errorf("the watcher answered %d: %s", res.StatusCode, msg)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

type Ack struct {
	Ack       int64 `json:"ack"`
	LastRun   int64 `json:"last_run"`
	BacklogAt int64 `json:"backlog_at"`
}

func (c Client) Signal(ctx context.Context, subject string, at int64) (Ack, error) {
	in := map[string]any{"subject": subject}
	if at != 0 {
		in["at"] = at
	}
	var out Ack
	err := c.do(ctx, "POST", "/signal", in, &out)
	return out, err
}

func (c Client) Expect(ctx context.Context, cfg map[string]string, list []Expectation, reset bool) error {
	if list == nil {
		list = []Expectation{}
	}
	return c.do(ctx, "POST", "/expectations", map[string]any{"config": cfg, "expectations": list, "reset_panel": reset}, nil)
}

func (c Client) Incident(ctx context.Context, subject, action, label, text string, tg map[string]any, silent bool, chats []string) error {
	in := map[string]any{"subject": subject, "action": action, "label": label, "text": text, "silent": silent}
	if tg != nil {
		in["telegram"] = tg
	}
	if chats != nil {
		in["chats"] = chats
	}
	return c.do(ctx, "POST", "/incident", in, nil)
}

func (c Client) Note(ctx context.Context, id, text string) error {
	return c.do(ctx, "POST", "/incident", map[string]string{"subject": "", "action": "note", "id": id, "text": text}, nil)
}

func (c Client) Incidents(ctx context.Context, days int) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, "GET", "/incidents?days="+strconv.Itoa(days), nil, &out)
	return out, err
}

func (c Client) Checks(ctx context.Context) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, "GET", "/checks", nil, &out)
	return out, err
}

func Answering(ctx context.Context, q *db.Queries, now int64) bool {
	u, _ := q.GetSetting(ctx, "watch.url")
	ack, _ := q.GetSetting(ctx, "watch.ack_at")
	at, err := strconv.ParseInt(ack, 10, 64)
	return u != "" && err == nil && now-at < AnswerAfter.Milliseconds()
}

func ClientFromSettings(ctx context.Context, q *db.Queries, v *vault.Vault) (Client, bool) {
	u, _ := q.GetSetting(ctx, "watch.url")
	key, err := vault.GetSecret(ctx, q, v, "watch.key")
	if u == "" || err != nil || len(key) != 32 {
		return Client{}, false
	}
	return Client{URL: u, Key: key}, true
}
