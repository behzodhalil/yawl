package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/behzodhalil/yawl/internal/spec"
)

func (h *handler) routesForms(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/projects", h.createProject)
	mux.HandleFunc("POST /api/environments", h.createEnv)
	mux.HandleFunc("POST /api/environments/{project}/{env}/services", h.putService)
}

func (h *handler) applyFile(w http.ResponseWriter, r *http.Request, f spec.File, dry bool) {
	if err := spec.Validate(&f); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	changes, err := h.a.Apply(applyCtx(r), f, actor(r), dry)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"changes": changes})
}

type envReq struct {
	Name, Project, Env, Server string
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		http.Error(w, "The request is not valid JSON: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func (h *handler) createProject(w http.ResponseWriter, r *http.Request) {
	var in envReq
	if !readJSON(w, r, &in) {
		return
	}
	if _, err := h.a.CurrentFile(r.Context(), in.Name, "production"); err == nil {
		http.Error(w, fmt.Sprintf("Project %s already exists.", in.Name), http.StatusConflict)
		return
	}
	h.applyFile(w, r, spec.File{Project: in.Name, Environment: "production", Server: in.Server}, false)
}

func (h *handler) createEnv(w http.ResponseWriter, r *http.Request) {
	var in envReq
	if !readJSON(w, r, &in) {
		return
	}
	if in.Env != "production" && in.Env != "staging" {
		http.Error(w, "The dashboard makes production and staging environments.", http.StatusBadRequest)
		return
	}
	envs, err := h.a.St.Q.ListEnvironmentTree(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	known := false
	for _, e := range envs {
		known = known || e.Project == in.Project
		if e.Project == in.Project && e.Env == in.Env {
			http.Error(w, fmt.Sprintf("%s already has %s.", in.Project, in.Env), http.StatusConflict)
			return
		}
	}
	if !known {
		http.Error(w, "no such project", http.StatusNotFound)
		return
	}
	h.applyFile(w, r, spec.File{Project: in.Project, Environment: in.Env, Server: in.Server}, false)
}

var testAfterRead func()

func (h *handler) putService(w http.ResponseWriter, r *http.Request) {
	var form ServiceForm
	if !readJSON(w, r, &form) {
		return
	}
	s, err := form.spec()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var bad error
	changes, err := h.a.ApplyEdit(applyCtx(r), r.PathValue("project"), r.PathValue("env"), func(f *spec.File) error {
		if testAfterRead != nil {
			testAfterRead()
		}
		replaced := false
		for i := range f.Services {
			if f.Services[i].Name == s.Name && r.URL.Query().Get("new") == "1" {
				return fmt.Errorf("%s already exists in %s.", s.Name, f.Environment)
			}
			if f.Services[i].Name == s.Name {
				keepSubSecond(&s, f.Services[i])
				f.Services[i], replaced = s, true
			}
		}
		if !replaced {
			f.Services = append(f.Services, s)
		}
		bad = spec.Validate(f)
		return bad
	}, actor(r), r.URL.Query().Get("dry") == "1")
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, "no such environment", http.StatusNotFound)
	case bad != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
	case err != nil:
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		writeJSON(w, map[string]any{"changes": changes})
	}
}

func keepSubSecond(s *spec.Service, old spec.Service) {
	for _, d := range []struct {
		to  *time.Duration
		was time.Duration
	}{
		{&s.HealthTimeout, old.HealthTimeout}, {&s.DrainTimeout, old.DrainTimeout},
		{&s.ReleaseTimeout, old.ReleaseTimeout}, {&s.Timeout, old.Timeout},
	} {
		if *d.to == d.was.Truncate(time.Second) {
			*d.to = d.was
		}
	}
}
