package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/behzodhalil/yawl/internal/server/app"
)

func (h *handler) routesMove(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/environments/{project}/{env}/move", h.planMove)
	mux.HandleFunc("POST /api/environments/{project}/{env}/move", h.startMove)
	mux.HandleFunc("GET /api/environments/{project}/{env}/moves/latest", h.latestMove)
	mux.HandleFunc("POST /api/moves/{id}/retry", h.retryMove)
}

func (h *handler) planMove(w http.ResponseWriter, r *http.Request) {
	p, err := h.a.PlanMove(r.Context(), r.PathValue("project"), r.PathValue("env"), r.URL.Query().Get("to"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, p)
}

func (h *handler) startMove(w http.ResponseWriter, r *http.Request) {
	var in app.MoveRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&in) != nil {
		http.Error(w, "send {to, boxes, recovery_box}", http.StatusBadRequest)
		return
	}
	id, err := h.a.StartMove(r.Context(), r.PathValue("project"), r.PathValue("env"), in, actor(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]string{"move": id})
}

func (h *handler) latestMove(w http.ResponseWriter, r *http.Request) {
	v, err := h.a.LatestMove(r.Context(), r.PathValue("project"), r.PathValue("env"))
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no move", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, v)
}

func (h *handler) retryMove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RecoveryBox string `json:"recovery_box"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		http.Error(w, "send {recovery_box}", http.StatusBadRequest)
		return
	}
	if err := h.a.RetryMove(r.Context(), r.PathValue("id"), in.RecoveryBox, actor(r)); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
