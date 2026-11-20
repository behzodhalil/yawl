package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/hetzner"
	"github.com/behzodhalil/yawl/internal/server/join"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

var InstallScript = "https://useyawl.com/install.sh"

const (
	hetznerKey  = "hetzner.token"
	hetznerMade = "hetzner.made"
)

var releaseRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

type madeServer struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
	At   int64  `json:"at"`
	Late bool   `json:"late"`
}

func (h *handler) made(r *http.Request) []madeServer {
	var all, out []madeServer
	if v, err := h.a.St.Q.GetSetting(r.Context(), hetznerMade); err == nil {
		json.Unmarshal([]byte(v), &all)
	}
	out = []madeServer{}
	for _, m := range all {
		if _, err := h.a.St.Q.GetServerByName(r.Context(), m.Name); err == nil {
			continue
		}
		m.Late = h.a.Now().Sub(time.UnixMilli(m.At)) > 15*time.Minute
		out = append(out, m)
	}
	return out
}

var serverNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

func (h *handler) routesHetzner(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/hetzner", h.hetznerChoices)
	mux.HandleFunc("PUT /api/settings/hetzner", h.hetznerConnect)
	mux.HandleFunc("DELETE /api/settings/hetzner", h.hetznerForget)
	mux.HandleFunc("POST /api/hetzner/servers", h.hetznerCreate)
}

func (h *handler) hetznerToken(r *http.Request) string {
	b, err := vault.GetSecret(r.Context(), h.a.St.Q, h.d.Vault, hetznerKey)
	if err != nil {
		return ""
	}
	return string(b)
}

func (h *handler) hetznerChoices(w http.ResponseWriter, r *http.Request) {
	tok := h.hetznerToken(r)
	if tok == "" {
		writeJSON(w, map[string]any{"connected": false})
		return
	}
	o, err := hetzner.Choices(r.Context(), tok)
	if err != nil {
		writeJSON(w, map[string]any{"connected": true, "error": err.Error(), "made": h.made(r)})
		return
	}
	writeJSON(w, map[string]any{"connected": true, "locations": o.Locations, "types": o.Types, "made": h.made(r)})
}

func (h *handler) hetznerConnect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := hetzner.Check(r.Context(), in.Token); in.Token == "" || err != nil {
		http.Error(w, fmt.Sprintf("Hetzner did not take the token: %v. Make one in the Cloud Console, the project's Security, API tokens, with Read & Write.", err), http.StatusBadRequest)
		return
	}
	if err := vault.SetSecret(r.Context(), h.a.St.Q, h.d.Vault, hetznerKey, []byte(in.Token)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) hetznerForget(w http.ResponseWriter, r *http.Request) {
	if err := h.a.St.Q.DeleteSettingKey(r.Context(), hetznerKey); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) hetznerCreate(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Type, Location string }
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	tok := h.hetznerToken(r)
	if tok == "" {
		http.Error(w, "Connect Hetzner first.", http.StatusBadRequest)
		return
	}
	if !releaseRe.MatchString(h.d.Version) {
		http.Error(w, fmt.Sprintf("This panel (%s) is not a release, so a new server could not install the same Yawl; add it by hand.", h.d.Version), http.StatusBadRequest)
		return
	}
	if strings.ContainsAny(h.d.PublicURL, "'\\") {
		http.Error(w, "The panel's public address has a quote in it.", http.StatusBadRequest)
		return
	}
	if !serverNameRe.MatchString(in.Name) {
		http.Error(w, "A server's name is lowercase letters, digits and dashes, starting with a letter, like fsn2.", http.StatusBadRequest)
		return
	}
	if _, err := h.a.St.Q.GetServerByName(ctx, in.Name); err == nil {
		http.Error(w, fmt.Sprintf("There is a server %s already.", in.Name), http.StatusBadRequest)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jt, err := join.NewToken(ctx, h.a.St.Q, h.a.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	script := fmt.Sprintf("#!/bin/sh\ncurl -fsSL %s | YAWL_VERSION=%s YAWL_JOIN='%s %s' sh\n", InstallScript, h.d.Version, h.d.PublicURL, jt)
	s, err := hetzner.Create(ctx, tok, in.Name, in.Type, in.Location, script)
	if err != nil {
		join.Revoke(ctx, h.a.St.Q, jt, h.a.Now())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	list := append(h.made(r), madeServer{Name: in.Name, IP: s.IP, At: h.a.Now().UnixMilli()})
	js, _ := json.Marshal(list)
	if err := h.a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: hetznerMade, Value: string(js)}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, s)
}
