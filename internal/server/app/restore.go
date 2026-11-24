package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

var dbNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

func (a *App) Restore(ctx context.Context, fromServiceID, backupID, into, recoveryBox, actor string) (string, error) {
	return a.RestoreFrom(ctx, fromServiceID, backupID, into, recoveryBox, "", actor)
}

func (a *App) goodCopy(ctx context.Context, backupID, copy string) error {
	if copy == "" {
		return nil
	}
	if n, err := a.St.Q.GoodCopy(ctx, db.GoodCopyParams{BackupID: backupID, Copy: copy}); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("%s has no good copy in %s", backupID, copy)
	}
	return nil
}

func (a *App) RestoreFrom(ctx context.Context, fromServiceID, backupID, into, recoveryBox, copy, actor string) (string, error) {
	if !dbNameRe.MatchString(into) {
		return "", fmt.Errorf("%q: a database name is lowercase letters, digits and dashes", into)
	}
	from, err := a.St.Q.GetService(ctx, fromServiceID)
	if err != nil {
		return "", err
	}
	b, err := a.St.Q.GetBackup(ctx, backupID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && b.ServiceID != from.ID) {
		return "", fmt.Errorf("%s is not a backup of %s", backupID, from.Name)
	} else if err != nil {
		return "", err
	}
	if b.State != "ok" {
		return "", fmt.Errorf("backup %s is %s, not ok; pick one from yawl db backups", backupID, b.State)
	}
	if b.Kind == "base" {
		return "", fmt.Errorf("%s is a base backup for restoring to a time; pick a dump from yawl db backups", backupID)
	}
	if err := a.goodCopy(ctx, backupID, copy); err != nil {
		return "", err
	}
	return a.newRestore(ctx, from, backupID, 0, into, recoveryBox, copy, actor)
}

func (a *App) newRestore(ctx context.Context, from db.GetServiceRow, backupID string, at int64, into, recoveryBox, copy, actor string) (string, error) {
	svcID, depID, now := store.NewID("svc_"), store.NewID("dep_"), a.Now().UnixMilli()
	err := a.St.Tx(ctx, func(q *db.Queries) error {
		before, err := q.CountLiveServices(ctx)
		if err != nil {
			return err
		}
		if _, err := q.GetServiceInEnv(ctx, db.GetServiceInEnvParams{EnvironmentID: from.EnvironmentID, Name: into}); err == nil {
			return fmt.Errorf("%s already exists; restore goes into a new database.", into)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := q.UpsertService(ctx, db.UpsertServiceParams{ID: svcID, EnvironmentID: from.EnvironmentID, Name: into, Kind: from.Kind,
			Version: from.Version, BackupAt: from.BackupAt, Backup: from.Backup, Cpu: from.Cpu, Memory: from.Memory, KeepJson: from.KeepJson, AutoDeploy: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if err := a.checkServices(ctx, q, before); err != nil {
			return err
		}
		if at != 0 {
			if err := q.CreateRecoverDeploy(ctx, db.CreateRecoverDeployParams{ID: depID, ServiceID: svcID, RestoreFrom: backupID, RestoreAt: at, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
		} else if err := q.CreateRestoreDeploy(ctx, db.CreateRestoreDeployParams{ID: depID, ServiceID: svcID, RestoreFrom: backupID, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if copy != "" {
			if err := q.SetRestoreStorage(ctx, db.SetRestoreStorageParams{RestoreStorage: copy, ID: depID}); err != nil {
				return err
			}
		}
		return Event(ctx, q, now, "restore.queued", depID, actor, map[string]string{"from": from.ID, "backup": backupID, "into": into})
	})
	if err != nil {
		return "", err
	}
	a.KeepRestoreKey(depID, recoveryBox)
	return depID, nil
}

func (a *App) RestoreAt(ctx context.Context, fromServiceID string, at time.Time, into, recoveryBox, actor string) (string, error) {
	if !dbNameRe.MatchString(into) {
		return "", fmt.Errorf("%q: a database name is lowercase letters, digits and dashes", into)
	}
	from, err := a.St.Q.GetService(ctx, fromServiceID)
	if err != nil {
		return "", err
	}
	base, batches, end, err := a.pitrWindow(ctx, from)
	if err != nil {
		return "", err
	}
	var pick *db.Backup
	for i := range base {
		if base[i].FinishedAt.Int64 <= at.UnixMilli() {
			pick = &base[i]
			break
		}
	}
	if pick == nil || at.UnixMilli() > end {
		f := "2006-01-02 15:04:05 UTC"
		return "", fmt.Errorf("Choose a time between %s and %s.", time.UnixMilli(base[len(base)-1].FinishedAt.Int64).UTC().Format(f), time.UnixMilli(end).UTC().Format(f))
	}
	if _, _, gap := WALFor(batches, *pick, at); gap != 0 {
		f := "2006-01-02 15:04:05 UTC"
		return "", fmt.Errorf("Changes after %s were not all uploaded, so %s cannot be reached. Choose an earlier time.", time.UnixMilli(gap).UTC().Format(f), at.UTC().Format(f))
	}
	return a.newRestore(ctx, from, pick.ID, at.UnixMilli(), into, recoveryBox, "", actor)
}

func (a *App) pitrWindow(ctx context.Context, svc db.GetServiceRow) ([]db.Backup, []db.WalBatch, int64, error) {
	all, err := a.St.Q.ListBackups(ctx, svc.ID)
	if err != nil {
		return nil, nil, 0, err
	}
	bases := slices.DeleteFunc(all, func(b db.Backup) bool { return b.Kind != "base" || b.State != "ok" })
	batches, err := a.St.Q.ListWALBatches(ctx, svc.ID)
	if err != nil {
		return nil, nil, 0, err
	}
	var shipped int64
	if v, err := a.St.Q.GetSetting(ctx, "pitr.shipped."+svc.ID); err == nil {
		shipped, _ = strconv.ParseInt(v, 10, 64)
	}
	for _, b := range slices.Backward(batches) {
		if b.Backlog == 0 {
			shipped = max(shipped, b.CreatedAt)
			break
		}
	}
	if len(bases) == 0 || shipped == 0 {
		return nil, nil, 0, fmt.Errorf("%s has no base backup yet; turn on PITR and the first one is taken at its backup time.", svc.Name)
	}
	return bases, batches, shipped - walSlack.Milliseconds(), nil
}

func (a *App) PITRWindow(ctx context.Context, serviceID string) (from, to, shipped int64, err error) {
	svc, err := a.St.Q.GetService(ctx, serviceID)
	if err != nil {
		return 0, 0, 0, err
	}
	bases, _, end, err := a.pitrWindow(ctx, svc)
	if err != nil {
		return 0, 0, 0, nil
	}
	return bases[len(bases)-1].FinishedAt.Int64, end, end + walSlack.Milliseconds(), nil
}

const (
	pgArchiveTimeout = 60 * time.Second
	agentWALQuiet    = 10 * time.Second
	walShipperEvery  = time.Minute
	walSlackHeadroom = 50 * time.Second
	walSlack         = pgArchiveTimeout + agentWALQuiet + walShipperEvery + walSlackHeadroom
)

func WALFor(batches []db.WalBatch, base db.Backup, at time.Time) (keys, sums []string, gap int64) {
	var prev *db.WalBatch
	for i, w := range batches {
		if w.CreatedAt < base.CreatedAt {
			continue
		}
		if prev != nil && isSegment(prev.LastWal) && isSegment(w.FirstWal) && nextSegment(prev.LastWal) != w.FirstWal {
			return keys, sums, prev.CreatedAt - walSlack.Milliseconds()
		}
		keys, sums = append(keys, w.ObjectKey), append(sums, w.Sha256)
		if isSegment(w.LastWal) {
			prev = &batches[i]
		}
		if w.Backlog == 0 && w.CreatedAt >= at.Add(walSlack).UnixMilli() {
			break
		}
	}
	return keys, sums, 0
}

var segmentRe = regexp.MustCompile(`^[0-9A-F]{24}$`)

func isSegment(name string) bool { return segmentRe.MatchString(name) }

// nextSegment is the WAL file after name: timeline, log and segment in hex,
// 256 segments of 16MB to a log.
func nextSegment(name string) string {
	tli, log, seg := name[:8], name[8:16], name[16:]
	l, _ := strconv.ParseUint(log, 16, 32)
	n, _ := strconv.ParseUint(seg, 16, 32)
	if n++; n > 0xFF {
		n, l = 0, l+1
	}
	return fmt.Sprintf("%s%08X%08X", tli, l, n)
}

func (a *App) KeepRestoreKey(deployID, box string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.restoreKeys == nil {
		a.restoreKeys = map[string]string{}
	}
	a.restoreKeys[deployID] = box
}

func (a *App) TakeRestoreKey(deployID string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	k, ok := a.restoreKeys[deployID]
	delete(a.restoreKeys, deployID)
	return k, ok
}

func (a *App) RestoreVolume(ctx context.Context, serviceID, backupID, recoveryBox, actor string) (string, error) {
	return a.RestoreVolumeFrom(ctx, serviceID, backupID, recoveryBox, "", actor)
}

func (a *App) RestoreVolumeFrom(ctx context.Context, serviceID, backupID, recoveryBox, copy, actor string) (string, error) {
	if err := a.goodCopy(ctx, backupID, copy); err != nil {
		return "", err
	}
	svc, err := a.St.Q.GetService(ctx, serviceID)
	if err != nil {
		return "", err
	}
	b, err := a.St.Q.GetBackup(ctx, backupID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (b.ServiceID != svc.ID || b.Volume == "")) {
		return "", fmt.Errorf("%s is not a volume backup of %s", backupID, svc.Name)
	} else if err != nil {
		return "", err
	}
	if b.State != "ok" {
		return "", fmt.Errorf("backup %s is %s, not ok", backupID, b.State)
	}
	if !hasVolume(svc.VolumesJson, b.Volume) {
		return "", fmt.Errorf("%s no longer has the volume %s", svc.Name, b.Volume)
	}
	if !svc.CurrentDeployID.Valid {
		return "", fmt.Errorf("%s has not been deployed yet; deploy it first", svc.Name)
	}
	cur, err := a.St.Q.GetDeploy(ctx, svc.CurrentDeployID.String)
	if err != nil {
		return "", err
	}
	depID, now := store.NewID("dep_"), a.Now().UnixMilli()
	err = a.St.Tx(ctx, func(q *db.Queries) error {
		if n, err := q.ActiveDeploys(ctx, svc.ID); err != nil {
			return err
		} else if n > 0 {
			return fmt.Errorf("%s has a deploy in progress; wait for it to finish.", svc.Name)
		}
		if err := q.CreateVolumeRestoreDeploy(ctx, db.CreateVolumeRestoreDeployParams{ID: depID, ServiceID: svc.ID, CommitSha: cur.CommitSha, Subject: cur.Subject, Actor: actor, Image: cur.Image,
			RestoreFrom: b.ID, Volume: b.Volume, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if copy != "" {
			if err := q.SetRestoreStorage(ctx, db.SetRestoreStorageParams{RestoreStorage: copy, ID: depID}); err != nil {
				return err
			}
		}
		return Event(ctx, q, now, "restore.queued", depID, actor, map[string]string{"service": svc.ID, "backup": b.ID, "volume": b.Volume})
	})
	if err != nil {
		return "", err
	}
	a.KeepRestoreKey(depID, recoveryBox)
	return depID, nil
}
