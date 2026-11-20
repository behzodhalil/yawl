package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

func (a *App) Backup(ctx context.Context, serviceID, actor string) (string, error) {
	svc, err := a.St.Q.GetService(ctx, serviceID)
	if err != nil {
		return "", err
	}
	if !spec.IsDatabase(spec.Kind(svc.Kind)) {
		return "", fmt.Errorf("%s is not a database", svc.Name)
	}
	if _, err := DatabaseVar(ctx, a.St.Q, svc.EnvironmentID, svc.ID, "URL"); err != nil {
		return "", fmt.Errorf("%s has not been created yet; deploy it first", svc.Name)
	}
	id, now := store.NewID("bak_"), a.Now()
	ext := map[string]string{"postgres": "dump", "mysql": "sql", "mariadb": "sql", "mongodb": "archive"}[svc.Kind]
	if ext == "" {
		ext = "tar"
	}
	key := fmt.Sprintf("yawl/%s/%s/%s/%s-%s.%s.age", svc.Project, svc.Env, svc.Name, now.UTC().Format("20060102T150405Z"), id, ext)
	err = a.St.Tx(ctx, func(q *db.Queries) error {
		if err := q.InsertBackup(ctx, db.InsertBackupParams{ID: id, ServiceID: svc.ID, ObjectKey: key, CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()}); err != nil {
			return err
		}
		return Event(ctx, q, now.UnixMilli(), "backup.queued", id, actor, map[string]string{"service": svc.ID})
	})
	return id, err
}

func (a *App) BaseBackup(ctx context.Context, serviceID, actor string) (string, error) {
	svc, err := a.St.Q.GetService(ctx, serviceID)
	if err != nil {
		return "", err
	}
	id, now := store.NewID("bak_"), a.Now()
	key := fmt.Sprintf("yawl/%s/%s/%s/base/%s-%s.tar.age", svc.Project, svc.Env, svc.Name, now.UTC().Format("20060102T150405Z"), id)
	day := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC).UnixMilli()
	err = a.St.Tx(ctx, func(q *db.Queries) error {
		all, err := q.ListBackups(ctx, svc.ID)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(all, func(b db.Backup) bool {
			return b.Kind == "base" && b.CreatedAt >= day && b.State != "failed" && b.State != "deleted"
		}) {
			id = ""
			return nil
		}
		if err := q.InsertBaseBackup(ctx, db.InsertBaseBackupParams{ID: id, ServiceID: svc.ID, ObjectKey: key, CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()}); err != nil {
			return err
		}
		return Event(ctx, q, now.UnixMilli(), "backup.queued", id, actor, map[string]string{"service": svc.ID, "kind": "base"})
	})
	return id, err
}

func DatabaseVar(ctx context.Context, q *db.Queries, envID, serviceID, key string) (string, error) {
	rows, err := q.EnvVarsFor(ctx, db.EnvVarsForParams{EnvironmentID: envID, ServiceID: serviceID})
	if err != nil {
		return "", err
	}
	for _, v := range rows {
		if v.ServiceID == serviceID && v.Key == key {
			return v.AgentBox, nil
		}
	}
	return "", sql.ErrNoRows
}

type KeepRule struct {
	Daily, Weekly, Monthly int
}

var DefaultKeep = KeepRule{Daily: 7, Weekly: 4}

func RuleOf(k *spec.Keep) KeepRule {
	if k == nil {
		return DefaultKeep
	}
	return KeepRule{Daily: k.Daily, Weekly: k.Weekly, Monthly: k.Monthly}
}

func Keep(backups []db.Backup) []db.Backup { return KeepWith(backups, DefaultKeep) }

func KeepWith(backups []db.Backup, rule KeepRule) []db.Backup {
	var ok []db.Backup
	for _, b := range backups {
		if b.State == "ok" {
			ok = append(ok, b)
		}
	}
	slices.SortFunc(ok, func(a, b db.Backup) int { return int(b.CreatedAt - a.CreatedAt) })
	keep := map[string]bool{}
	if len(ok) > 0 {
		keep[ok[0].ID] = true
	}
	day := func(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02") }
	week := func(ms int64) string { y, w := time.UnixMilli(ms).UTC().ISOWeek(); return fmt.Sprint(y, "-", w) }
	month := func(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01") }
	for _, b := range []struct {
		n  int
		of func(int64) string
	}{{rule.Daily, day}, {rule.Weekly, week}, {rule.Monthly, month}} {
		seen := map[string]bool{}
		for _, x := range ok {
			if k := b.of(x.CreatedAt); !seen[k] && len(seen) < b.n {
				keep[x.ID], seen[k] = true, true
			}
		}
	}
	var drop []db.Backup
	for _, b := range ok {
		if !keep[b.ID] {
			drop = append(drop, b)
		}
	}
	return drop
}

func (a *App) BackupVolume(ctx context.Context, serviceID, volume, actor string) (string, error) {
	return a.backupVolume(ctx, serviceID, volume, actor, false)
}

func (a *App) CarryVolume(ctx context.Context, serviceID, volume, actor string) (string, error) {
	return a.backupVolume(ctx, serviceID, volume, actor, true)
}

func (a *App) backupVolume(ctx context.Context, serviceID, volume, actor string, detached bool) (string, error) {
	svc, err := a.St.Q.GetService(ctx, serviceID)
	if err != nil {
		return "", err
	}
	if !hasVolume(svc.VolumesJson, volume) && !(detached && slices.Contains(AllVolumes(svc.VolumesJson, svc.VolumesKept), volume)) {
		return "", fmt.Errorf("%s has no volume %s", svc.Name, volume)
	}
	if !svc.CurrentDeployID.Valid {
		return "", fmt.Errorf("%s has not been deployed yet; deploy it first", svc.Name)
	}
	id, now := store.NewID("bak_"), a.Now()
	key := fmt.Sprintf("yawl/%s/%s/%s/volumes/%s/%s-%s.tar.age", svc.Project, svc.Env, svc.Name, volume, now.UTC().Format("20060102T150405Z"), id)
	err = a.St.Tx(ctx, func(q *db.Queries) error {
		if err := q.InsertBackup(ctx, db.InsertBackupParams{ID: id, ServiceID: svc.ID, ObjectKey: key, Volume: volume, CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()}); err != nil {
			return err
		}
		return Event(ctx, q, now.UnixMilli(), "backup.queued", id, actor, map[string]string{"service": svc.ID, "volume": volume})
	})
	return id, err
}

func Volumes(js string) []spec.Volume {
	var vs []spec.Volume
	json.Unmarshal([]byte(js), &vs)
	return vs
}

func hasVolume(js, name string) bool {
	return slices.ContainsFunc(Volumes(js), func(v spec.Volume) bool { return v.Name == name })
}

func AllVolumes(volumesJSON, keptJSON string) []string {
	var names []string
	json.Unmarshal([]byte(keptJSON), &names)
	for _, v := range Volumes(volumesJSON) {
		names = append(names, v.Name)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func CopyIncident(serviceID, volume, copy string) string {
	if volume == "" {
		return "backupcopy:" + serviceID + ":" + copy
	}
	return "backupcopy:" + serviceID + ":" + volume + ":" + copy
}

func ForgetCopy(ctx context.Context, q *db.Queries, name string, now int64) error {
	failed, err := q.CopyIncidents(ctx, name)
	if err != nil {
		return err
	}
	for _, f := range failed {
		evID := store.NewID("evt_")
		if err := q.InsertEvent(ctx, db.InsertEventParams{ID: evID, Kind: "backup.copy_removed", SubjectID: f.ServiceID, Actor: "system",
			BodyJson: `{"copy":"` + name + `"}`, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if err := q.InsertOutbox(ctx, db.InsertOutboxParams{ID: store.NewID("obx_"), EventID: evID, Channel: "telegram", NextAttemptAt: now,
			Incident: CopyIncident(f.ServiceID, f.Volume, name), Action: "close", Quiet: 1, Label: f.Project + "/" + f.Env + "/" + f.Service,
			CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
	}
	return q.ForgetCopy(ctx, name)
}
