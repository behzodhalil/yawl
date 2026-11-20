package api

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/workers"
	"github.com/behzodhalil/yawl/internal/spec"
)

func (h *handler) routesTrash(mux *http.ServeMux) {
	mux.HandleFunc("DELETE /api/services/{project}/{env}/{name}", h.deleteService)
	mux.HandleFunc("GET /api/services/{project}/{env}/{name}/delete-info", h.deleteInfo)
	mux.HandleFunc("DELETE /api/environments/{project}/{env}", h.deleteEnvironment)
	mux.HandleFunc("DELETE /api/projects/{project}", h.deleteProject)
	mux.HandleFunc("GET /api/trash", h.trash)
	mux.HandleFunc("POST /api/trash/{kind}/{id}/restore", h.restoreFromTrash)
}

func confirmed(w http.ResponseWriter, r *http.Request, name string) bool {
	var in struct{ Confirm string }
	if !readJSON(w, r, &in) {
		return false
	}
	if in.Confirm != name {
		http.Error(w, "Type "+name+" to delete it.", http.StatusBadRequest)
		return false
	}
	return true
}

func (h *handler) trashed(w http.ResponseWriter, r *http.Request, kind, id string) {
	if err := h.a.Delete(r.Context(), kind, id, actor(r)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if h.gw != nil {
		(&workers.Trash{St: h.a.St, GW: h.gw, App: h.a, Now: time.Now}).StopDeleted(r.Context())
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) deleteService(w http.ResponseWriter, r *http.Request) {
	s, ok := h.service(w, r)
	if !ok || !confirmed(w, r, s.Name) {
		return
	}
	h.trashed(w, r, "service", s.ID)
}

func (h *handler) deleteInfo(w http.ResponseWriter, r *http.Request) {
	s, ok := h.service(w, r)
	if !ok {
		return
	}
	users := []string{}
	if spec.IsDatabase(spec.Kind(s.Kind)) {
		if us, err := h.a.St.Q.RefUsers(r.Context(), db.RefUsersParams{EnvironmentID: s.EnvironmentID, Ref: s.Name + ".%"}); err == nil {
			users = append(users, us...)
		}
	}
	writeJSON(w, map[string]any{"purge_at": h.a.Now().Add(app.Grace).UnixMilli(), "users": users})
}

func (h *handler) deleteEnvironment(w http.ResponseWriter, r *http.Request) {
	env, err := h.a.St.Q.GetEnvironmentByName(r.Context(), db.GetEnvironmentByNameParams{Project: r.PathValue("project"), Env: r.PathValue("env")})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && env.DeletedAt.Valid) {
		http.Error(w, "no such environment", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if confirmed(w, r, r.PathValue("env")) {
		h.trashed(w, r, "environment", env.ID)
	}
}

func (h *handler) deleteProject(w http.ResponseWriter, r *http.Request) {
	id, err := h.a.St.Q.GetLiveProjectByName(r.Context(), r.PathValue("project"))
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such project", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if confirmed(w, r, r.PathValue("project")) {
		h.trashed(w, r, "project", id)
	}
}

func (h *handler) trash(w http.ResponseWriter, r *http.Request) {
	items, err := h.a.Trash(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}

func (h *handler) restoreFromTrash(w http.ResponseWriter, r *http.Request) {
	err := h.a.RestoreFromTrash(r.Context(), r.PathValue("kind"), r.PathValue("id"), actor(r))
	var held app.HeldError
	switch {
	case errors.Is(err, app.ErrNotInTrash):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, app.ErrPurging), errors.As(err, &held):
		http.Error(w, err.Error(), http.StatusConflict)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}
