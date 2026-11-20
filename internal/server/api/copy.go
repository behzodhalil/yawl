package api

import (
	"encoding/json"
	"net/http"

	"github.com/behzodhalil/yawl/internal/server/app"
)

func (h *handler) copyService(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.service(w, r)
	if !ok {
		return
	}
	var in struct {
		Project  string                `json:"project"`
		Env      string                `json:"env"`
		Move     bool                  `json:"move"`
		Boxes    map[string]app.Reseal `json:"boxes"`
		ToPubkey string                `json:"to_pubkey"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&in); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("dry") == "1" {
		p, err := h.a.PlanCopy(r.Context(), svc.ID, in.Project, in.Env)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, p)
		return
	}
	id, err := h.a.CopyService(r.Context(), svc.ID, in.Project, in.Env, in.Boxes, in.ToPubkey, in.Move, actor(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"service": id, "path": "/p/" + in.Project + "/" + in.Env + "/s/" + svc.Name})
}
