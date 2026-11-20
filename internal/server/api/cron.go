package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/cron"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func (h *handler) routesCron(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{project}/{env}/{name}/runs", h.cronRuns)
	mux.HandleFunc("POST /api/services/{project}/{env}/{name}/runs", h.cronRunNow)
}

func (h *handler) cronJob(w http.ResponseWriter, r *http.Request) (db.GetServiceByNameRow, string, bool) {
	s, ok := h.service(w, r)
	if !ok {
		return s, "", false
	}
	if s.Kind != "cron" {
		http.Error(w, s.Name+" is not a cron job.", http.StatusNotFound)
		return s, "", false
	}
	server := s.ServerID
	if srv, err := h.a.St.Q.GetServer(r.Context(), s.ServerID); err == nil {
		server = srv.Name
	}
	return s, server, true
}

func (h *handler) cronSend(ctx context.Context, s db.GetServiceByNameRow, name string) (proto.Result, error) {
	cmd, _ := proto.NewCommand(store.NewID("cr_"), name, proto.CronRemoveArgs{ID: s.ID})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return h.gw.Send(ctx, s.ServerID, cmd)
}

func (h *handler) cronRuns(w http.ResponseWriter, r *http.Request) {
	s, server, ok := h.cronJob(w, r)
	if !ok {
		return
	}
	tz, schedule := s.Timezone, s.Schedule
	if tz == "" {
		tz = cron.PanelZone()
	}
	if jobs, err := app.LiveCrons(r.Context(), h.a.St.Q); err == nil {
		for _, j := range jobs {
			if j.ID == s.ID {
				tz, schedule = j.Scheduled.Timezone, j.Scheduled.Schedule
			}
		}
	}
	var next int64
	if sch, err := cron.Parse(schedule); err == nil {
		if loc, err := cron.Zone(tz); err == nil {
			if t := sch.Next(h.a.Now().In(loc)); !t.IsZero() {
				next = t.UnixMilli()
			}
		}
	}
	res, err := h.cronSend(r.Context(), s, proto.CmdCronRuns)
	if err != nil {
		http.Error(w, fmt.Sprintf("%s is offline; the runs of %s cannot be read now.", server, s.Name), http.StatusBadGateway)
		return
	}
	if !res.OK {
		http.Error(w, res.Cause, http.StatusBadGateway)
		return
	}
	var data proto.CronRunsData
	json.Unmarshal(res.Data, &data)
	if data.Runs == nil {
		data.Runs = []proto.CronRun{}
	}
	writeJSON(w, map[string]any{"next": next, "timezone": tz, "running": data.Running, "runs": data.Runs})
}

func (h *handler) cronRunNow(w http.ResponseWriter, r *http.Request) {
	s, server, ok := h.cronJob(w, r)
	if !ok {
		return
	}
	res, err := h.cronSend(r.Context(), s, proto.CmdCronRunNow)
	switch {
	case err != nil:
		http.Error(w, fmt.Sprintf("%s is offline; %s cannot be run now.", server, s.Name), http.StatusBadGateway)
	case !res.OK && strings.HasPrefix(res.Cause, "this agent has no cron job"):
		http.Error(w, fmt.Sprintf("Deploy %s first: %s does not have it yet.", s.Name, server), http.StatusConflict)
	case !res.OK:
		http.Error(w, res.Cause, http.StatusConflict)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}
