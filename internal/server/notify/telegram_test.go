package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramRequest(t *testing.T) {
	var path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	if err := Telegram(srv.URL, "TOKEN", "42").Send(context.Background(), Message{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if path != "/botTOKEN/sendMessage" || body["chat_id"] != "42" || body["text"] != "hello" {
		t.Fatalf("path %s body %v", path, body)
	}
}

func TestTelegramError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
	}))
	defer srv.Close()
	err := Telegram(srv.URL, "T", "1").Send(context.Background(), Message{Text: "x"})
	if err == nil || err.Error() != "telegram: Bad Request: chat not found" {
		t.Fatalf("got %v", err)
	}
}

func TestTelegramErrorHidesToken(t *testing.T) {
	err := Telegram("http://127.0.0.1:1", "SECRET-TOKEN", "1").Send(context.Background(), Message{Text: "x"})
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Fatalf("error leaks the token: %v", err)
	}
}

func TestBotMeAndFindChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/botT/getMe":
			w.Write([]byte(`{"ok":true,"result":{"username":"acme_bot"}}`))
		case "/botT/getUpdates":
			w.Write([]byte(`{"ok":true,"result":[{"update_id":1,"message":{"text":"hi","chat":{"id":5}}},{"update_id":2,"message":{"text":"/start yawl-123456","chat":{"id":-1001,"type":"group","title":"Acme ops"}}}]}`))
		default:
			w.WriteHeader(401)
			w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	b := Bot{Base: srv.URL, Token: "T"}
	if u, err := b.Me(ctx); err != nil || u != "acme_bot" {
		t.Fatalf("%q %v", u, err)
	}
	if chat, ok, _, err := b.FindChat(ctx, "yawl-123456", 0); err != nil || !ok || chat.ID != "-1001" || chat.Type != "group" || chat.Name != "Acme ops" {
		t.Fatalf("%q %v %v", chat, ok, err)
	}
	if _, ok, next, _ := b.FindChat(ctx, "yawl-000000", 0); ok || next != 3 {
		t.Fatalf("found a code nobody sent, or next %d", next)
	}
	_, err := Bot{Base: srv.URL, Token: "BAD"}.Me(ctx)
	if !errors.Is(err, ErrUnknownBot) || strings.Contains(err.Error(), "BAD") {
		t.Fatalf("%v", err)
	}
}

func TestFindChatOffsetAndWebhook(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Query().Get("offset")
		if r.URL.Path == "/botHOOK/getUpdates" {
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"ok":false,"error_code":409,"description":"Conflict: can't use getUpdates method while webhook is active"}`))
			return
		}
		w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()
	ctx := context.Background()
	if _, _, next, err := (Bot{Base: srv.URL, Token: "T"}).FindChat(ctx, "x", 101); err != nil || asked != "101" || next != 101 {
		t.Fatalf("offset %q next %d %v", asked, next, err)
	}
	if _, _, _, err := (Bot{Base: srv.URL, Token: "HOOK"}).FindChat(ctx, "x", 0); !errors.Is(err, ErrWebhook) {
		t.Fatalf("%v", err)
	}
}
