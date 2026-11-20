package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/workers"
)

func (h *handler) routesPreviews(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects/{project}/previews", h.listPreviews)
	mux.HandleFunc("POST /api/previews/{id}/close", h.closePreview)
	mux.HandleFunc("PUT /api/environments/{project}/{env}/preview", h.setPreview)
	mux.HandleFunc("PUT /api/services/{project}/{env}/{name}/preview", h.setServicePreview)
	mux.HandleFunc("GET /api/projects/{project}/preview-senders", h.previewSenders)
	mux.HandleFunc("PUT /api/projects/{project}/preview-senders", h.setPreviewSenders)
}

var forgeLogin = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(\[bot\])?$`)

func (h *handler) previewSenders(w http.ResponseWriter, r *http.Request) {
	id, err := h.a.St.Q.GetLiveProjectByName(r.Context(), r.PathValue("project"))
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such project", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	list, _ := h.a.St.Q.GetSetting(r.Context(), app.PreviewSendersKey(id))
	out := []string{}
	if list != "" {
		out = strings.Split(list, "\n")
	}
	writeJSON(w, map[string][]string{"senders": out})
}

func (h *handler) setPreviewSenders(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Senders []string `json:"senders"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	id, err := h.a.St.Q.GetLiveProjectByName(r.Context(), r.PathValue("project"))
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such project", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var list []string
	for _, s := range in.Senders {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || slices.Contains(list, s) {
			continue
		}
		if !forgeLogin.MatchString(s) || len(s) > 255 {
			http.Error(w, fmt.Sprintf("%q is not a forge login", s), http.StatusBadRequest)
			return
		}
		list = append(list, s)
	}
	if err := h.a.St.Q.SetSetting(r.Context(), db.SetSettingParams{Key: app.PreviewSendersKey(id), Value: strings.Join(list, "\n")}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type PreviewView struct {
	ID      string `json:"id"`
	Number  int64  `json:"number"`
	Title   string `json:"title"`
	Branch  string `json:"branch"`
	State   string `json:"state"`
	Source  string `json:"source"`
	Env     string `json:"env,omitempty"`
	Comment string `json:"comment,omitempty"`
	Base    string `json:"base,omitempty"`
	Author  string `json:"author,omitempty"`
	Head    string `json:"head,omitempty"`
	At      int64  `json:"at"`
	Merged  bool   `json:"merged,omitempty"`
	workers.Card
}

func (h *handler) listPreviews(w http.ResponseWriter, r *http.Request) {
	since := time.Now().Add(-24 * time.Hour).UnixMilli()
	rows, err := h.a.St.Q.ProjectPreviews(r.Context(), db.ProjectPreviewsParams{Project: r.PathValue("project"), MergedSince: since})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := []PreviewView{}
	for _, p := range rows {
		pv := db.Preview{ID: p.ID, ProjectID: p.ProjectID, SourceEnvID: p.SourceEnvID, Number: p.Number, Repo: p.Repo, Branch: p.Branch,
			State: p.State, EnvID: p.EnvID, Cause: p.Cause, Base: p.Base, MergeSha: p.MergeSha, MergedAt: p.MergedAt}
		line, err := workers.PreviewText(r.Context(), h.a.St.Q, pv)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		card, err := workers.PreviewCard(r.Context(), h.a.St.Q, pv)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		v := PreviewView{ID: p.ID, Number: p.Number, Title: p.Title, Branch: p.Branch, State: p.State, Source: p.Source, Comment: line,
			Base: p.Base, Author: p.Author, Head: p.HeadSha, At: p.LastPushAt, Card: card}
		if p.State == "closed" {
			v.Merged, v.At = true, p.MergedAt
		} else if p.EnvID != "" {
			v.Env = fmt.Sprintf("pr-%d", p.Number)
		}
		out = append(out, v)
	}
	writeJSON(w, out)
}

func (h *handler) closePreview(w http.ResponseWriter, r *http.Request) {
	n, err := h.a.St.Q.ClosePreviewByID(r.Context(), db.ClosePreviewByIDParams{UpdatedAt: time.Now().UnixMilli(), ID: r.PathValue("id")})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n == 0 {
		http.Error(w, "no such open preview", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) setServicePreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On bool `json:"on"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	s, err := h.a.St.Q.GetServiceByName(ctx, db.GetServiceByNameParams{Project: r.PathValue("project"), Env: r.PathValue("env"), Service: r.PathValue("name")})
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such service", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	off := int64(1)
	if in.On {
		off = 0
	}
	if err := h.a.St.Q.SetServiceNoPreview(ctx, db.SetServiceNoPreviewParams{NoPreview: off, UpdatedAt: time.Now().UnixMilli(), ID: s.ID}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) setPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On bool `json:"on"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx, project, env := r.Context(), r.PathValue("project"), r.PathValue("env")
	e, err := h.a.St.Q.GetEnvironmentByName(ctx, db.GetEnvironmentByNameParams{Project: project, Env: env})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && e.DeletedAt.Valid) {
		http.Error(w, "no such environment", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if in.On && e.PreviewOf != "" {
		http.Error(w, env+" is a preview; it cannot make previews.", http.StatusBadRequest)
		return
	}
	if in.On {
		if o, err := h.a.St.Q.PreviewEnvInProject(ctx, project); err == nil && o.Name != env {
			http.Error(w, fmt.Sprintf("only one environment of %s has preview: true; %s already has it", project, o.Name), http.StatusConflict)
			return
		}
	}
	on, domain := int64(0), ""
	if in.On {
		on, domain = 1, e.PreviewDomain
	}
	if err := h.a.St.Q.SetEnvironmentPreview(ctx, db.SetEnvironmentPreviewParams{Preview: on, PreviewDomain: domain, UpdatedAt: time.Now().UnixMilli(), ID: e.ID}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
