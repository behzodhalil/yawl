package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	worker "github.com/behzodhalil/yawl/watch"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/server/watch"
)

func (h *handler) routesWatch(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/watch", h.watchState)
	mux.HandleFunc("PUT /api/settings/watch", h.watchSetup)
	mux.HandleFunc("DELETE /api/settings/watch", h.watchForget)
	mux.HandleFunc("GET /api/incidents", h.incidents)
	mux.HandleFunc("GET /api/checks", h.checks)
	mux.HandleFunc("POST /api/incidents/{id}/note", h.incidentNote)
	mux.HandleFunc("GET /api/heartbeats", h.heartbeats)
	mux.HandleFunc("POST /api/heartbeats", h.addHeartbeat)
	mux.HandleFunc("DELETE /api/heartbeats/{id}", h.deleteHeartbeat)
}

type watcherState string

const (
	watcherOff   watcherState = "off"
	watcherOn    watcherState = "on"
	watcherError watcherState = "error"
)

type agentWatch string

const (
	agentWatched    agentWatch = "watched"
	agentNotWatched agentWatch = "not watched"
)

type watchView struct {
	State   watcherState `json:"state"`
	URL     string       `json:"url,omitempty"`
	Error   string       `json:"error,omitempty"`
	Account string       `json:"account,omitempty"`
	Note    string       `json:"note,omitempty"`
	Checks  int          `json:"checks"`
	LastRun int64        `json:"last_run,omitempty"`
	Servers []watchAgent `json:"servers"`
}

type watchAgent struct {
	Name  string     `json:"name"`
	State agentWatch `json:"state"`
}

var checksWait = 3 * time.Second

const resetWatcherOnNextSync = "watch.reset"

func (h *handler) watchView(r *http.Request) watchView {
	ctx := r.Context()
	v := watchView{State: watcherOff, Account: h.setting(ctx, "watch.account"), URL: h.setting(ctx, "watch.url"), Error: h.setting(ctx, "watch.error")}
	if _, ok := watch.FromSettings(ctx, h.a.St.Q, h.d.Vault, h.d.CloudflareBase); !ok {
		return watchView{State: watcherOff}
	}
	switch {
	case v.Error != "":
		v.State = watcherError
	case v.URL != "":
		v.State = watcherOn
	}
	v.Servers = []watchAgent{}
	fp := h.setting(ctx, "watch.deployed")
	if servers, err := h.a.St.Q.ListServers(ctx); err == nil {
		for _, s := range servers {
			st := agentNotWatched
			if fp != "" && h.setting(ctx, "watch.agent."+s.ID) == watch.AgentMarker(fp, v.URL) {
				st = agentWatched
			}
			v.Servers = append(v.Servers, watchAgent{Name: s.Name, State: st})
		}
	}
	if c, ok := watch.ClientFromSettings(ctx, h.a.St.Q, h.d.Vault); ok && v.URL != "" {
		var checks struct {
			At      int64                      `json:"at"`
			Results map[string]json.RawMessage `json:"results"`
		}
		cctx, cancel := context.WithTimeout(ctx, checksWait)
		defer cancel()
		if raw, err := c.Checks(cctx); err == nil && json.Unmarshal(raw, &checks) == nil {
			v.Checks, v.LastRun = len(checks.Results), checks.At
		}
	}
	return v
}

func (h *handler) watcher(w http.ResponseWriter, r *http.Request) (watch.Client, bool) {
	c, ok := watch.ClientFromSettings(r.Context(), h.a.St.Q, h.d.Vault)
	if !ok {
		writeJSON(w, map[string]bool{"off": true})
	}
	return c, ok
}

func (h *handler) incidents(w http.ResponseWriter, r *http.Request) {
	c, ok := h.watcher(w, r)
	if !ok {
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 {
		days = 90
	}
	raw, err := c.Incidents(r.Context(), days)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}

func (h *handler) checks(w http.ResponseWriter, r *http.Request) {
	c, ok := h.watcher(w, r)
	if !ok {
		return
	}
	raw, err := c.Checks(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}

func (h *handler) incidentNote(w http.ResponseWriter, r *http.Request) {
	var in struct{ Text string }
	if !readJSON(w, r, &in) {
		return
	}
	c, ok := watch.ClientFromSettings(r.Context(), h.a.St.Q, h.d.Vault)
	if !ok {
		http.Error(w, "The watcher is not set up.", http.StatusConflict)
		return
	}
	if err := c.Note(r.Context(), r.PathValue("id"), strings.TrimSpace(in.Text)); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type heartbeatView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	PeriodS int64  `json:"period_s"`
	GraceS  int64  `json:"grace_s"`
	URL     string `json:"url"`
}

func (h *handler) heartbeats(w http.ResponseWriter, r *http.Request) {
	rows, err := h.a.St.Q.ListHeartbeats(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	u := h.setting(r.Context(), "watch.url")
	m, _ := member(r)
	out := []heartbeatView{}
	for _, b := range rows {
		hb := heartbeatView{ID: b.ID, Name: b.Name, PeriodS: b.PeriodS, GraceS: b.GraceS}
		if roleRank[m.Role] >= developer {
			hb.URL = u + "/hb/" + b.Token
		}
		out = append(out, hb)
	}
	writeJSON(w, out)
}

func (h *handler) addHeartbeat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string
		PeriodS int64 `json:"period_s"`
		GraceS  int64 `json:"grace_s"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	u := h.setting(ctx, "watch.url")
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case u == "":
		http.Error(w, "Deploy the watcher first: heartbeats are checked from Cloudflare.", http.StatusBadRequest)
		return
	case in.Name == "":
		http.Error(w, "A heartbeat needs a name.", http.StatusBadRequest)
		return
	case in.PeriodS < 60:
		http.Error(w, "A heartbeat's period is at least 60 seconds.", http.StatusBadRequest)
		return
	case in.GraceS < 0:
		http.Error(w, "The grace time cannot be negative.", http.StatusBadRequest)
		return
	}
	b := make([]byte, 16)
	rand.Read(b)
	id, tok, now := store.NewID("hb_"), hex.EncodeToString(b), h.a.Now().UnixMilli()
	err := h.a.St.Q.InsertHeartbeat(ctx, db.InsertHeartbeatParams{ID: id, Name: in.Name, PeriodS: in.PeriodS, GraceS: in.GraceS, Token: tok, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		if rows, _ := h.a.St.Q.ListHeartbeats(ctx); slices.ContainsFunc(rows, func(x db.Heartbeat) bool { return x.Name == in.Name }) {
			http.Error(w, "There is already a heartbeat named "+in.Name+".", http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, heartbeatView{ID: id, Name: in.Name, PeriodS: in.PeriodS, GraceS: in.GraceS, URL: u + "/hb/" + tok})
}

func (h *handler) deleteHeartbeat(w http.ResponseWriter, r *http.Request) {
	n, err := h.a.St.Q.DeleteHeartbeat(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n == 0 {
		http.Error(w, "no such heartbeat", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) watchState(w http.ResponseWriter, r *http.Request) { writeJSON(w, h.watchView(r)) }

func (h *handler) watchCode() ([]byte, error) {
	if h.d.WatchCode != nil {
		return h.d.WatchCode()
	}
	return worker.Code()
}

func (h *handler) watchSetup(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Account string }
	if !readJSON(w, r, &in) {
		return
	}
	in.Token, in.Account = strings.TrimSpace(in.Token), strings.TrimSpace(in.Account)
	if in.Token == "" || in.Account == "" {
		http.Error(w, "Paste a Cloudflare API token and your account id.", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	code, err := h.watchCode()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := vault.SetSecret(ctx, h.a.St.Q, h.d.Vault, "watch.cf_token", []byte(in.Token)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.put(ctx, "watch.account", in.Account, resetWatcherOnNextSync, "1")
	cf, _ := watch.FromSettings(ctx, h.a.St.Q, h.d.Vault, h.d.CloudflareBase)
	if _, err := watch.Deploy(ctx, h.a.St, h.d.Vault, cf, code); err != nil {
		var se *watch.StepError
		if errors.As(err, &se) || errors.Is(err, watch.ErrNoTelegram) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, h.watchView(r))
}

func (h *handler) watchForget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	watch.Forget(ctx, h.a.St.Q)
	writeJSON(w, watchView{State: watcherOff, Note: "The watcher stays in your Cloudflare account; delete " +
		watch.Name(watch.PanelID(ctx, h.a.St)) + " there to remove it."})
}
