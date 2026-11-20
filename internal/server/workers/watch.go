package workers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/notify"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

type Watch struct {
	St  *store.Store
	GW  Sender
	App *app.App
	Now func() time.Time

	mu      sync.Mutex
	fails   map[string]int
	started time.Time
}

const (
	failsInARow    = 3
	rollbackWindow = 5 * time.Minute
	offlineAfter   = 2 * time.Minute
)

func (w *Watch) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Watch) Tick(ctx context.Context) error {
	if app.Adopting(ctx, w.St.Q) {
		return nil
	}
	if w.started.IsZero() {
		w.started = w.now()
	}
	svcs, err := w.St.Q.LiveServices(ctx)
	if err != nil {
		return err
	}
	live := map[string]bool{}
	for _, s := range svcs {
		live[s.DeployID] = true
		if s.Kind == "cron" {
			continue
		}
		if err := w.probe(ctx, s); err != nil {
			return err
		}
	}
	w.mu.Lock()
	for id := range w.fails {
		if !live[id] {
			delete(w.fails, id)
		}
	}
	w.mu.Unlock()
	return w.servers(ctx)
}

func (w *Watch) probe(ctx context.Context, s db.LiveServicesRow) error {
	now := w.now()
	container := s.Container
	if spec.IsDatabase(spec.Kind(s.Kind)) {
		container = fmt.Sprintf("yawl-%s-%s-%s", s.Project, s.Env, s.Name)
	}
	cmd, _ := proto.NewCommand(fmt.Sprintf("watch:%s:%d", s.DeployID, now.UnixMilli()), proto.CmdProbe,
		proto.ProbeArgs{Container: container, Kind: s.Kind, Path: s.HealthPath, Port: int(s.Port)})
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	res, err := w.GW.Send(pctx, s.ServerID, cmd)
	cancel()
	if ctx.Err() != nil || err != nil {
		return nil
	}
	name, ms := s.Project+"-"+s.Name, now.UnixMilli()
	label := s.Project + "/" + s.Env + "/" + s.Name
	w.mu.Lock()
	if w.fails == nil {
		w.fails = map[string]int{}
	}
	if res.OK {
		delete(w.fails, s.DeployID)
		w.mu.Unlock()
		if s.Health != "down" {
			return nil
		}
		if err := w.St.Tx(ctx, func(q *db.Queries) error {
			if err := q.SetServiceHealth(ctx, db.SetServiceHealthParams{Health: "ok", UpdatedAt: ms, ID: s.ID}); err != nil {
				return err
			}
			return announceIncident(ctx, q, ms, "service.recovered", s.ID, map[string]string{"deploy": s.DeployID},
				app.ServiceUp(label, openFor(ctx, q, "service:"+s.ID, ms), name+" recovered."),
				"service:"+s.ID, "close", label, false)
		}); err != nil {
			return err
		}
		Reroute(ctx, w.St.Q, w.GW, s.ID)
		return nil
	}
	w.fails[s.DeployID]++
	n := w.fails[s.DeployID]
	w.mu.Unlock()
	if n < failsInARow {
		return nil
	}
	cause := res.Cause
	busy, err := w.St.Q.ActiveDeploys(ctx, s.ID)
	if err != nil {
		return err
	}
	if busy > 0 {
		return nil
	}
	if cur, err := w.St.Q.GetService(ctx, s.ID); err != nil {
		return err
	} else if cur.UserStopped == 1 {
		return nil
	}
	inWindow := s.FinishedAt.Valid && now.Sub(time.UnixMilli(s.FinishedAt.Int64)) < rollbackWindow
	if inWindow && s.PrevDeployID != "" && s.Rollback == 0 && s.AutoRolledBack == 0 && !spec.IsDatabase(spec.Kind(s.Kind)) {
		if done, err := w.rollback(ctx, s, name, now); done || err != nil {
			return err
		}
	}
	if s.Health == "down" {
		return nil
	}
	return w.St.Tx(ctx, func(q *db.Queries) error {
		if err := q.SetServiceHealth(ctx, db.SetServiceHealthParams{Health: "down", UpdatedAt: ms, ID: s.ID}); err != nil {
			return err
		}
		return announceIncident(ctx, q, ms, "service.down", s.ID, map[string]string{"deploy": s.DeployID, "cause": cause},
			app.ServiceDown(label, spec.Kind(s.Kind), trimDot(strings.TrimSpace(cause)), fmt.Sprintf("%s is down: %s.", name, strings.TrimSuffix(strings.TrimSpace(cause), "."))), "service:"+s.ID, "open", label, false)
	})
}

func (w *Watch) rollback(ctx context.Context, s db.LiveServicesRow, name string, now time.Time) (bool, error) {
	id, to, err := w.App.Rollback(ctx, s.ID, "", "system")
	if errors.Is(err, app.ErrNoEarlier) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	text := fmt.Sprintf("%s failed %d health checks within 5 minutes of going live; rolled back to %s.", name, failsInARow, to)
	if note := app.RollbackNote(ctx, w.St.Q, s.DeployID); note != "" {
		text += " " + note
	}
	ms := now.UnixMilli()
	w.mu.Lock()
	delete(w.fails, s.DeployID)
	w.mu.Unlock()
	return true, w.St.Tx(ctx, func(q *db.Queries) error {
		if err := q.MarkAutoRolledBack(ctx, db.MarkAutoRolledBackParams{UpdatedAt: ms, ID: s.DeployID}); err != nil {
			return err
		}
		return announce(ctx, q, ms, "deploy.auto_rollback", s.DeployID, map[string]string{"service": s.ID, "rollback": id, "to": to, "text": text},
			app.AutoRollback(s.Project+"/"+s.Env+"/"+s.Name, to, fmt.Sprintf("failed %d health checks within 5 minutes of going live", failsInARow), text))
	})
}

func (w *Watch) servers(ctx context.Context) error {
	srvs, err := w.St.Q.ListServers(ctx)
	if err != nil {
		return err
	}
	now := w.now()
	ms := now.UnixMilli()
	for _, s := range srvs {
		if !s.LastHeartbeat.Valid {
			continue
		}
		disk, offline := s.DiskAlert, s.Offline
		type say struct {
			kind     string
			m        notify.Message
			incident string
			action   string
			quiet    bool
		}
		var texts []say
		level := int64(0)
		switch {
		case s.DiskUsedPct >= 95:
			level = 95
		case s.DiskUsedPct >= 85:
			level = 85
		}
		if level > disk {
			texts = append(texts, say{"server.disk", app.ServerDisk(s.Name, s.DiskUsedPct, level, fmt.Sprintf("%s disk is %d%% full.", s.Name, level)), fmt.Sprintf("disk:%s:%d", s.ID, level), "open", false})
			disk = level
		} else if s.DiskUsedPct < 80 && disk > 0 {
			texts = append(texts, say{"server.disk", app.ServerDiskOK(s.Name, s.DiskUsedPct, s.Name+" disk is under 80% again."), "disk:" + s.ID + ":85", "close", true})
			if disk == 95 {
				texts = append(texts, say{"server.disk", notify.Message{}, "disk:" + s.ID + ":95", "close", true})
			}
			disk = 0
		}
		silent := now.Sub(time.UnixMilli(s.LastHeartbeat.Int64)) > offlineAfter
		if silent && now.Sub(w.started) < offlineAfter {
			silent = offline == 1
		}
		if silent && offline == 0 {
			runs := ""
			if n, err := w.St.Q.CountServicesOnServer(ctx, s.ID); err == nil && n > 0 {
				runs = strconv.FormatInt(n, 10) + " services"
				if n == 1 {
					runs = "1 service"
				}
			}
			texts = append(texts, say{"server.offline", app.ServerOffline(s.Name, fmt.Sprintf("no answer for %s (limit %[1]s)", Lasted(offlineAfter)), runs,
				s.Name+" agent has been offline for 2 minutes."), "server:" + s.ID, "open", false})
			offline = 1
		} else if !silent && offline == 1 {
			texts = append(texts, say{"server.online", app.ServerOnline(s.Name, openFor(ctx, w.St.Q, "server:"+s.ID, ms), s.Name+" agent is back."), "server:" + s.ID, "close", false})
			offline = 0
		}
		if disk == s.DiskAlert && offline == s.Offline {
			continue
		}
		if err := w.St.Tx(ctx, func(q *db.Queries) error {
			if err := q.SetServerAlert(ctx, db.SetServerAlertParams{DiskAlert: disk, Offline: offline, UpdatedAt: ms, ID: s.ID}); err != nil {
				return err
			}
			for _, t := range texts {
				if err := announceIncident(ctx, q, ms, t.kind, s.ID, map[string]any{"disk_used_pct": s.DiskUsedPct}, t.m, t.incident, t.action, s.Name, t.quiet); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
