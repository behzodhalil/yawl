package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type TokenSource interface {
	Token(ctx context.Context, repo string) (string, error)
}

const inspectTimeout = 2 * time.Minute

func (h *handler) inspect(w http.ResponseWriter, r *http.Request) {
	var in struct{ Project, Env, Repo, Branch string }
	if !readJSON(w, r, &in) {
		return
	}
	in.Repo = strings.TrimSpace(in.Repo)
	if in.Repo == "" {
		http.Error(w, "Enter a repository URL.", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	env, err := h.a.St.Q.GetEnvironmentByName(ctx, db.GetEnvironmentByNameParams{Project: in.Project, Env: in.Env})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && env.DeletedAt.Valid) {
		http.Error(w, "no such environment", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	vars, err := h.a.St.Q.EnvVarsInEnv(ctx, env.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	args := proto.InspectArgs{Repo: in.Repo, Branch: in.Branch, Env: in.Env}
	for _, v := range vars {
		if v.ServiceID == "" && !slices.Contains(args.Keys, v.Key) {
			args.Keys = append(args.Keys, v.Key)
		}
	}
	shared, err := h.a.St.Q.SharedVarsFor(ctx, env.ServerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, v := range shared {
		if !slices.Contains(args.Keys, v.Key) {
			args.Keys = append(args.Keys, v.Key)
		}
	}
	if h.d.Tokens != nil {
		if args.Token, err = h.d.Tokens.Token(ctx, in.Repo); err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
	}
	limit := h.d.InspectTimeout
	if limit == 0 {
		limit = inspectTimeout
	}
	tctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	raw, _ := json.Marshal(args)
	res, err := h.gw.Send(tctx, env.ServerID, proto.Command{Key: store.NewID("inspect_"), Name: proto.CmdInspect, Args: raw})
	switch {
	case errors.Is(tctx.Err(), context.DeadlineExceeded):
		http.Error(w, fmt.Sprintf("Reading %s took longer than 2 minutes.", in.Repo), http.StatusGatewayTimeout)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	case res.Cause != "":
		http.Error(w, res.Cause, http.StatusUnprocessableEntity)
		return
	}
	var data proto.InspectData
	if err := json.Unmarshal(res.Data, &data); err != nil {
		http.Error(w, "the agent answered with something else: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, data)
}
