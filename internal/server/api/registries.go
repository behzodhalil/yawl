package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
)

type RegistryView struct {
	Host     string `json:"host"`
	Username string `json:"username"`
}

func (h *handler) registries(w http.ResponseWriter, r *http.Request) {
	rows, err := h.a.St.Q.SettingsWithPrefix(r.Context(), app.RegistryKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := []RegistryView{}
	for _, row := range rows {
		host := strings.TrimPrefix(row.Key, app.RegistryKey)
		if a, err := app.RegistryAuth(r.Context(), h.a.St.Q, h.d.Vault, host); err == nil && a != nil {
			out = append(out, RegistryView{Host: host, Username: a.Username})
		}
	}
	writeJSON(w, out)
}

func registryHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	h = strings.TrimRight(h, "/")
	if h == "index.docker.io" || h == "registry-1.docker.io" {
		return "docker.io"
	}
	return h
}

func (h *handler) saveRegistry(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Token string }
	if !readJSON(w, r, &in) {
		return
	}
	host := registryHost(r.PathValue("host"))
	if strings.Contains(host, "/") {
		http.Error(w, "A registry host is a name like ghcr.io, with no path.", http.StatusBadRequest)
		return
	}
	if host == "" || in.Username == "" || in.Token == "" {
		http.Error(w, "A registry login needs a host, a user and a token.", http.StatusBadRequest)
		return
	}
	if err := app.SaveRegistry(r.Context(), h.a.St.Q, h.d.Vault, host, in.Username, in.Token); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) removeRegistry(w http.ResponseWriter, r *http.Request) {
	if err := h.a.St.Q.DeleteSettingKey(r.Context(), app.RegistryKey+registryHost(r.PathValue("host"))); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) registryTest(w http.ResponseWriter, r *http.Request) {
	host := registryHost(r.PathValue("host"))
	a, err := app.RegistryAuth(r.Context(), h.a.St.Q, h.d.Vault, host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if a == nil {
		http.Error(w, "No login is saved for "+host+".", http.StatusNotFound)
		return
	}
	if err := app.CheckRegistry(r.Context(), &http.Client{Timeout: 15 * time.Second}, host, a.Username, a.Token); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
