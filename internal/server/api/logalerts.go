package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/workers"
)

func (h *handler) routesLogAlerts(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{project}/{env}/{name}/log-alerts", h.logAlertsGet)
	mux.HandleFunc("PUT /api/services/{project}/{env}/{name}/log-alerts", h.logAlertsPut)
}

type logAlertView struct {
	workers.LogAlert
	Open bool `json:"open"`
}

func (h *handler) logAlertsGet(w http.ResponseWriter, r *http.Request) {
	id, ok := h.svcID(w, r)
	if !ok {
		return
	}
	h.writeLogAlerts(w, r, workers.LogAlerts(r.Context(), h.a.St.Q, id))
}

func (h *handler) writeLogAlerts(w http.ResponseWriter, r *http.Request, rules []workers.LogAlert) {
	out := []logAlertView{}
	for _, a := range rules {
		_, err := h.a.St.Q.GetSetting(r.Context(), workers.LogAlertOpen+a.ID)
		out = append(out, logAlertView{a, err == nil})
	}
	writeJSON(w, map[string]any{"rules": out})
}

func (h *handler) logAlertsPut(w http.ResponseWriter, r *http.Request) {
	id, ok := h.svcID(w, r)
	if !ok {
		return
	}
	var in struct{ Rules []workers.LogAlert }
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.Rules) > 5 {
		http.Error(w, "A service has at most 5 log alerts.", http.StatusBadRequest)
		return
	}
	old := workers.LogAlerts(r.Context(), h.a.St.Q, id)
	for i, a := range in.Rules {
		switch {
		case a.Lines < 0 || a.Lines > 100000:
			http.Error(w, "More than is a number of lines from 0 to 100,000.", http.StatusBadRequest)
			return
		case a.Minutes < 1 || a.Minutes > 60:
			http.Error(w, "Within is 1 to 60 minutes.", http.StatusBadRequest)
			return
		case len(a.Text) > 200 || strings.ContainsAny(a.Text, "\r\n"):
			http.Error(w, "The text is one line of at most 200 characters.", http.StatusBadRequest)
			return
		}
		if !slices.ContainsFunc(old, func(o workers.LogAlert) bool { return o.ID == a.ID }) {
			in.Rules[i].ID = store.NewID("la_")
		}
	}
	var err error
	if len(in.Rules) == 0 {
		err = h.a.St.Q.DeleteSettingKey(r.Context(), workers.LogAlertsKey+id)
	} else {
		js, _ := json.Marshal(in.Rules)
		err = h.a.St.Q.SetSetting(r.Context(), db.SetSettingParams{Key: workers.LogAlertsKey + id, Value: string(js)})
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("saving the log alerts: %v", err), http.StatusInternalServerError)
		return
	}
	h.writeLogAlerts(w, r, in.Rules)
}
