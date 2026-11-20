package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type sourceView struct {
	Provider  string `json:"provider"`
	URL       string `json:"url,omitempty"`
	Path      string `json:"path,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Env       string `json:"env,omitempty"`
	UpdatedAt int64  `json:"updated_at"`
}

func (h *handler) envID(w http.ResponseWriter, r *http.Request) (string, bool) {
	e, err := h.a.St.Q.GetEnvironmentByName(r.Context(), db.GetEnvironmentByNameParams{Project: r.PathValue("project"), Env: r.PathValue("env")})
	if err != nil || e.DeletedAt.Valid {
		http.Error(w, "no environment "+r.PathValue("project")+"/"+r.PathValue("env"), http.StatusNotFound)
		return "", false
	}
	return e.ID, true
}

func (h *handler) listSources(w http.ResponseWriter, r *http.Request) {
	id, ok := h.envID(w, r)
	if !ok {
		return
	}
	rows, err := h.a.St.Q.SourcesForEnv(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := []sourceView{}
	for _, s := range rows {
		out = append(out, sourceView{s.Provider, s.Url, s.Path, s.Workspace, s.Env, s.UpdatedAt})
	}
	writeJSON(w, out)
}

func (h *handler) setSource(w http.ResponseWriter, r *http.Request) {
	id, ok := h.envID(w, r)
	if !ok || h.moving(w, r, scope{envID: id}) {
		return
	}
	var in struct {
		URL         string `json:"url"`
		Path        string `json:"path"`
		Workspace   string `json:"workspace"`
		Env         string `json:"env"`
		AgentBox    string `json:"agent_box"`
		RecoveryBox string `json:"recovery_box"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*maxBox+4096)).Decode(&in); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	p := r.PathValue("provider")
	why := ""
	switch {
	case p != "doppler" && p != "infisical" && p != "vault":
		why = "Yawl reads secrets from doppler, infisical or vault."
	case !validBox(in.AgentBox) || !validBox(in.RecoveryBox):
		why = "Send the token sealed to the server's agent and to the recovery key; the panel takes no plain token."
	case p == "vault" && (in.URL == "" || in.Path == ""):
		why = "Vault needs its address and the path of a KV secret, like secret/data/shop."
	case p == "infisical" && (in.Workspace == "" || in.Env == ""):
		why = "Infisical needs the project's ID and the environment's slug."
	case in.URL != "" && !managerURLNotLocal(r.Context(), in.URL):
		why = "The address is the manager's own, starting with https://; not localhost or a link-local address like 169.254.169.254."
	}
	if why != "" {
		http.Error(w, why, http.StatusBadRequest)
		return
	}
	if p == "infisical" && in.URL == "" {
		in.URL = "https://app.infisical.com"
	}
	now := time.Now().UnixMilli()
	if err := h.a.St.Q.UpsertSource(r.Context(), db.UpsertSourceParams{ID: store.NewID("src_"), EnvironmentID: id, Provider: p, Url: in.URL, Path: in.Path,
		Workspace: in.Workspace, Env: in.Env, AgentBox: in.AgentBox, RecoveryBox: in.RecoveryBox, CreatedAt: now, UpdatedAt: now}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func managerURLNotLocal(ctx context.Context, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ips, _ := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	for _, ip := range ips {
		if ip = ip.Unmap(); ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return false
		}
	}
	return true
}

func (h *handler) deleteSource(w http.ResponseWriter, r *http.Request) {
	id, ok := h.envID(w, r)
	if !ok || h.moving(w, r, scope{envID: id}) {
		return
	}
	if _, err := h.a.St.Q.DeleteSource(r.Context(), db.DeleteSourceParams{EnvironmentID: id, Provider: r.PathValue("provider")}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
