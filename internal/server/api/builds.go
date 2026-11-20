package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

var repoRe = regexp.MustCompile(`^[a-z0-9.-]+(:\d+)?(/[a-z0-9._-]+)+$`)

func (h *handler) buildsGet(w http.ResponseWriter, r *http.Request) {
	s, _ := h.a.St.Q.GetSetting(r.Context(), "build.server")
	repo, _ := h.a.St.Q.GetSetting(r.Context(), "build.repository")
	writeJSON(w, map[string]string{"server": s, "repository": repo})
}

func (h *handler) buildsPut(w http.ResponseWriter, r *http.Request) {
	var in struct{ Server, Repository string }
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	if in.Server == "" {
		for _, k := range []string{"build.server", "build.repository"} {
			if err := h.a.St.Q.DeleteSettingKey(ctx, k); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, err := h.a.St.Q.GetServerByName(ctx, in.Server); errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no server "+in.Server, http.StatusBadRequest)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	repo := strings.TrimSpace(in.Repository)
	if strings.Contains(repo[strings.LastIndex(repo, "/")+1:], ":") { // a tag on the last part; a port is on the host
		http.Error(w, "Give the repository without a tag; Yawl tags each build with its deploy.", http.StatusBadRequest)
		return
	}
	if !repoRe.MatchString(repo) || !strings.Contains(strings.Split(repo, "/")[0], ".") {
		http.Error(w, "The repository is a registry and a path, like ghcr.io/acme/builds.", http.StatusBadRequest)
		return
	}
	host := spec.RegistryHost(repo)
	if auth, err := app.RegistryAuth(ctx, h.a.St.Q, h.d.Vault, host); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if auth == nil {
		http.Error(w, fmt.Sprintf("Add a login for %s in Integrations, Registries first: the build server pushes there.", host), http.StatusBadRequest)
		return
	}
	for k, v := range map[string]string{"build.server": in.Server, "build.repository": repo} {
		if err := h.a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: v}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
