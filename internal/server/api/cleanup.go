package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/workers"
	"github.com/behzodhalil/yawl/internal/spec"
)

func (h *handler) serverID(w http.ResponseWriter, r *http.Request) (string, bool) {
	s, err := h.a.St.Q.GetServerByName(r.Context(), r.PathValue("server"))
	if err != nil {
		http.Error(w, "no server "+r.PathValue("server"), http.StatusNotFound)
		return "", false
	}
	return s.ID, true
}

func (h *handler) lastCleanup(w http.ResponseWriter, r *http.Request) {
	id, ok := h.serverID(w, r)
	if !ok {
		return
	}
	run, _ := workers.LastCleanup(r.Context(), h.a.St.Q, id)
	writeJSON(w, run)
}

func (h *handler) cleanupNow(w http.ResponseWriter, r *http.Request) {
	id, ok := h.serverID(w, r)
	if !ok {
		return
	}
	if h.d.GW == nil {
		http.Error(w, errNoGateway.Error(), http.StatusServiceUnavailable)
		return
	}
	run, err := workers.RunCleanup(r.Context(), h.a.St.Q, h.d.GW, id, false, "by hand", time.Now())
	if errors.Is(err, workers.ErrCleaning) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, run)
}

func (h *handler) ownedVolumes(r *http.Request, serverID string) ([]string, error) {
	rows, err := h.a.St.Q.ServiceVolumesOnServer(r.Context(), serverID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range rows {
		if spec.IsDatabase(spec.Kind(s.Kind)) {
			out = append(out, fmt.Sprintf("yawl-%s-%s-%s-data", s.Project, s.Env, s.Name))
		}
		for _, v := range app.AllVolumes(s.VolumesJson, s.VolumesKept) {
			if n, err := proto.VolumeName(s.Project, s.Env, s.Name, v); err == nil {
				out = append(out, n)
			}
		}
	}
	return out, nil
}

func (h *handler) unusedVolumes(w http.ResponseWriter, r *http.Request) {
	id, ok := h.serverID(w, r)
	if !ok {
		return
	}
	owned, err := h.ownedVolumes(r, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	res, err := h.send(r.Context(), id, proto.CmdUnusedVolumes, struct{}{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	var d proto.UnusedVolumesData
	json.Unmarshal(res.Data, &d)
	writeJSON(w, removable(d.Names, owned))
}

func removable(unused, owned []string) []string {
	out := []string{}
	for _, n := range unused {
		if !slices.Contains(owned, n) && !strings.HasPrefix(n, "yawl-caddy") {
			out = append(out, n)
		}
	}
	return out
}

func (h *handler) removeVolume(w http.ResponseWriter, r *http.Request) {
	id, ok := h.serverID(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	owned, err := h.ownedVolumes(r, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if slices.Contains(owned, name) {
		http.Error(w, name+" belongs to a service; delete the service, and its volume goes when the trash is emptied.", http.StatusConflict)
		return
	}
	res, err := h.send(r.Context(), id, proto.CmdUnusedVolumes, struct{}{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	var d proto.UnusedVolumesData
	json.Unmarshal(res.Data, &d)
	if !slices.Contains(removable(d.Names, owned), name) {
		http.Error(w, name+" is not an unused volume on this server.", http.StatusConflict)
		return
	}
	if _, err := h.send(r.Context(), id, proto.CmdRemoveVolume, proto.RemoveVolumeArgs{Name: name}); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var errNoGateway = errors.New("this panel has no connection to agents")

func (h *handler) send(ctx context.Context, serverID, name string, args any) (proto.Result, error) {
	if h.d.GW == nil {
		return proto.Result{}, errNoGateway
	}
	cmd, err := proto.NewCommand(fmt.Sprintf("api:%s:%d", name, time.Now().UnixNano()), name, args)
	if err != nil {
		return proto.Result{}, err
	}
	sctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	res, err := h.d.GW.Send(sctx, serverID, cmd)
	if err != nil {
		return res, err
	}
	if !res.OK {
		return res, errors.New(res.Cause)
	}
	return res, nil
}
