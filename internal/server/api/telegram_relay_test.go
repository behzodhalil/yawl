package api

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/notify"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/server/watch"
)

type fakeRelay struct {
	mu        sync.Mutex
	down      bool
	health    int
	key, host string
	registers int
	code      string
	codes     int
	expires   int64
	used      map[string]notify.CodeUse
	chats     []notify.RelayChat
	sent      []map[string]any
}

func (f *fakeRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	var in map[string]any
	json.NewDecoder(r.Body).Decode(&in)
	route := r.Method + " " + r.URL.Path
	switch route {
	case "GET /v1/health":
		f.health++
		w.Write([]byte(`{"ok":true,"bot":"useyawl_bot"}`))
		return
	case "POST /v1/panels":
		f.registers++
		f.key, f.host = fmt.Sprintf("%064d", f.registers), in["host"].(string)
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"key":%q}`, f.key)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.key || f.key == "" {
		http.Error(w, "unknown panel key", 401)
		return
	}
	switch {
	case route == "POST /v1/codes":
		if len(f.chats) >= 10 {
			http.Error(w, "This panel has 10 chats, the most it can have.", 409)
			return
		}
		f.codes++
		f.code = fmt.Sprintf("Code%012d", f.codes)
		f.host = in["host"].(string)
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"code":%q,"link":"https://t.me/useyawl_bot?start=%s","expires_at":%d,"bot":"useyawl_bot"}`, f.code, f.code, cmp.Or(f.expires, int64(1)<<50))
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/codes/"):
		u, ok := f.used[strings.TrimPrefix(r.URL.Path, "/v1/codes/")]
		if !ok && strings.TrimPrefix(r.URL.Path, "/v1/codes/") != f.code {
			http.Error(w, "no such code", 404)
			return
		}
		json.NewEncoder(w).Encode(u)
	case route == "GET /v1/chats":
		json.NewEncoder(w).Encode(map[string]any{"bot": "useyawl_bot", "chats": f.chats})
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/v1/chats/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/chats/")
		for i, c := range f.chats {
			if c.ID == id {
				f.chats = append(f.chats[:i], f.chats[i+1:]...)
				w.WriteHeader(204)
				return
			}
		}
		http.Error(w, "not this panel's chat", 404)
	case route == "POST /v1/send":
		f.sent = append(f.sent, in)
		w.Write([]byte(`{"sent":1,"gone":0}`))
	default:
		http.Error(w, "not found", 404)
	}
}

func (f *fakeRelay) start(id, typ, title string, at int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := notify.RelayChat{ID: id, Type: typ, Title: title, LinkedAt: at}
	i := slices.IndexFunc(f.chats, func(c notify.RelayChat) bool { return c.ID == id })
	if i >= 0 {
		f.chats[i] = c
	} else {
		f.chats = append(f.chats, c)
	}
	if f.used == nil {
		f.used = map[string]notify.CodeUse{}
	}
	f.used[f.code] = notify.CodeUse{UsedAt: at, Already: i >= 0, Chat: &c}
	f.code = ""
}

func startRelay(t *testing.T, a *app.App) *fakeRelay {
	f := &fakeRelay{}
	s := httptest.NewServer(f)
	t.Cleanup(s.Close)
	a.St.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "telegram.relay_url", Value: s.URL})
	return f
}

type relayOut struct {
	State string
	Relay struct {
		Available bool
		Bot       string
		Chats     []notify.RelayChat
		Pending   *struct {
			Code, Link, QR string
			ExpiresAt      int64 `json:"expires_at"`
		}
	}
}

func readRelay(t *testing.T, b []byte) relayOut {
	t.Helper()
	var o relayOut
	if err := json.Unmarshal(b, &o); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	return o
}

func TestRelayUnavailable(t *testing.T) {
	srv, tok, _, _, _ := setupSettings(t)
	_, b := call(t, srv, tok, "GET", "/api/settings/telegram", "")
	if o := readRelay(t, b); o.Relay.Available || o.Relay.Chats == nil || len(o.Relay.Chats) != 0 || o.State != "none" {
		t.Fatalf("%s", b)
	}
	res, b := call(t, srv, tok, "POST", "/api/settings/telegram/connect", "")
	if res.StatusCode != 503 || strings.TrimSpace(string(b)) != "The Telegram relay does not answer. Use your own bot." {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestRelayConnect(t *testing.T) {
	srv, tok, a, v, tg := setupSettings(t)
	f := startRelay(t, a)
	ctx := context.Background()
	_, b := call(t, srv, tok, "POST", "/api/settings/telegram", `{"token":"123:GOOD"}`)
	_, code, _ := tgState(t, b)
	tg.mu.Lock()
	tg.updates = []string{"/start " + code}
	tg.mu.Unlock()
	call(t, srv, tok, "GET", "/api/settings/telegram", "")
	a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.domain", Value: "panel.acme.test"})

	_, b = call(t, srv, tok, "GET", "/api/settings/telegram", "")
	if o := readRelay(t, b); !o.Relay.Available || o.Relay.Bot != "useyawl_bot" || o.State != "linked" {
		t.Fatalf("before: %s", b)
	}
	res, b := call(t, srv, tok, "POST", "/api/settings/telegram/connect", "")
	var c struct {
		Code, Link, Bot, QR string
		ExpiresAt           int64 `json:"expires_at"`
	}
	json.Unmarshal(b, &c)
	if res.StatusCode != 200 || c.Code != "Code000000000001" || c.Link != "https://t.me/useyawl_bot?start="+c.Code || c.Bot != "useyawl_bot" || !strings.HasPrefix(c.QR, "<svg") || c.ExpiresAt == 0 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if key, _ := vault.GetSecret(ctx, a.St.Q, v, "telegram.relay_key"); string(key) != f.key || f.host != "panel.acme.test" {
		t.Fatalf("key %q host %q", key, f.host)
	}
	_, b = call(t, srv, tok, "GET", "/api/settings/telegram", "")
	if o := readRelay(t, b); o.Relay.Pending == nil || o.Relay.Pending.Code != c.Code || !strings.HasPrefix(o.Relay.Pending.QR, "<svg") || o.State != "linked" {
		t.Fatalf("waiting: %s", b)
	}
	f.start("7", "private", "Behzod Halil", 1000)
	_, b = call(t, srv, tok, "GET", "/api/settings/telegram", "")
	o := readRelay(t, b)
	if o.Relay.Pending != nil || len(o.Relay.Chats) != 1 || o.Relay.Chats[0].Title != "Behzod Halil" || o.State != "none" {
		t.Fatalf("linked: %s", b)
	}
	if _, err := vault.GetSecret(ctx, a.St.Q, v, "telegram.token"); err == nil {
		t.Fatal("the own bot's token is kept after the relay linked")
	}

	call(t, srv, tok, "POST", "/api/settings/telegram/connect", "")
	f.start("-100", "supergroup", "Acme ops", 2000)
	call(t, srv, tok, "GET", "/api/settings/telegram", "")
	if f.registers != 1 || f.codes != 2 {
		t.Fatalf("registers %d codes %d", f.registers, f.codes)
	}
	if res, b := call(t, srv, tok, "POST", "/api/settings/telegram/chats/-100/test", ""); res.StatusCode != 204 {
		t.Fatalf("test: %d %s", res.StatusCode, b)
	}
	if s := f.sent[0]; s["chat_id"] != "-100" || s["telegram"].(map[string]any)["text"] != "Test message from Yawl." {
		t.Fatalf("sent %v", f.sent)
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/settings/telegram/chats/7", ""); res.StatusCode != 204 || len(f.chats) != 1 {
		t.Fatalf("unlink: %d %v", res.StatusCode, f.chats)
	}
	if cs, _ := a.St.Q.GetSetting(ctx, "telegram.relay_chats"); !strings.Contains(cs, `"-100"`) || strings.Contains(cs, `"7"`) {
		t.Fatalf("kept list %s", cs)
	}
	f.mu.Lock()
	f.down = true
	f.mu.Unlock()
	_, b = call(t, srv, tok, "GET", "/api/settings/telegram", "")
	if o := readRelay(t, b); len(o.Relay.Chats) != 1 {
		t.Fatalf("down: %s", b)
	}
}

func TestOwnBotDropsRelayChats(t *testing.T) {
	srv, tok, a, _, tg := setupSettings(t)
	f := startRelay(t, a)
	call(t, srv, tok, "POST", "/api/settings/telegram/connect", "")
	f.start("7", "private", "B", 1000)
	call(t, srv, tok, "GET", "/api/settings/telegram", "")
	_, b := call(t, srv, tok, "POST", "/api/settings/telegram", `{"token":"123:GOOD"}`)
	_, code, _ := tgState(t, b)
	tg.mu.Lock()
	tg.updates = []string{"/start " + code}
	tg.mu.Unlock()
	_, b = call(t, srv, tok, "GET", "/api/settings/telegram", "")
	if o := readRelay(t, b); o.State != "linked" || len(o.Relay.Chats) != 0 || len(f.chats) != 0 {
		t.Fatalf("%s %v", b, f.chats)
	}
}

func TestOwnBotReplacesRelayEvenWhileRelayIsDown(t *testing.T) {
	srv, tok, a, v, tg := setupSettings(t)
	f := startRelay(t, a)
	call(t, srv, tok, "POST", "/api/settings/telegram/connect", "")
	f.start("7", "private", "B", 1000)
	call(t, srv, tok, "GET", "/api/settings/telegram", "")
	_, b := call(t, srv, tok, "POST", "/api/settings/telegram", `{"token":"123:GOOD"}`)
	_, code, _ := tgState(t, b)
	tg.mu.Lock()
	tg.updates = []string{"/start " + code}
	tg.mu.Unlock()
	f.mu.Lock()
	f.down = true
	f.mu.Unlock()
	_, b = call(t, srv, tok, "GET", "/api/settings/telegram", "")
	if o := readRelay(t, b); o.State != "linked" || len(o.Relay.Chats) != 0 {
		t.Fatalf("%s", b)
	}
	if _, chats, _ := watch.Relay(context.Background(), a.St.Q, v); watch.Live(chats) != 0 {
		t.Fatalf("alerts would still go through the relay: %v", chats)
	}
}

func TestRelayConnectErrors(t *testing.T) {
	srv, tok, a, v, _ := setupSettings(t)
	f := startRelay(t, a)
	call(t, srv, tok, "POST", "/api/settings/telegram/connect", "")
	for i := range 10 {
		f.start(fmt.Sprint(i), "group", "g", int64(i))
	}
	res, b := call(t, srv, tok, "POST", "/api/settings/telegram/connect", "")
	if res.StatusCode != 409 || strings.TrimSpace(string(b)) != "This panel has 10 chats, the most it can have." {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	f.mu.Lock()
	f.chats, f.key = nil, ""
	f.mu.Unlock()
	res, b = call(t, srv, tok, "POST", "/api/settings/telegram/connect", "")
	if key, _ := vault.GetSecret(context.Background(), a.St.Q, v, "telegram.relay_key"); res.StatusCode != 200 || f.registers != 2 || string(key) != f.key {
		t.Fatalf("%d %s registers %d", res.StatusCode, b, f.registers)
	}
	f.mu.Lock()
	f.key = "gone"
	f.mu.Unlock()
	if res, _ := call(t, srv, tok, "POST", "/api/settings/telegram/chats/1/test", ""); res.StatusCode != 502 {
		t.Fatalf("a relay 401 reached the page as %d", res.StatusCode)
	}
}

func TestRelayHealthCached(t *testing.T) {
	srv, tok, a, _, _ := setupSettings(t)
	f := startRelay(t, a)
	for range 3 {
		call(t, srv, tok, "GET", "/api/settings/telegram", "")
	}
	if f.health != 1 {
		t.Fatalf("health asked %d times", f.health)
	}
}

func TestRelayWaitEnds(t *testing.T) {
	srv, tok, a, _, _ := setupSettings(t)
	f := startRelay(t, a)
	call(t, srv, tok, "POST", "/api/settings/telegram/connect", "")
	f.start("7", "private", "Behzod Halil", 1000)
	call(t, srv, tok, "GET", "/api/settings/telegram", "")

	_, b := call(t, srv, tok, "POST", "/api/settings/telegram/connect", `{"group":true}`)
	var c struct{ Code, Link string }
	json.Unmarshal(b, &c)
	if c.Link != "https://t.me/useyawl_bot?startgroup="+c.Code {
		t.Fatalf("group link %s", b)
	}
	if _, b = call(t, srv, tok, "GET", "/api/settings/telegram", ""); !strings.Contains(string(b), `"link":"https://t.me/useyawl_bot?startgroup=`) {
		t.Fatalf("waiting: %s", b)
	}
	f.start("7", "private", "Behzod Halil", 2000)
	_, b = call(t, srv, tok, "GET", "/api/settings/telegram", "")
	var o struct {
		Relay struct {
			Pending *struct{}
			Chats   []notify.RelayChat
			Ended   *struct{ Why, Title string }
		}
	}
	json.Unmarshal(b, &o)
	if o.Relay.Pending != nil || o.Relay.Ended == nil || o.Relay.Ended.Why != "already" || o.Relay.Ended.Title != "Behzod Halil" || len(o.Relay.Chats) != 1 {
		t.Fatalf("already: %s", b)
	}

	f.mu.Lock()
	f.expires = 1
	f.mu.Unlock()
	call(t, srv, tok, "POST", "/api/settings/telegram/connect", "")
	_, b = call(t, srv, tok, "GET", "/api/settings/telegram", "")
	o.Relay.Ended = nil
	json.Unmarshal(b, &o)
	if o.Relay.Pending != nil || o.Relay.Ended == nil || o.Relay.Ended.Why != "expired" {
		t.Fatalf("expired: %s", b)
	}
	if _, b = call(t, srv, tok, "GET", "/api/settings/telegram", ""); strings.Contains(string(b), `"ended"`) {
		t.Fatalf("said twice: %s", b)
	}
}

func TestRelayRoutes(t *testing.T) {
	srv, tok, a, _, _ := setupSettings(t)
	f := startRelay(t, a)
	for _, q := range []string{
		`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, created_at, updated_at) VALUES ('srv_1', 'hel1', '1.2.3.4', 'P', '', 1, 1)`,
		`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, created_at, updated_at) VALUES ('srv_2', 'fsn1', '5.6.7.8', 'P2', '', 1, 1)`,
		`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('prj_1','acme',1,1)`,
		`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_1','prj_1','production','srv_1',1,1)`,
	} {
		if _, err := a.St.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	call(t, srv, tok, "POST", "/api/settings/telegram/connect", "")
	f.start("7", "private", "B", 1000)
	call(t, srv, tok, "POST", "/api/settings/telegram/connect", "")
	f.start("-100", "supergroup", "Acme ops", 2000)
	routes := func() map[string]notify.Route {
		_, b := call(t, srv, tok, "GET", "/api/settings/telegram", "")
		var o struct {
			Relay struct {
				Chats []struct {
					ID     string
					Routes notify.Route
				}
			}
		}
		json.Unmarshal(b, &o)
		out := map[string]notify.Route{}
		for _, c := range o.Relay.Chats {
			out[c.ID] = c.Routes
		}
		return out
	}
	if r := routes(); r["7"].Servers == nil || len(r["7"].Servers)+len(r["7"].Projects) != 0 {
		t.Fatalf("a new chat gets every alert: %+v", r)
	}
	for body, want := range map[string]string{
		`{"servers":["srv_9"],"projects":[]}`: `There is no server "srv_9".`,
		`{"servers":[],"projects":["nope"]}`:  `There is no project "nope".`,
	} {
		if res, b := call(t, srv, tok, "PUT", "/api/settings/telegram/chats/-100/routes", body); res.StatusCode != 400 || strings.TrimSpace(string(b)) != want {
			t.Fatalf("%s: %d %s", body, res.StatusCode, b)
		}
	}
	if res, _ := call(t, srv, tok, "PUT", "/api/settings/telegram/chats/55/routes", `{"servers":[],"projects":[]}`); res.StatusCode != 404 {
		t.Fatalf("not a linked chat: %d", res.StatusCode)
	}
	if res, b := call(t, srv, tok, "PUT", "/api/settings/telegram/chats/-100/routes", `{"servers":["srv_2","srv_2"],"projects":["acme"]}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	call(t, srv, tok, "PUT", "/api/settings/telegram/chats/7/routes", `{"servers":["srv_1"]}`)
	if r := routes(); !slices.Equal(r["-100"].Servers, []string{"srv_2"}) || !slices.Equal(r["-100"].Projects, []string{"acme"}) || !slices.Equal(r["7"].Servers, []string{"srv_1"}) {
		t.Fatalf("%+v", r)
	}
	a.St.DB.Exec(`DELETE FROM servers WHERE id = 'srv_2'`)
	if r := routes(); len(r["-100"].Servers) != 0 || !slices.Equal(r["-100"].Projects, []string{"acme"}) {
		t.Fatalf("deleted server: %+v", r)
	}
	call(t, srv, tok, "PUT", "/api/settings/telegram/chats/-100/routes", `{"servers":[],"projects":[]}`)
	call(t, srv, tok, "DELETE", "/api/settings/telegram/chats/7", "")
	if s, _ := a.St.Q.GetSetting(context.Background(), "telegram.relay_routes"); s != "{}" {
		t.Fatalf("kept %s", s)
	}
}
