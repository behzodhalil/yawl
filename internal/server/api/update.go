package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"

	"github.com/behzodhalil/yawl/internal/server/workers"
	"github.com/behzodhalil/yawl/internal/update"
)

func (h *handler) routesUpdate(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/update", h.updateState)
	mux.HandleFunc("POST /api/update", h.updateNow)
}

var errDev = errors.New("This is a development build; it does not update itself.")

func (h *handler) updater() (update.Updater, error) {
	if h.d.Version == "" || h.d.Version == "dev" {
		return update.Updater{}, errDev
	}
	if h.d.Updater == nil {
		return update.Default("")
	}
	return h.d.Updater()
}

func (h *handler) updateState(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"current": h.d.Version}
	u, err := h.updater()
	if err != nil {
		out["error"] = err.Error()
		writeJSON(w, out)
		return
	}
	latest, err := u.LatestVersion(r.Context())
	out["latest"] = latest
	if err != nil {
		out["error"] = "Could not ask for the latest release: " + err.Error()
	}
	writeJSON(w, out)
}

func older(a, b string) bool {
	pa, pb := strings.Split(strings.SplitN(a, "-", 2)[0], "."), strings.Split(strings.SplitN(b, "-", 2)[0], ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

func (h *handler) updateNow(w http.ResponseWriter, r *http.Request) {
	if h.d.Managed {
		http.Error(w, "The cloud updates this panel.", http.StatusConflict)
		return
	}
	var in struct {
		Version    string
		SkipBackup bool `json:"skip_backup"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in)
	u, err := h.updater()
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	v := strings.TrimPrefix(in.Version, "v")
	if v == "" {
		if v, err = u.LatestVersion(r.Context()); err != nil {
			http.Error(w, "Could not ask for the latest release: "+err.Error(), http.StatusBadGateway)
			return
		}
		if unsignedLatestIsDowngrade := older(v, h.d.Version); unsignedLatestIsDowngrade {
			http.Error(w, "The latest release (v"+v+") is older than this panel (v"+h.d.Version+"); name the version to go back.", http.StatusConflict)
			return
		}
	}
	if v == h.d.Version {
		http.Error(w, "The panel already runs v"+v+".", http.StatusConflict)
		return
	}
	path, err := u.Fetch(r.Context(), v, goruntime.GOOS, goruntime.GOARCH, filepath.Dir(h.d.Exe))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	backup := "skipped"
	if !in.SkipBackup && h.d.RunSelfBackup != nil {
		if err := h.d.RunSelfBackup(r.Context()); errors.Is(err, workers.ErrNoStorage) || errors.Is(err, workers.ErrNoRecovery) {
			backup = "none: " + err.Error()
		} else if err != nil {
			os.Remove(path)
			http.Error(w, "The backup before the update failed: "+err.Error()+". Fix it, or update without a backup.", http.StatusConflict)
			return
		} else {
			backup = "taken"
		}
	}
	if err := update.Swap(path, h.d.Exe); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]string{"version": v, "backup": backup})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if h.d.Restart != nil {
		h.d.Restart()
	}
}
