package api

import (
	"context"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"time"

	"github.com/behzodhalil/yawl/internal/server/notify"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/server/workers"
)

func (h *handler) routesNotifications(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/notifications", h.notifications)
	mux.HandleFunc("PUT /api/settings/notifications/{channel}", h.notificationPut)
	mux.HandleFunc("DELETE /api/settings/notifications/{channel}", h.notificationDelete)
	mux.HandleFunc("POST /api/settings/notifications/{channel}/test", h.notificationTest)
}

var hookChannels = map[string]string{"slack": "notify.slack.url", "discord": "notify.discord.url",
	"webhook": "notify.webhook.url", "ntfy": "notify.ntfy.url"}

var badHookURL = map[string]string{"slack": "Paste the webhook URL Slack gave you.", "discord": "Paste the webhook URL Discord gave you.",
	"webhook": "Give the webhook's whole address, like https://n8n.acme.uz/webhook/alerts.", "ntfy": "Give the topic's whole address, like https://ntfy.sh/your-topic."}

var emailKeys = []string{"host", "port", "username", "from", "to"}

func knownChannel(w http.ResponseWriter, r *http.Request) (string, bool) {
	c := r.PathValue("channel")
	if _, ok := hookChannels[c]; ok || c == "email" {
		return c, true
	}
	http.Error(w, "no such channel: slack, discord, email, webhook or ntfy", http.StatusNotFound)
	return c, false
}

func (h *handler) notifications(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]map[string]any{}
	for c, key := range hookChannels {
		u, err := vault.GetSecret(ctx, h.a.St.Q, h.d.Vault, key)
		host := ""
		if p, perr := url.Parse(string(u)); err == nil && perr == nil {
			host = p.Host
		}
		out[c] = map[string]any{"set": err == nil, "host": host}
	}
	e, ok := workers.EmailSettings(ctx, h.a.St, h.d.Vault)
	out["email"] = map[string]any{"set": ok, "host": e.Host, "port": e.Port, "username": e.Username, "from": e.From, "to": e.To}
	writeJSON(w, out)
}

func (h *handler) notificationPut(w http.ResponseWriter, r *http.Request) {
	c, ok := knownChannel(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if key, hook := hookChannels[c]; hook {
		var in struct {
			URL string `json:"url"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if u, err := url.Parse(in.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			http.Error(w, badHookURL[c], http.StatusBadRequest)
			return
		}
		if err := vault.SetSecret(ctx, h.a.St.Q, h.d.Vault, key, []byte(in.URL)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]bool{"set": true})
		return
	}
	var in struct{ Host, Port, Username, Password, From, To string }
	if !readJSON(w, r, &in) {
		return
	}
	if p, err := strconv.Atoi(in.Port); in.Host == "" || err != nil || p < 1 || p > 65535 {
		http.Error(w, "Give the mail server's host and port (465 or 587, usually).", http.StatusBadRequest)
		return
	}
	for _, a := range []string{in.From, in.To} {
		if _, err := mail.ParseAddress(a); err != nil {
			http.Error(w, a+" is not an email address.", http.StatusBadRequest)
			return
		}
	}
	for k, v := range map[string]string{"host": in.Host, "port": in.Port, "username": in.Username, "from": in.From, "to": in.To} {
		if err := h.a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "notify.email." + k, Value: v}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if in.Password != "" {
		if err := vault.SetSecret(ctx, h.a.St.Q, h.d.Vault, "notify.email.password", []byte(in.Password)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, map[string]bool{"set": true})
}

func (h *handler) notificationDelete(w http.ResponseWriter, r *http.Request) {
	c, ok := knownChannel(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	keys := []string{hookChannels[c]}
	if c == "email" {
		keys = []string{"notify.email.password"}
		for _, k := range emailKeys {
			keys = append(keys, "notify.email."+k)
		}
	}
	for _, k := range keys {
		if err := h.a.St.Q.DeleteSettingKey(ctx, k); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) notificationTest(w http.ResponseWriter, r *http.Request) {
	c, ok := knownChannel(w, r)
	if !ok {
		return
	}
	extras, _ := workers.ExtrasFromSettings(h.a.St, h.d.Vault)(r.Context())
	n := extras[c]
	if n == nil {
		http.Error(w, c+" is not set up.", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := n.Send(ctx, notify.Message{Text: "A test message from Yawl. Alerts will come here."}); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
