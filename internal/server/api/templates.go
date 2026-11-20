package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/behzodhalil/yawl/internal/server/templates"
)

func (h *handler) routesTemplates(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/templates", h.templates)
	mux.HandleFunc("GET /api/templates/{id}", h.template)
	mux.HandleFunc("POST /api/environments/{project}/{env}/templates/{id}", h.installTemplate)
}

type TemplateCard struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Line   string   `json:"line"`
	Memory string   `json:"memory"`
	Icons  []string `json:"icons"`
}

type TemplateView struct {
	TemplateCard
	Note     string          `json:"note"`
	Services []ServiceForm   `json:"services"`
	Vars     []templates.Var `json:"vars"`
}

func card(t templates.Template) TemplateCard {
	return TemplateCard{ID: t.ID, Title: t.Title, Line: t.Line, Memory: t.Memory, Icons: t.Icons}
}

func (h *handler) templates(w http.ResponseWriter, r *http.Request) {
	out := []TemplateCard{}
	for _, t := range templates.All() {
		out = append(out, card(t))
	}
	writeJSON(w, out)
}

func (h *handler) template(w http.ResponseWriter, r *http.Request) {
	t, ok := templates.Get(r.PathValue("id"))
	if !ok {
		http.Error(w, "no such template", http.StatusNotFound)
		return
	}
	v := TemplateView{TemplateCard: card(t), Note: t.Note, Services: []ServiceForm{}, Vars: t.Vars}
	if v.Vars == nil {
		v.Vars = []templates.Var{}
	}
	for _, s := range t.Services {
		v.Services = append(v.Services, formOf(s))
	}
	writeJSON(w, v)
}

func (h *handler) installTemplate(w http.ResponseWriter, r *http.Request) {
	t, ok := templates.Get(r.PathValue("id"))
	if !ok {
		http.Error(w, "no such template", http.StatusNotFound)
		return
	}
	project, env := r.PathValue("project"), r.PathValue("env")
	f, err := h.a.CurrentFile(r.Context(), project, env)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such environment", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, s := range t.Services {
		for _, have := range f.Services {
			if have.Name == s.Name {
				http.Error(w, fmt.Sprintf("%s already exists in %s/%s.", s.Name, project, env), http.StatusConflict)
				return
			}
		}
	}
	f.Services = append(f.Services, t.Services...)
	changes, err := h.a.Apply(applyCtx(r), f, actor(r), r.URL.Query().Get("dry") == "1")
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"changes": changes})
}
