package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Notifier interface {
	Send(ctx context.Context, m Message) error
}

const TelegramAPI = "https://api.telegram.org"

type telegram struct {
	base, token, chat string
	hc                *http.Client
}

func Telegram(baseURL, token, chatID string) Notifier {
	return &telegram{base: baseURL, token: token, chat: chatID, hc: &http.Client{Timeout: 15 * time.Second}}
}

func (t *telegram) Send(ctx context.Context, m Message) error {
	p := TelegramPayload(m, clock())
	p["chat_id"] = t.chat
	b, _ := json.Marshal(p)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/bot"+t.token+"/sendMessage", bytes.NewReader(b))
	if err != nil {
		return errors.New("telegram: the base URL is not valid") // the parse error would quote the token
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := t.hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // url.Error prints the URL, and the URL holds the bot token
		}
		return fmt.Errorf("telegram: %w", err)
	}
	defer res.Body.Close()
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	json.NewDecoder(res.Body).Decode(&out)
	if !out.OK {
		if out.Description == "" {
			out.Description = res.Status
		}
		return fmt.Errorf("telegram: %s", out.Description)
	}
	return nil
}

var ErrUnknownBot = errors.New("Telegram does not know that bot token.")

// ErrWebhook is Telegram refusing getUpdates (409) to a bot that has a webhook (yawl-6).
var ErrWebhook = errors.New("This bot has a webhook; use a bot of its own.")

type Bot struct {
	Base, Token string
	HC          *http.Client
}

func (b Bot) call(ctx context.Context, method string, out any) error {
	hc := b.HC
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.Base+"/bot"+b.Token+"/"+method, nil)
	if err != nil {
		return errors.New("telegram: the base URL is not valid")
	}
	res, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("telegram: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusNotFound {
		return ErrUnknownBot
	}
	if res.StatusCode == http.StatusConflict {
		return ErrWebhook
	}
	var env struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		return fmt.Errorf("telegram: %s", res.Status)
	}
	if !env.OK {
		return fmt.Errorf("telegram: %s", env.Description)
	}
	return json.Unmarshal(env.Result, out)
}

func (b Bot) Me(ctx context.Context) (string, error) {
	var me struct{ Username string }
	err := b.call(ctx, "getMe", &me)
	return me.Username, err
}

type Chat struct{ ID, Type, Name string }

// FindChat reads the bot's updates from offset once and returns the chat of
// the first message whose text contains code, and the offset to read from
// next: Telegram hands out 100 at most, so without it the oldest 100 would
// be read forever (yawl-6).
func (b Bot) FindChat(ctx context.Context, code string, offset int64) (chat Chat, found bool, next int64, err error) {
	var ups []struct {
		UpdateID int64 `json:"update_id"`
		Message  struct {
			Text string
			Chat struct {
				ID          int64
				Type, Title string
				First       string `json:"first_name"`
				Last        string `json:"last_name"`
			}
		}
	}
	if err := b.call(ctx, "getUpdates?offset="+strconv.FormatInt(offset, 10), &ups); err != nil {
		return Chat{}, false, offset, err
	}
	next = offset
	for _, u := range ups {
		next = max(next, u.UpdateID+1)
		if c := u.Message.Chat; strings.Contains(u.Message.Text, code) {
			name := c.Title
			if name == "" {
				name = strings.TrimSpace(c.First + " " + c.Last)
			}
			return Chat{ID: strconv.FormatInt(c.ID, 10), Type: c.Type, Name: name}, true, next, nil
		}
	}
	return Chat{}, false, next, nil
}
