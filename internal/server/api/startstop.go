package api

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"

	"errors"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/workers"
)

func (h *handler) routesStartStop(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/services/{project}/{env}/{name}/restart", h.serviceAct(func(r *http.Request, id string) (string, error) { return h.a.Restart(r.Context(), id, actor(r)) }))
	mux.HandleFunc("POST /api/services/{project}/{env}/{name}/start", h.serviceAct(func(r *http.Request, id string) (string, error) { return h.a.Start(r.Context(), id, actor(r)) }))
	mux.HandleFunc("POST /api/services/{project}/{env}/{name}/stop", h.stopService)
	mux.HandleFunc("PUT /api/services/{project}/{env}/{name}/password", h.setPassword)
	mux.HandleFunc("PUT /api/services/{project}/{env}/{name}/maintenance", h.setMaintenance)
}

func (h *handler) setPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := h.svcID(w, r)
	if !ok {
		return
	}
	var in struct{ User, Password string }
	if !readJSON(w, r, &in) {
		return
	}
	stored := ""
	if in.User != "" {
		if len(in.Password) < 8 || len(in.Password) > 72 {
			http.Error(w, "The password is at least 8 characters (and at most 72).", http.StatusBadRequest)
			return
		}
		if strings.ContainsAny(in.User, ":") {
			http.Error(w, "A user name has no colon.", http.StatusBadRequest)
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		b, _ := json.Marshal(map[string]string{"user": in.User, "hash": base64.StdEncoding.EncodeToString(hash)})
		stored = string(b)
	}
	if err := h.a.St.Q.SetBasicAuth(r.Context(), db.SetBasicAuthParams{BasicAuth: stored, UpdatedAt: time.Now().UnixMilli(), ID: id}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.reroute(w, r, id)
}

func (h *handler) setMaintenance(w http.ResponseWriter, r *http.Request) {
	id, ok := h.svcID(w, r)
	if !ok {
		return
	}
	var in struct{ On bool }
	if !readJSON(w, r, &in) {
		return
	}
	on := int64(0)
	if in.On {
		on = 1
	}
	if err := h.a.St.Q.SetMaintenance(r.Context(), db.SetMaintenanceParams{Maintenance: on, UpdatedAt: time.Now().UnixMilli(), ID: id}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.reroute(w, r, id)
}

func (h *handler) reroute(w http.ResponseWriter, r *http.Request, id string) {
	err := workers.Reroute(r.Context(), h.a.St.Q, h.gw, id)
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	note := "Saved. A deploy is running; it takes the change when it switches."
	if !errors.Is(err, workers.ErrDeployRunning) {
		note = "Saved, but the agent did not take it yet (" + err.Error() + "). It applies with the next deploy."
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"note": note})
}

func (h *handler) svcID(w http.ResponseWriter, r *http.Request) (string, bool) {
	svc, err := h.a.St.Q.GetServiceByName(r.Context(), db.GetServiceByNameParams{
		Project: r.PathValue("project"), Env: r.PathValue("env"), Service: r.PathValue("name")})
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such service", http.StatusNotFound)
		return "", false
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return "", false
	}
	return svc.ID, true
}

func (h *handler) serviceAct(f func(*http.Request, string) (string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.svcID(w, r)
		if !ok {
			return
		}
		dep, err := f(r, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, map[string]string{"deploy_id": dep})
	}
}

func (h *handler) stopService(w http.ResponseWriter, r *http.Request) {
	id, ok := h.svcID(w, r)
	if !ok {
		return
	}
	if err := h.a.Stop(r.Context(), id, actor(r)); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	note := ""
	if busy, _ := h.a.St.Q.ActiveDeploys(r.Context(), id); busy > 0 {
		note = "A deploy is running; Yawl stops the service once it ends."
	} else if h.gw != nil {
		if err := (&workers.Trash{St: h.a.St, GW: h.gw, App: h.a, Now: time.Now}).StopUserStopped(r.Context()); err != nil {
			note = "The agent did not answer; Yawl tries again every minute."
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"note": note})
}

func (h *handler) cancelDeploy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d, err := h.a.St.Q.GetDeploy(ctx, r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such deploy", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if m, _ := member(r); (d.MoveID != "" || d.RestoreFrom != "" || d.ImportID != "") && roleRank[m.Role] != admin {
		http.Error(w, "Only an Admin can cancel a move, restore or import.", http.StatusForbidden)
		return
	}
	now := time.Now().UnixMilli()
	if d.State == "queued" {
		var n int64
		err := h.a.St.Tx(ctx, func(q *db.Queries) (err error) {
			if n, err = q.CancelQueued(ctx, db.CancelQueuedParams{FinishedAt: sql.NullInt64{Int64: now, Valid: true}, UpdatedAt: now, ID: d.ID}); err != nil || n == 0 {
				return err
			}
			return app.Event(ctx, q, now, "deploy.canceled", d.ID, actor(r), nil)
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if n == 1 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
	}
	n, err := h.a.St.Q.RequestCancel(ctx, db.RequestCancelParams{UpdatedAt: now, ID: d.ID})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n == 0 {
		http.Error(w, "This deploy is past its build; it cannot be canceled now. Roll back once it ends.", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
