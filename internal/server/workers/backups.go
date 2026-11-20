package workers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/notify"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

type Backups struct {
	St  *store.Store
	GW  Sender
	V   *vault.Vault
	Now func() time.Time

	copying sync.Map
	copies  sync.WaitGroup
	last    time.Time
}

func (b *Backups) Wait() { b.copies.Wait() }

const (
	backupTimeout = 2 * time.Hour
	backupStale   = 3 * time.Hour
	missedAfter   = 26 * time.Hour
)

var errNoStorage = errors.New("backup storage is not set: run yawl server storage set")

var ErrNoStorage, ErrNoRecovery = errNoStorage, errNoRecovery

func (b *Backups) Tick(ctx context.Context) error {
	if app.Adopting(ctx, b.St.Q) {
		return nil
	}
	now, last := b.Now(), b.last
	b.last = now
	if err := b.failStale(ctx, now); err != nil {
		return err
	}
	dbs, err := b.St.Q.ListDatabaseServices(ctx)
	if err != nil {
		return err
	}
	a := app.New(b.St)
	a.Now = b.Now
	for _, d := range dbs {
		due, ok := dueAt(now, last, d.BackupAt)
		if !ok {
			continue
		}
		if n, err := b.St.Q.BackupsSince(ctx, db.BackupsSinceParams{ServiceID: d.ID, CreatedAt: due.UnixMilli()}); err != nil || n > 0 {
			continue
		}
		if _, err := a.Backup(ctx, d.ID, "schedule"); err == nil && d.Pitr == 1 {
			a.BaseBackup(ctx, d.ID, "schedule")
		}
	}
	vols, err := b.St.Q.ListVolumeServices(ctx)
	if err != nil {
		return err
	}
	for _, s := range vols {
		due, ok := dueAt(now, last, s.BackupAt)
		if !ok {
			continue
		}
		for _, v := range app.Volumes(s.VolumesJson) {
			if n, err := b.St.Q.BackupsSince(ctx, db.BackupsSinceParams{ServiceID: s.ID, Volume: v.Name, CreatedAt: due.UnixMilli()}); err != nil || n > 0 {
				continue
			}
			a.BackupVolume(ctx, s.ID, v.Name, "schedule")
		}
	}
	queued, err := b.St.Q.QueuedBackups(ctx)
	if err != nil {
		return err
	}
	for _, q := range queued {
		if err := b.run(ctx, q); err != nil {
			return err
		}
	}
	if err := b.copyMissing(ctx); err != nil {
		return err
	}
	for _, d := range dbs {
		if err := b.checkMissed(ctx, d, b.Now()); err != nil {
			return err
		}
	}
	for _, v := range vols {
		for _, vol := range app.Volumes(v.VolumesJson) {
			if err := b.checkVolumeMissed(ctx, v, vol.Name, b.Now()); err != nil {
				return err
			}
		}
	}
	return nil
}

func dueAt(now, last time.Time, backupAt string) (time.Time, bool) {
	at, err := time.Parse("15:04", backupAt)
	if err != nil {
		return time.Time{}, false
	}
	due := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), at.Hour(), at.Minute(), 0, 0, time.UTC)
	if !now.Before(due) {
		return due, true
	}
	due = due.AddDate(0, 0, -1)
	return due, !last.IsZero() && last.Before(due)
}

func (b *Backups) failStale(ctx context.Context, now time.Time) error {
	running, err := b.St.Q.RunningBackups(ctx)
	if err != nil {
		return err
	}
	for _, r := range running {
		if r.StartedAt.Valid && now.Sub(time.UnixMilli(r.StartedAt.Int64)) > backupStale {
			if err := b.fail(ctx, r, "the agent did not finish this backup (server restarted or agent lost)"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *Backups) storage(ctx context.Context) (proto.Storage, error) {
	return storageFrom(ctx, b.St.Q, b.V)
}

func storageFrom(ctx context.Context, q *db.Queries, v *vault.Vault) (proto.Storage, error) {
	if v == nil {
		return proto.Storage{}, errNoStorage
	}
	endpoint, err1 := q.GetSetting(ctx, "storage.endpoint")
	bucket, err2 := q.GetSetting(ctx, "storage.bucket")
	ak, err3 := vault.GetSecret(ctx, q, v, "storage.access_key")
	sk, err4 := vault.GetSecret(ctx, q, v, "storage.secret_key")
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		return proto.Storage{}, errNoStorage
	}
	region, _ := q.GetSetting(ctx, "storage.region")
	return storageOf(endpoint, bucket, region, string(ak), string(sk)), nil
}

func (b *Backups) run(ctx context.Context, r db.Backup) error {
	svc, err := b.St.Q.GetService(ctx, r.ServiceID)
	if err != nil {
		return err
	}
	if c, ok := b.GW.(interface{ Connected(string) bool }); ok && !c.Connected(svc.ServerID) {
		return nil
	}
	storage, err := b.storage(ctx)
	if err != nil {
		return b.fail(ctx, r, err.Error())
	}
	rec, err := b.St.Q.GetSetting(ctx, "recovery.pubkey")
	if err != nil {
		return b.fail(ctx, r, "no recovery key yet; run yawl recovery-key init first")
	}
	var cmd proto.Command
	if r.Volume != "" {
		cmd, err = proto.NewCommand("backup:"+r.ID, proto.CmdBackupVolume, proto.BackupVolumeArgs{Project: svc.Project, Env: svc.Env, Service: svc.Name,
			Name: r.Volume, Storage: storage, Key: r.ObjectKey, RecoveryPubkey: rec})
	} else {
		pw, perr := app.DatabaseVar(ctx, b.St.Q, svc.EnvironmentID, svc.ID, "PASSWORD")
		if perr != nil {
			return b.fail(ctx, r, svc.Name+" has no password on this server; deploy it first")
		}
		cmd, err = proto.NewCommand("backup:"+r.ID, proto.CmdBackup, proto.BackupArgs{Kind: svc.Kind, Project: svc.Project, Env: svc.Env, Name: svc.Name,
			Version: int(svc.Version), Password: pw, Storage: storage, Key: r.ObjectKey, RecoveryPubkey: rec, Base: r.Kind == "base"})
	}
	if err != nil {
		return err
	}
	now := b.Now().UnixMilli()
	if err := b.St.Q.StartBackup(ctx, db.StartBackupParams{StartedAt: sql.NullInt64{Int64: now, Valid: true}, UpdatedAt: now, ID: r.ID}); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, backupTimeout)
	defer cancel()
	res, err := b.GW.Send(cctx, svc.ServerID, cmd)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return b.fail(ctx, r, "the agent did not answer: "+err.Error())
	}
	if !res.OK {
		return b.fail(ctx, r, res.Cause)
	}
	var data proto.BackupData
	if err := json.Unmarshal(res.Data, &data); err != nil {
		return b.fail(ctx, r, "the agent's answer could not be read")
	}
	fin := b.Now().UnixMilli()
	if err := b.St.Tx(ctx, func(q *db.Queries) error {
		if err := q.FinishBackup(ctx, db.FinishBackupParams{State: "ok", Size: data.Size, Sha256: data.SHA256,
			FinishedAt: sql.NullInt64{Int64: fin, Valid: true}, UpdatedAt: fin, ID: r.ID}); err != nil {
			return err
		}
		if err := app.Event(ctx, q, fin, "backup.ok", r.ID, "system", map[string]any{"service": svc.ID, "size": data.Size}); err != nil {
			return err
		}
		if err := announceIncident(ctx, q, fin, "backup.recovered", r.ID, nil, app.BackupOK(svc.Project+"/"+svc.Env+"/"+svc.Name, r.Volume, backupName(svc.Name, r.Volume)+" backup succeeded again."),
			failedKey(svc.ID, r.Volume), "close", svc.Project+"/"+svc.Env+"/"+svc.Name, true); err != nil {
			return err
		}
		missed := "backup:" + svc.ID
		if r.Volume != "" {
			missed += ":" + r.Volume
		}
		return announceIncident(ctx, q, fin, "backup.recovered", r.ID, nil, notify.Message{}, missed, "close", svc.Project+"/"+svc.Env+"/"+svc.Name, true)
	}); err != nil {
		return err
	}
	if r.Kind != "base" {
		b.copyLater(ctx, svc, r, storage)
	}
	return b.prune(ctx, svc.ServerID, svc.ID, r.Volume, r.ID, storage)
}

func (b *Backups) copyLater(ctx context.Context, svc db.GetServiceRow, r db.Backup, storage proto.Storage) {
	if _, busy := b.copying.LoadOrStore(svc.ID, true); !busy {
		b.copies.Add(1)
		go func() {
			defer b.copies.Done()
			defer b.copying.Delete(svc.ID)
			b.copyOut(context.WithoutCancel(ctx), svc, r, storage)
		}()
	}
}

func (b *Backups) copyMissing(ctx context.Context) error {
	missing, err := b.St.Q.BackupsMissingCopy(ctx)
	if err != nil || len(missing) == 0 {
		return err
	}
	storage, err := b.storage(ctx)
	if err != nil {
		return nil
	}
	for _, r := range missing {
		if svc, err := b.St.Q.GetService(ctx, r.ServiceID); err == nil {
			b.copyLater(ctx, svc, r, storage)
		}
	}
	return nil
}

type copyDest struct {
	name    string
	storage proto.Storage
}

func copyStorages(ctx context.Context, q *db.Queries, v *vault.Vault) []copyDest {
	if v == nil {
		return nil
	}
	rows, err := q.ListStorageCopies(ctx)
	if err != nil {
		return nil
	}
	var out []copyDest
	for _, c := range rows {
		ak, err1 := vault.GetSecret(ctx, q, v, "storage.copy."+c.Name+".access_key")
		sk, err2 := vault.GetSecret(ctx, q, v, "storage.copy."+c.Name+".secret_key")
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, copyDest{c.Name, storageOf(c.Endpoint, c.Bucket, c.Region, string(ak), string(sk))})
	}
	return out
}

func storageOf(endpoint, bucket, region, ak, sk string) proto.Storage {
	insecure := strings.HasPrefix(endpoint, "http://")
	endpoint = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://"), "/")
	return proto.Storage{Endpoint: endpoint, Bucket: bucket, Region: region, AccessKey: ak, SecretKey: sk, Insecure: insecure}
}

func (b *Backups) copyOut(ctx context.Context, svc db.GetServiceRow, r db.Backup, from proto.Storage) error {
	label := svc.Project + "/" + svc.Env + "/" + svc.Name
	record := func(copy, state, cause string) error {
		now, key := b.Now().UnixMilli(), app.CopyIncident(svc.ID, r.Volume, copy)
		return b.St.Tx(ctx, func(q *db.Queries) error {
			if err := q.SetBackupCopy(ctx, db.SetBackupCopyParams{BackupID: r.ID, Copy: copy, State: state, Cause: cause, UpdatedAt: now}); err != nil {
				return err
			}
			if state == "ok" {
				return announceIncident(ctx, q, now, "backup.copy_recovered", r.ID, nil, notify.Message{}, key, "close", label, true)
			}
			return announceIncident(ctx, q, now, "backup.copy_failed", r.ID, map[string]string{"copy": copy, "cause": cause},
				app.BackupCopyFailed(label, r.Volume, copy, cause, fmt.Sprintf("%s backup copy to %s failed: %s", backupName(svc.Name, r.Volume), copy, cause)), key, "open", label, false)
		})
	}
	done := map[string]bool{}
	if rows, err := b.St.Q.CopiesOfService(ctx, svc.ID); err == nil {
		for _, c := range rows {
			if c.BackupID == r.ID {
				done[c.Copy] = true
			}
		}
	}
	dests := copyStorages(ctx, b.St.Q, b.V)
	all, _ := b.St.Q.ListStorageCopies(ctx)
	for _, c := range all {
		if !done[c.Name] && !slices.ContainsFunc(dests, func(d copyDest) bool { return d.name == c.Name }) {
			if err := record(c.Name, "failed", "its keys are missing; set them again in Settings, Storage"); err != nil {
				return err
			}
		}
	}
	for _, c := range dests {
		if done[c.name] {
			continue
		}
		cmd, err := proto.NewCommand("backup:"+r.ID+":copy:"+c.name, proto.CmdCopyBackup, proto.CopyBackupArgs{From: from, To: c.storage, Key: r.ObjectKey})
		if err != nil {
			return err
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
		res, err := b.GW.Send(cctx, svc.ServerID, cmd)
		cancel()
		state, cause := "ok", ""
		if err != nil {
			state, cause = "failed", err.Error()
		} else if !res.OK {
			state, cause = "failed", res.Cause
		}
		if err := record(c.name, state, cause); err != nil {
			return err
		}
	}
	return nil
}

func failedKey(serviceID, volume string) string {
	if volume == "" {
		return "backup:" + serviceID + ":failed"
	}
	return "backup:" + serviceID + ":" + volume + ":failed"
}

func backupName(service, volume string) string {
	if volume == "" {
		return service
	}
	return service + " volume " + volume
}

func (b *Backups) prune(ctx context.Context, serverID, serviceID, volume, after string, storage proto.Storage) error {
	var reading int
	if err := b.St.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploys d JOIN backups k ON k.id = d.restore_from
		WHERE k.service_id = ? AND d.state NOT IN ('live', 'failed', 'rolled_back', 'superseded')`, serviceID).Scan(&reading); err != nil || reading > 0 {
		return err
	}
	all, err := b.St.Q.ListBackups(ctx, serviceID)
	if err != nil {
		return err
	}
	all = slices.DeleteFunc(all, func(x db.Backup) bool { return x.Volume != volume })
	dumps := slices.DeleteFunc(slices.Clone(all), func(x db.Backup) bool { return x.Kind == "base" })
	rule := app.DefaultKeep
	if svc, err := b.St.Q.GetService(ctx, serviceID); err == nil {
		rule = app.RuleOf(app.KeepOf(svc.KeepJson))
	}
	drop := app.KeepWith(dumps, rule)
	byKey, batchByKey := map[string]string{}, map[string]string{}
	var keys []string
	for _, d := range drop {
		byKey[d.ObjectKey] = d.ID
		keys = append(keys, d.ObjectKey)
	}
	if volume == "" {
		var bases []db.Backup
		for _, x := range all {
			if x.Kind == "base" && x.State == "ok" {
				bases = append(bases, x)
			}
		}
		if len(bases) > 0 {
			kept := bases[:min(len(bases), 7)]
			for _, x := range bases[len(kept):] {
				byKey[x.ObjectKey] = x.ID
				keys = append(keys, x.ObjectKey)
			}
			batches, err := b.St.Q.ListWALBatches(ctx, serviceID)
			if err != nil {
				return err
			}
			for _, w := range batches {
				if w.CreatedAt < kept[len(kept)-1].CreatedAt {
					batchByKey[w.ObjectKey] = w.ID
					keys = append(keys, w.ObjectKey)
				}
			}
		}
	}
	var data proto.DeleteBackupsData
	if len(keys) > 0 {
		cmd, err := proto.NewCommand("backup:"+after+":prune", proto.CmdDeleteBackups, proto.DeleteBackupsArgs{Storage: storage, Keys: keys})
		if err != nil {
			return err
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		res, err := b.GW.Send(cctx, serverID, cmd)
		cancel()
		if err == nil && res.OK {
			json.Unmarshal(res.Data, &data)
		}
	}
	if err := b.markDeleted(ctx, data.Deleted, byKey, batchByKey); err != nil {
		return err
	}
	return b.pruneCopies(ctx, serverID, serviceID, after, data.Deleted)
}

func (b *Backups) pruneCopies(ctx context.Context, serverID, serviceID, after string, deleted []string) error {
	for _, c := range copyStorages(ctx, b.St.Q, b.V) {
		left, err := b.St.Q.CopyLeftovers(ctx, db.CopyLeftoversParams{Copy: c.name, ServiceID: serviceID})
		if err != nil {
			return err
		}
		keys, byKey := slices.Clone(deleted), map[string]string{}
		for _, l := range left {
			byKey[l.ObjectKey] = l.ID
			if !slices.Contains(keys, l.ObjectKey) {
				keys = append(keys, l.ObjectKey)
			}
		}
		if len(keys) == 0 {
			continue
		}
		cmd, err := proto.NewCommand("backup:"+after+":prune:"+c.name, proto.CmdDeleteBackups, proto.DeleteBackupsArgs{Storage: c.storage, Keys: keys})
		if err != nil {
			return err
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		res, err := b.GW.Send(cctx, serverID, cmd)
		cancel()
		var data proto.DeleteBackupsData
		if err != nil || !res.OK || json.Unmarshal(res.Data, &data) != nil {
			continue
		}
		for _, k := range data.Deleted {
			if id, ok := byKey[k]; ok {
				if err := b.St.Q.DropBackupCopy(ctx, db.DropBackupCopyParams{BackupID: id, Copy: c.name}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (b *Backups) markDeleted(ctx context.Context, deleted []string, byKey, batchByKey map[string]string) error {
	now := b.Now().UnixMilli()
	return b.St.Tx(ctx, func(q *db.Queries) error {
		for _, k := range deleted {
			if id, ok := byKey[k]; ok {
				if err := q.MarkBackupDeleted(ctx, db.MarkBackupDeletedParams{UpdatedAt: now, ID: id}); err != nil {
					return err
				}
			}
			if id, ok := batchByKey[k]; ok {
				if err := q.DeleteWALBatch(ctx, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (b *Backups) fail(ctx context.Context, r db.Backup, cause string) error {
	svc, err := b.St.Q.GetService(ctx, r.ServiceID)
	if err != nil {
		return err
	}
	now := b.Now().UnixMilli()
	return b.St.Tx(ctx, func(q *db.Queries) error {
		if err := q.FinishBackup(ctx, db.FinishBackupParams{State: "failed", Cause: cause,
			FinishedAt: sql.NullInt64{Int64: now, Valid: true}, UpdatedAt: now, ID: r.ID}); err != nil {
			return err
		}
		label := svc.Project + "/" + svc.Env + "/" + svc.Name
		return announceIncident(ctx, q, now, "backup.failed", r.ID, map[string]string{"service": svc.ID, "cause": cause},
			app.BackupFailed(label, r.Volume, cause, app.LastGood(lastOK(ctx, q, svc.ID, r.Volume)), fmt.Sprintf("%s backup failed: %s", backupName(svc.Name, r.Volume), cause)),
			failedKey(svc.ID, r.Volume), "open", label, false)
	})
}

func (b *Backups) checkMissed(ctx context.Context, d db.ListBackupCandidatesRow, now time.Time) error {
	since, last, okAt := d.CreatedAt, "never", int64(0)
	if v, err := b.St.Q.GetSetting(ctx, "backup.since."+d.ID); err == nil {
		if t, _ := strconv.ParseInt(v, 10, 64); t > since {
			since = t
		}
	}
	if now.Sub(time.UnixMilli(since)) < missedAfter {
		return nil
	}
	if ok, err := b.St.Q.LatestOKBackup(ctx, d.ID); err == nil {
		t := ok.CreatedAt
		if ok.FinishedAt.Valid {
			t = ok.FinishedAt.Int64
		}
		if now.Sub(time.UnixMilli(t)) < missedAfter {
			return nil
		}
		since, last, okAt = max(since, t), time.UnixMilli(t).UTC().Format("2006-01-02 15:04")+" UTC", t
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if n, err := b.St.Q.CountEventsFor(ctx, db.CountEventsForParams{Kind: "backup.missed", SubjectID: d.ID, CreatedAt: since}); err != nil || n > 0 {
		return err
	}
	text := fmt.Sprintf("%s: no backup has succeeded in 26 hours (last: %s).", d.Name, last)
	_, noStorage := b.storage(ctx)
	if noStorage != nil {
		text += " Backup storage is not set: run yawl server storage set."
	}
	label := d.Project + "/" + d.Env + "/" + d.Name
	return b.St.Tx(ctx, func(q *db.Queries) error {
		return announceIncident(ctx, q, now.UnixMilli(), "backup.missed", d.ID, map[string]string{"last": last},
			app.BackupMissed(label, "", app.LastGood(okAt), noStorage != nil, text), "backup:"+d.ID, "open", label, false)
	})
}

func (b *Backups) checkVolumeMissed(ctx context.Context, s db.ListVolumeServicesRow, volume string, now time.Time) error {
	if now.Sub(time.UnixMilli(s.CreatedAt)) < missedAfter {
		return nil
	}
	since, last, okAt := s.CreatedAt, "never", int64(0)
	if ok, err := b.St.Q.LatestOKVolumeBackup(ctx, db.LatestOKVolumeBackupParams{ServiceID: s.ID, Volume: volume}); err == nil {
		t := ok.CreatedAt
		if ok.FinishedAt.Valid {
			t = ok.FinishedAt.Int64
		}
		if now.Sub(time.UnixMilli(t)) < missedAfter {
			return nil
		}
		since, last, okAt = t, time.UnixMilli(t).UTC().Format("2006-01-02 15:04")+" UTC", t
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	subject := s.ID + ":" + volume
	if n, err := b.St.Q.CountEventsFor(ctx, db.CountEventsForParams{Kind: "backup.missed", SubjectID: subject, CreatedAt: since}); err != nil || n > 0 {
		return err
	}
	text := fmt.Sprintf("%s: no backup has succeeded in 26 hours (last: %s).", backupName(s.Name, volume), last)
	_, noStorage := b.storage(ctx)
	if noStorage != nil {
		text += " Backup storage is not set: run yawl server storage set."
	}
	label := s.Project + "/" + s.Env + "/" + s.Name
	return b.St.Tx(ctx, func(q *db.Queries) error {
		return announceIncident(ctx, q, now.UnixMilli(), "backup.missed", subject, map[string]string{"last": last},
			app.BackupMissed(label, volume, app.LastGood(okAt), noStorage != nil, text), "backup:"+subject, "open", label, false)
	})
}

func announce(ctx context.Context, q *db.Queries, now int64, kind, subject string, body any, m notify.Message) error {
	return announceIncident(ctx, q, now, kind, subject, body, m, "", "", "", false)
}

func announceIncident(ctx context.Context, q *db.Queries, now int64, kind, subject string, body any, m notify.Message, incident, action, label string, quiet bool) error {
	if quiet {
		if u, _ := q.GetSetting(ctx, "watch.url"); u == "" {
			return nil
		}
	}
	evID := store.NewID("evt_")
	b, _ := json.Marshal(body)
	if err := q.InsertEvent(ctx, db.InsertEventParams{ID: evID, Kind: kind, SubjectID: subject, Actor: "system", BodyJson: string(b), CreatedAt: now, UpdatedAt: now}); err != nil {
		return err
	}
	return q.InsertOutbox(ctx, db.InsertOutboxParams{ID: store.NewID("obx_"), EventID: evID, Channel: "telegram", Body: m.Text, MessageJson: m.JSON(now), NextAttemptAt: now,
		Incident: incident, Action: action, Quiet: boolInt(quiet), Label: label, CreatedAt: now, UpdatedAt: now})
}

func lastOK(ctx context.Context, q *db.Queries, id, volume string) int64 {
	var b db.Backup
	var err error
	if volume == "" {
		b, err = q.LatestOKBackup(ctx, id)
	} else {
		b, err = q.LatestOKVolumeBackup(ctx, db.LatestOKVolumeBackupParams{ServiceID: id, Volume: volume})
	}
	if err != nil {
		return 0
	}
	if b.FinishedAt.Valid {
		return b.FinishedAt.Int64
	}
	return b.CreatedAt
}

func openFor(ctx context.Context, q *db.Queries, incident string, now int64) string {
	at, err := q.IncidentOpenedAt(ctx, incident)
	if err != nil {
		return ""
	}
	return Lasted(time.Duration(now-at) * time.Millisecond)
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
