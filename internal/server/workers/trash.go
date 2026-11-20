package workers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/spec"
)

type Trash struct {
	St  *store.Store
	GW  Sender
	App *app.App
	V   *vault.Vault
	Now func() time.Time

	down map[string]bool
}

type trashed = db.TrashToStopRow

func (w *Trash) Tick(ctx context.Context) error {
	q := w.St.Q
	if app.Adopting(ctx, q) {
		return nil
	}
	w.down = map[string]bool{}
	if err := w.StopDeleted(ctx); err != nil {
		return err
	}
	if err := w.StopUserStopped(ctx); err != nil {
		return err
	}
	due, err := q.TrashDue(ctx, sql.NullInt64{Int64: w.Now().UnixMilli(), Valid: true})
	if err != nil {
		return err
	}
	for _, d := range due {
		w.purgeOne(ctx, d.ID)
	}
	return w.purgeEmpty(ctx)
}

func (w *Trash) StopDeleted(ctx context.Context) error {
	q := w.St.Q
	stop, err := q.TrashToStop(ctx)
	if err != nil {
		return err
	}
	var last error
	for _, s := range stop {
		if busy, err := q.ActiveDeploys(ctx, s.ID); err != nil || busy > 0 {
			continue
		}
		if w.waiting(ctx, s) {
			continue
		}
		if err := w.stop(ctx, s); err != nil {
			last = err
			continue
		}
		if err := q.SetServiceStopped(ctx, s.ID); err != nil {
			return err
		}
	}
	return last
}

func (w *Trash) purgeOne(ctx context.Context, id string) {
	row, err := w.St.Q.TrashOne(ctx, db.TrashOneParams{ID: id, PurgeAt: sql.NullInt64{Int64: w.Now().UnixMilli(), Valid: true}})
	if err != nil {
		return
	}
	s := trashed(row)
	if busy, err := w.St.Q.ActiveDeploys(ctx, s.ID); err != nil || busy > 0 {
		return
	}
	if w.waiting(ctx, s) {
		return
	}
	w.purge(ctx, s)
}

func (w *Trash) waiting(ctx context.Context, s trashed) bool {
	k := "trash.waiting." + s.ID
	if s.Offline == 1 {
		w.St.Q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: "waiting for " + s.ServerName + " to be online to remove its containers"})
		return true
	}
	w.St.Q.DeleteSettingKey(ctx, k)
	return false
}

func (w *Trash) StopUserStopped(ctx context.Context) error {
	q := w.St.Q
	todo, err := q.UserStopsToDo(ctx)
	if err != nil {
		return err
	}
	var last error
	for _, row := range todo {
		s := trashed(row)
		if busy, err := q.ActiveDeploys(ctx, s.ID); err != nil || busy > 0 {
			continue
		}
		if err := w.stop(ctx, s); err != nil {
			last = err
			continue
		}
		if err := w.St.Tx(ctx, func(q *db.Queries) error {
			if err := q.SetServiceStopped(ctx, s.ID); err != nil {
				return err
			}
			if svc, err := q.GetService(ctx, s.ID); err != nil || svc.Health != "down" {
				return err
			}
			ms := w.Now().UnixMilli()
			if err := q.SetServiceHealth(ctx, db.SetServiceHealthParams{Health: "ok", UpdatedAt: ms, ID: s.ID}); err != nil {
				return err
			}
			return announceIncident(ctx, q, ms, "service.stopped", s.ID, map[string]string{"incident": "closed"},
				app.ServiceStopped(s.Project+"/"+s.Env+"/"+s.Name, s.Project+"-"+s.Name+" was stopped."),
				"service:"+s.ID, "close", s.Project+"/"+s.Env+"/"+s.Name, true)
		}); err != nil {
			return err
		}
	}
	return last
}

func trashKeyNotReplayed(id, step string, try time.Time) string {
	return fmt.Sprintf("trash:%s:%s:%d", id, step, try.UnixMilli())
}

func (w *Trash) send(ctx context.Context, s trashed, step, name string, args any) error {
	if w.down[s.ServerID] {
		return fmt.Errorf("%s did not answer", s.ServerName)
	}
	cmd, err := proto.NewCommand(trashKeyNotReplayed(s.ID, step, w.Now()), name, args)
	if err != nil {
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	res, err := w.GW.Send(sctx, s.ServerID, cmd)
	if err != nil {
		if w.down == nil {
			w.down = map[string]bool{}
		}
		w.down[s.ServerID] = true
		return err
	}
	if !res.OK {
		return errors.New(res.Cause)
	}
	return nil
}

func (w *Trash) container(s trashed) string {
	if spec.IsDatabase(spec.Kind(s.Kind)) {
		return fmt.Sprintf("yawl-%s-%s-%s", s.Project, s.Env, s.Name)
	}
	return s.Container
}

func (w *Trash) unroute(ctx context.Context, s trashed) error {
	if s.Kind != "web" {
		return nil
	}
	var hosts []string
	if ip, err := netip.ParseAddr(s.PublicIp); err == nil {
		free := app.FreeHost(s.Name, s.Env, ip)
		if owner, err := app.FreeHostOwner(ctx, w.St.Q, s.ServerID, ip, free, s.Project, s.Env, s.Name); err != nil {
			return err
		} else if owner == "" {
			hosts = append(hosts, free)
		}
	}
	doms, err := w.St.Q.ListDomains(ctx, s.ID)
	if err != nil {
		return err
	}
	for _, d := range doms {
		hosts = append(hosts, d.Host)
	}
	for _, h := range hosts {
		if err := w.send(ctx, s, "route", proto.CmdRemoveRoute, proto.RemoveRouteArgs{Host: h}); err != nil {
			return err
		}
	}
	return nil
}

func (w *Trash) stop(ctx context.Context, s trashed) error {
	if err := w.unroute(ctx, s); err != nil {
		return err
	}
	if s.Kind == "cron" {
		return w.send(ctx, s, "stop", proto.CmdCronRemove, proto.CronRemoveArgs{ID: s.ID})
	}
	if c := w.container(s); c != "" {
		return w.send(ctx, s, "stop", proto.CmdStop, proto.StopArgs{Container: c})
	}
	return nil
}

func (w *Trash) purge(ctx context.Context, s trashed) error {
	q := w.St.Q
	if err := w.stop(ctx, s); err != nil {
		return err
	}
	if c := w.container(s); c != "" {
		if err := w.send(ctx, s, "drain", proto.CmdDrain, proto.DrainArgs{Container: c, Timeout: 10 * time.Second}); err != nil {
			return err
		}
	}
	if spec.IsDatabase(spec.Kind(s.Kind)) {
		if err := w.send(ctx, s, "volume", proto.CmdRemoveVolume, proto.RemoveVolumeArgs{Name: w.container(s) + "-data"}); err != nil {
			return err
		}
	}
	for _, v := range app.AllVolumes(s.VolumesJson, s.VolumesKept) {
		name, err := proto.VolumeName(s.Project, s.Env, s.Name, v)
		if err != nil {
			return err
		}
		if err := w.send(ctx, s, "volume:"+v, proto.CmdRemoveVolume, proto.RemoveVolumeArgs{Name: name}); err != nil {
			return err
		}
	}
	bks, err := q.ListBackups(ctx, s.ID)
	if err != nil {
		return err
	}
	var keys []string
	for _, b := range bks {
		if b.State != "deleted" {
			keys = append(keys, b.ObjectKey)
		}
	}
	wal, err := q.ListWALBatches(ctx, s.ID)
	if err != nil {
		return err
	}
	for _, b := range wal {
		keys = append(keys, b.ObjectKey)
	}
	if len(keys) > 0 {
		if storage, err := storageFrom(ctx, q, w.V); err == nil {
			if err := w.send(ctx, s, "backups", proto.CmdDeleteBackups, proto.DeleteBackupsArgs{Storage: storage, Keys: keys}); err != nil {
				return err
			}
		}
		for _, c := range copyStorages(ctx, q, w.V) {
			if err := w.send(ctx, s, "backups:"+c.name, proto.CmdDeleteBackups, proto.DeleteBackupsArgs{Storage: c.storage, Keys: keys}); err != nil {
				return err
			}
		}
	}
	if files, err := q.ServiceImportFiles(ctx, s.ID); err == nil {
		for _, f := range files {
			os.Remove(f)
		}
	}
	label := s.Project + "/" + s.Env + "/" + s.Name
	now := w.Now().UnixMilli()
	return w.St.Tx(ctx, func(q *db.Queries) error {
		for _, f := range []func(context.Context, string) error{q.PurgeServiceDomains, q.PurgeServiceVars, q.PurgeServiceBackupCopies, q.PurgeServiceBackups, q.PurgeServiceWALBatches, q.DeleteShares,
			q.PurgeServiceImports, q.PurgeServiceAnalyticsHours, q.PurgeServiceAnalyticsDays, q.PurgeServiceAnalyticsTop,
			q.ClearCurrentDeploy, q.PurgeServiceSteps, q.PurgeServiceDeploys, q.PurgeServiceMinutes, q.PurgeServiceHours, q.PurgeService} {
			if err := f(ctx, s.ID); err != nil {
				return err
			}
		}
		q.DeleteSettingKey(ctx, "trash.waiting."+s.ID)
		q.DeleteSettingKey(ctx, "cron.scheduled."+s.ID)
		cps, err := q.ListStorageCopies(ctx)
		if err != nil {
			return err
		}
		for _, inc := range incidentsOf(s, cps) {
			if err := announceIncident(ctx, q, now, "trash.purged", s.ID, map[string]string{"incident": "closed"}, app.Purged(label, label+" was purged."), inc, "close", label, true); err != nil {
				return err
			}
		}
		if s.DeletedBy == "preview" {
			return nil
		}
		return app.Announce(ctx, q, now, "trash.purged", s.ID, "system", app.Purged(label, label+" was purged."))
	})
}

func incidentsOf(s trashed, copies []db.StorageCopy) []string {
	out := []string{"deploy:" + s.ID, "service:" + s.ID, "oom:" + s.ID}
	if s.Kind == "cron" {
		out = append(out, "cron:"+s.ID)
	}
	var backed []string
	if spec.IsDatabase(spec.Kind(s.Kind)) {
		out = append(out, "backup:"+s.ID)
		backed = append(backed, "")
	}
	for _, v := range app.AllVolumes(s.VolumesJson, s.VolumesKept) {
		out = append(out, "backup:"+s.ID+":"+v)
		backed = append(backed, v)
	}
	for _, v := range backed {
		out = append(out, failedKey(s.ID, v))
		for _, c := range copies {
			out = append(out, app.CopyIncident(s.ID, v, c.Name))
		}
	}
	return out
}

func (w *Trash) purgeEmpty(ctx context.Context) error {
	now := w.Now().UnixMilli()
	return w.St.Tx(ctx, func(q *db.Queries) error {
		envs, err := q.PurgeEmptyEnvironments(ctx, sql.NullInt64{Int64: now, Valid: true})
		if err != nil {
			return err
		}
		for _, id := range envs {
			for _, f := range []func(context.Context, string) error{q.PurgeEnvVars, q.PurgeEnvMoves, q.PurgeEnvSources, q.DeletePreviewsOfEnv, q.PurgeEnvironment} {
				if err := f(ctx, id); err != nil {
					return err
				}
			}
		}
		ps, err := q.PurgeEmptyProjects(ctx, sql.NullInt64{Int64: now, Valid: true})
		if err != nil {
			return err
		}
		for _, p := range ps {
			if err := q.DeletePreviewsOfProject(ctx, p.ID); err != nil {
				return err
			}
			if err := q.PurgeProject(ctx, p.ID); err != nil {
				return err
			}
			if err := app.Announce(ctx, q, now, "trash.purged", p.ID, "system", app.Purged(p.Name, p.Name+" was purged.")); err != nil {
				return err
			}
		}
		return nil
	})
}
