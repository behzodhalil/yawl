package api

import (
	"net/http"

	"github.com/behzodhalil/yawl/internal/proto"
)

func (h *handler) osStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := h.serverID(w, r)
	if !ok {
		return
	}
	res, err := h.send(r.Context(), id, proto.CmdOSStatus, struct{}{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(res.Data)
}

func (h *handler) osUpgrade(w http.ResponseWriter, r *http.Request) {
	id, ok := h.serverID(w, r)
	if !ok {
		return
	}
	if _, err := h.send(r.Context(), id, proto.CmdOSUpgrade, struct{}{}); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
