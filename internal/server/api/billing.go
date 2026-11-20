package api

import (
	"errors"
	"net/http"

	"github.com/behzodhalil/yawl/internal/server/app"
)

func (h *handler) routesBilling(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/billing", h.billing)
}

func (h *handler) billing(w http.ResponseWriter, r *http.Request) {
	s, err := h.a.BillingState(r.Context())
	switch {
	case errors.Is(err, app.ErrNoBilling):
		http.Error(w, err.Error(), http.StatusNotFound)
	case err != nil:
		http.Error(w, "The cloud could not be asked just now.", http.StatusBadGateway)
	default:
		writeJSON(w, s)
	}
}
