package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

func (h *handler) exportEnv(w http.ResponseWriter, r *http.Request) {
	project, env := r.PathValue("project"), r.PathValue("env")
	ctx := r.Context()
	e, err := h.a.St.Q.GetEnvironmentByName(ctx, db.GetEnvironmentByNameParams{Project: project, Env: env})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && e.DeletedAt.Valid) {
		http.Error(w, "no environment "+project+"/"+env, http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if e.PreviewOf != "" {
		http.Error(w, env+" is a preview environment; export the environment it previews", http.StatusConflict)
		return
	}
	f, err := h.a.CurrentFile(ctx, project, env)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	body, err := spec.Marshal(f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var head strings.Builder
	fmt.Fprintf(&head, "# %s/%s, exported by Yawl. Variables are not in it; their names:\n", project, env)
	if vars, err := h.a.St.Q.EnvVarsInEnv(ctx, e.ID); err == nil {
		names := map[string]string{"": "every service"}
		if svcs, err := h.a.St.Q.ListServicesInEnv(ctx, e.ID); err == nil {
			for _, s := range svcs {
				names[s.ID] = s.Name
			}
		}
		for _, v := range vars {
			who, ok := names[v.ServiceID]
			if !ok {
				continue
			}
			if v.Ref != "" {
				fmt.Fprintf(&head, "# %s: %s, a reference to %s\n", who, v.Key, v.Ref)
			} else {
				fmt.Fprintf(&head, "# %s: %s\n", who, v.Key)
			}
		}
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="yawl.yaml"`)
	w.Write([]byte(head.String()))
	w.Write(body)
}
