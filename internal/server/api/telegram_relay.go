package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/notify"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/server/watch"
)

func (h *handler) routesRelay(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/settings/telegram/connect", h.relayConnect)
	mux.HandleFunc("DELETE /api/settings/telegram/connect", h.relayCancel)
	mux.HandleFunc("DELETE /api/settings/telegram/chats/{id}", h.relayUnlink)
	mux.HandleFunc("POST /api/settings/telegram/chats/{id}/test", h.relayTest)
	mux.HandleFunc("PUT /api/settings/telegram/chats/{id}/routes", h.relayRoutes)
}

var (
	ownBotKeys   = append([]string{"telegram.token", "telegram.chat_id", "telegram.bot", "telegram.chat_type", "telegram.chat_name", "telegram.linked_at"}, pendingKeys...)
	relayPending = []string{"telegram.relay_code", "telegram.relay_link", "telegram.relay_expires"}
)

type relayUp struct {
	mu  sync.Mutex
	url string
	at  time.Time
	bot string
	ok  bool
}

const relayUpFor = 30 * time.Second

func (h *handler) relayAvailable(ctx context.Context, r notify.Relay) (string, bool) {
	c := h.relayUp
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.url == r.URL && time.Since(c.at) < relayUpFor {
		return c.bot, c.ok
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	bot, err := notify.Relay{URL: r.URL}.Health(ctx)
	c.url, c.at, c.bot, c.ok = r.URL, time.Now(), bot, err == nil
	return bot, c.ok
}

type relayView struct {
	Available bool        `json:"available"`
	Bot       string      `json:"bot,omitempty"`
	Chats     []relayChat `json:"chats"`
	Pending   *relayCode  `json:"pending,omitempty"`
	Ended     *relayEnded `json:"ended,omitempty"`
	Error     string      `json:"error,omitempty"`
}

type relayChat struct {
	notify.RelayChat
	Routes notify.Route `json:"routes"`
}

type relayEnded struct {
	Why   string `json:"why"`
	Title string `json:"title,omitempty"`
}

type relayCode struct {
	notify.RelayCode
	QR string `json:"qr"`
}

func (h *handler) saveRelayChats(ctx context.Context, chats []notify.RelayChat) {
	if chats == nil {
		chats = []notify.RelayChat{}
	}
	b, _ := json.Marshal(chats)
	h.put(ctx, "telegram.relay_chats", string(b))
	routes := h.routes(ctx)
	if n := len(routes); n > 0 {
		maps.DeleteFunc(routes, func(id string, _ notify.Route) bool {
			return !slices.ContainsFunc(chats, func(c notify.RelayChat) bool { return c.ID == id })
		})
		if len(routes) < n {
			h.saveRoutes(ctx, routes)
		}
	}
}

func (h *handler) routes(ctx context.Context) map[string]notify.Route {
	out := map[string]notify.Route{}
	json.Unmarshal([]byte(h.setting(ctx, "telegram.relay_routes")), &out)
	return out
}

func (h *handler) saveRoutes(ctx context.Context, routes map[string]notify.Route) error {
	b, _ := json.Marshal(routes)
	return h.put(ctx, "telegram.relay_routes", string(b))
}

func (h *handler) relayView(ctx context.Context) relayView {
	r, chats, registered := watch.Relay(ctx, h.a.St.Q, h.d.Vault)
	bot, up := h.relayAvailable(ctx, r)
	v := relayView{Available: up, Bot: bot}
	code := h.setting(ctx, "telegram.relay_code")
	if registered && up {
		if b, list, err := r.Chats(ctx); err != nil {
			v.Error = err.Error()
		} else {
			v.Bot, chats = b, list
			h.saveRelayChats(ctx, list)
		}
		var u notify.CodeUse
		var err error
		if code != "" {
			u, err = r.Used(ctx, code)
		}
		if code != "" && err == nil && u.UsedAt != 0 {
			h.drop(ctx, append(relayPending, ownBotKeys...)...)
			code = ""
			if u.Already && u.Chat != nil {
				v.Ended = &relayEnded{Why: "already", Title: u.Chat.Title}
			}
		}
	}
	exp, _ := strconv.ParseInt(h.setting(ctx, "telegram.relay_expires"), 10, 64)
	if code != "" && h.a.Now().UnixMilli() >= exp {
		h.drop(ctx, relayPending...)
		v.Ended = &relayEnded{Why: "expired"}
	} else if code != "" {
		link := h.setting(ctx, "telegram.relay_link")
		qr, _ := app.QRSVG(link)
		v.Pending = &relayCode{RelayCode: notify.RelayCode{Code: code, Link: link, ExpiresAt: exp, Bot: v.Bot}, QR: qr}
	}
	routes, _ := watch.Routes(ctx, h.a.St.Q)
	v.Chats = []relayChat{}
	for _, c := range chats {
		rt, ok := routes[c.ID]
		if !ok {
			rt = notify.Route{Servers: []string{}, Projects: []string{}}
		}
		v.Chats = append(v.Chats, relayChat{c, rt})
	}
	return v
}

func (h *handler) dropRelayChats(ctx context.Context) {
	r, chats, ok := watch.Relay(ctx, h.a.St.Q, h.d.Vault)
	if !ok {
		return
	}
	for _, c := range chats {
		_ = r.Unlink(ctx, c.ID)
	}
	h.saveRelayChats(ctx, nil)
	h.drop(ctx, relayPending...)
}

func (h *handler) panelHost(r *http.Request) string {
	if d := h.setting(r.Context(), "panel.domain"); d != "" {
		return d
	}
	return r.Host
}

const dashboardReadsAsSignedOut = http.StatusUnauthorized

func relayStatus(err error) int {
	var re *notify.RelayError
	if errors.As(err, &re) && re.Status/100 == 4 && re.Status != dashboardReadsAsSignedOut {
		return re.Status
	}
	return http.StatusBadGateway
}

// relayConnect registers the panel with the relay when it has no key yet,
// then asks for a link code (R2, R3). With {"group": true} the link is
// ?startgroup: Telegram asks which group, adds the bot and sends
// /start@bot <code> there; ?start always opens the private chat.
func (h *handler) relayConnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct{ Group bool }
	json.NewDecoder(r.Body).Decode(&in)
	q := h.a.St.Q
	rl, _, registered := watch.Relay(ctx, q, h.d.Vault)
	if _, up := h.relayAvailable(ctx, rl); !up {
		http.Error(w, "The Telegram relay does not answer. Use your own bot.", http.StatusServiceUnavailable)
		return
	}
	host := h.panelHost(r)
	register := func() error {
		key, err := rl.Register(ctx, host)
		if err != nil {
			return err
		}
		rl.Key = key
		return vault.SetSecret(ctx, q, h.d.Vault, "telegram.relay_key", []byte(key))
	}
	if !registered {
		if err := register(); err != nil {
			http.Error(w, err.Error(), relayStatus(err))
			return
		}
	}
	c, err := rl.Code(ctx, host)
	if re := (*notify.RelayError)(nil); errors.As(err, &re) && re.Status == http.StatusUnauthorized {
		if err = register(); err == nil {
			c, err = rl.Code(ctx, host)
		}
	}
	if err != nil {
		http.Error(w, err.Error(), relayStatus(err))
		return
	}
	if in.Group {
		c.Link = "https://t.me/" + c.Bot + "?startgroup=" + c.Code
	}
	if err := h.put(ctx, "telegram.relay_code", c.Code, "telegram.relay_link", c.Link,
		"telegram.relay_expires", strconv.FormatInt(c.ExpiresAt, 10)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	qr, err := app.QRSVG(c.Link)
	if err != nil {
		http.Error(w, fmt.Sprintf("Could not draw the QR code: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, relayCode{RelayCode: c, QR: qr})
}

func (h *handler) relayCancel(w http.ResponseWriter, r *http.Request) {
	h.drop(r.Context(), relayPending...)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) relayUnlink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rl, chats, ok := watch.Relay(ctx, h.a.St.Q, h.d.Vault)
	id := r.PathValue("id")
	if !ok {
		http.Error(w, "Telegram is not connected.", http.StatusConflict)
		return
	}
	if err := rl.Unlink(ctx, id); err != nil && relayStatus(err) != http.StatusNotFound {
		http.Error(w, err.Error(), relayStatus(err))
		return
	}
	keep := []notify.RelayChat{}
	for _, c := range chats {
		if c.ID != id {
			keep = append(keep, c)
		}
	}
	h.saveRelayChats(ctx, keep)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) relayRoutes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in notify.Route
	if !readJSON(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if _, chats, _ := watch.Relay(ctx, h.a.St.Q, h.d.Vault); !slices.ContainsFunc(chats, func(c notify.RelayChat) bool { return c.ID == id }) {
		http.Error(w, "This chat is not connected.", http.StatusNotFound)
		return
	}
	servers, projects, err := watch.Known(ctx, h.a.St.Q)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, s := range in.Servers {
		if !servers[s] {
			http.Error(w, fmt.Sprintf("There is no server %q.", s), http.StatusBadRequest)
			return
		}
	}
	for _, p := range in.Projects {
		if !projects[p] {
			http.Error(w, fmt.Sprintf("There is no project %q.", p), http.StatusBadRequest)
			return
		}
	}
	routes := h.routes(ctx)
	if rt := in.Known(servers, projects); len(rt.Servers)+len(rt.Projects) == 0 {
		delete(routes, id)
	} else {
		routes[id] = rt
	}
	if err := h.saveRoutes(ctx, routes); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) relayTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rl, _, ok := watch.Relay(ctx, h.a.St.Q, h.d.Vault)
	if !ok {
		http.Error(w, "Telegram is not connected.", http.StatusConflict)
		return
	}
	rl.Chat = r.PathValue("id")
	if err := rl.Send(ctx, notify.Message{Text: "Test message from Yawl."}); err != nil {
		http.Error(w, err.Error(), relayStatus(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
