package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const RelayURL = "https://tg.useyawl.com"

type Relay struct {
	URL, Key, Chat string
	HC             *http.Client
}

type RelayChat struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	LinkedAt int64  `json:"linked_at"`
	GoneAt   int64  `json:"gone_at"`
}

type RelayCode struct {
	Code      string `json:"code"`
	Link      string `json:"link"`
	ExpiresAt int64  `json:"expires_at"`
	Bot       string `json:"bot"`
}

type CodeUse struct {
	ExpiresAt int64      `json:"expires_at"`
	UsedAt    int64      `json:"used_at"`
	Already   bool       `json:"already"`
	Chat      *RelayChat `json:"chat"`
}

type Route struct {
	Servers  []string `json:"servers"`
	Projects []string `json:"projects"`
}

func (r Route) Takes(server, project string) bool {
	if len(r.Servers)+len(r.Projects) == 0 || server == "" && project == "" {
		return true
	}
	return server != "" && slices.Contains(r.Servers, server) || project != "" && slices.Contains(r.Projects, project)
}

func (r Route) Known(servers, projects map[string]bool) Route {
	out := Route{Servers: []string{}, Projects: []string{}}
	for _, s := range r.Servers {
		if servers[s] && !slices.Contains(out.Servers, s) {
			out.Servers = append(out.Servers, s)
		}
	}
	for _, p := range r.Projects {
		if projects[p] && !slices.Contains(out.Projects, p) {
			out.Projects = append(out.Projects, p)
		}
	}
	return out
}

func RouteTo(chats []RelayChat, routes map[string]Route, server, project string) (to []string, all bool) {
	all = true
	for _, c := range chats {
		if c.GoneAt != 0 {
			continue
		}
		if routes[c.ID].Takes(server, project) {
			to = append(to, c.ID)
		} else {
			all = false
		}
	}
	return to, all
}

type RelayError struct {
	Status int
	Msg    string
}

func (e *RelayError) Error() string { return e.Msg }

var relayHC = &http.Client{Timeout: 30 * time.Second}

func (r Relay) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(r.URL, "/")+path, body)
	if err != nil {
		return fmt.Errorf("the relay address is not valid: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.Key != "" {
		req.Header.Set("Authorization", "Bearer "+r.Key)
	}
	hc := r.HC
	if hc == nil {
		hc = relayHC
	}
	res, err := hc.Do(req)
	if err != nil {
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err
		}
		return fmt.Errorf("the Telegram relay did not answer (%v)", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(b))
		if msg == "" || strings.HasPrefix(msg, "<") || len(msg) > 200 {
			msg = fmt.Sprintf("the Telegram relay answered %d", res.StatusCode)
		}
		return &RelayError{Status: res.StatusCode, Msg: msg}
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func (r Relay) Send(ctx context.Context, m Message) error {
	in := map[string]any{"telegram": TelegramPayload(m, clock())}
	if r.Chat != "" {
		in["chat_id"] = r.Chat
	}
	return r.do(ctx, http.MethodPost, "/v1/send", in, nil)
}

func (r Relay) Health(ctx context.Context) (string, error) {
	var out struct{ Bot string }
	err := r.do(ctx, http.MethodGet, "/v1/health", nil, &out)
	return out.Bot, err
}

func (r Relay) Register(ctx context.Context, host string) (string, error) {
	var out struct{ Key string }
	err := r.do(ctx, http.MethodPost, "/v1/panels", map[string]string{"host": host}, &out)
	return out.Key, err
}

func (r Relay) Code(ctx context.Context, host string) (RelayCode, error) {
	var c RelayCode
	err := r.do(ctx, http.MethodPost, "/v1/codes", map[string]string{"host": host}, &c)
	return c, err
}

func (r Relay) Chats(ctx context.Context) (bot string, chats []RelayChat, err error) {
	var out struct {
		Bot   string      `json:"bot"`
		Chats []RelayChat `json:"chats"`
	}
	err = r.do(ctx, http.MethodGet, "/v1/chats", nil, &out)
	return out.Bot, out.Chats, err
}

func (r Relay) Used(ctx context.Context, code string) (CodeUse, error) {
	var u CodeUse
	err := r.do(ctx, http.MethodGet, "/v1/codes/"+url.PathEscape(code), nil, &u)
	return u, err
}

func (r Relay) Unlink(ctx context.Context, chat string) error {
	return r.do(ctx, http.MethodDelete, "/v1/chats/"+url.PathEscape(chat), nil, nil)
}
