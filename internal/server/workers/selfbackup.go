package workers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/selfbackup"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

type SelfBackup struct {
	St        *store.Store
	V         *vault.Vault
	Data, Etc string
	Now       func() time.Time
	NewStore  func(ctx context.Context) (selfbackup.Store, error)
}

const selfKeep = 7

func (w *SelfBackup) setting(ctx context.Context, k string) string {
	v, _ := w.St.Q.GetSetting(ctx, k)
	return v
}

func (w *SelfBackup) set(ctx context.Context, k, v string) {
	w.St.Q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: v})
}

func (w *SelfBackup) store(ctx context.Context) (selfbackup.Store, error) {
	if w.NewStore != nil {
		return w.NewStore(ctx)
	}
	s, err := storageFrom(ctx, w.St.Q, w.V)
	if err != nil {
		return nil, err
	}
	return selfbackup.S3(s.Endpoint, s.Bucket, s.Region, s.AccessKey, s.SecretKey, s.Insecure)
}

func (w *SelfBackup) Tick(ctx context.Context) error {
	if app.Adopting(ctx, w.St.Q) {
		return nil
	}
	now := w.Now().UTC()
	if w.setting(ctx, "server_backup.since") == "" {
		w.set(ctx, "server_backup.since", strconv.FormatInt(now.UnixMilli(), 10))
	}
	at := w.setting(ctx, "server_backup.at")
	if at == "" {
		at = "04:00"
	}
	hm, err := time.Parse("15:04", at)
	if err != nil {
		hm, _ = time.Parse("15:04", "04:00")
	}
	due := time.Date(now.Year(), now.Month(), now.Day(), hm.Hour(), hm.Minute(), 0, 0, time.UTC)
	last, _ := strconv.ParseInt(w.setting(ctx, "server_backup.last_try"), 10, 64)
	if !now.Before(due) && last < due.UnixMilli() {
		w.set(ctx, "server_backup.last_try", strconv.FormatInt(now.UnixMilli(), 10))
		err := w.Run(ctx)
		if err != nil && !errors.Is(err, errNoStorage) && !errors.Is(err, errNoRecovery) {
			text := fmt.Sprintf("The panel's own backup failed: %s.", err)
			okAt, _ := strconv.ParseInt(w.setting(ctx, "server_backup.last_ok"), 10, 64)
			if err := w.St.Tx(ctx, func(q *db.Queries) error {
				return announceIncident(ctx, q, now.UnixMilli(), "panel.backup.failed", "panel", map[string]string{"cause": err.Error()},
					app.PanelBackupFailed(err.Error(), app.LastGood(okAt), text), "panel-backup", "open", "The panel", false)
			}); err != nil {
				return err
			}
		}
	}
	return w.checkMissed(ctx, now)
}

var errNoRecovery = errors.New("no recovery key yet")

func (w *SelfBackup) Run(ctx context.Context) error {
	pub, err := base64.StdEncoding.DecodeString(w.setting(ctx, "recovery.pubkey"))
	if err != nil || len(pub) != 32 {
		return errNoRecovery
	}
	st, err := w.store(ctx)
	if err != nil {
		return err
	}
	now := w.Now().UTC()
	var buf bytes.Buffer
	if err := selfbackup.Make(ctx, w.St.DB, w.Data, w.Etc, [32]byte(pub), &buf); err != nil {
		w.set(ctx, "server_backup.last_error", err.Error())
		return err
	}
	stamp := now.Format("2006-01-02T150405Z")
	key := selfbackup.Prefix + stamp + ".tar.age"
	if k, err := vault.GetSecret(ctx, w.St.Q, w.V, selfbackup.KeySetting); err == nil {
		key = selfbackup.Name(k, stamp, buf.Bytes())
	} else if !errors.Is(err, sql.ErrNoRows) {
		w.set(ctx, "server_backup.last_error", err.Error())
		return err
	}
	if err := st.Put(ctx, key, &buf, int64(buf.Len())); err != nil {
		w.set(ctx, "server_backup.last_error", err.Error())
		return err
	}
	w.set(ctx, "server_backup.last_ok", strconv.FormatInt(now.UnixMilli(), 10))
	w.set(ctx, "server_backup.last_key", key)
	w.set(ctx, "server_backup.last_error", "")
	w.St.Tx(ctx, func(q *db.Queries) error {
		return announceIncident(ctx, q, now.UnixMilli(), "panel.backup.recovered", "panel", nil, app.PanelBackupOK("Panel backup succeeded again."),
			"panel-backup", "close", "The panel", true)
	})
	keys, err := st.List(ctx, selfbackup.Prefix)
	if err != nil {
		return nil
	}
	for len(keys) > selfKeep {
		st.Delete(ctx, keys[0])
		keys = keys[1:]
	}
	return nil
}

func (w *SelfBackup) checkMissed(ctx context.Context, now time.Time) error {
	okAt, _ := strconv.ParseInt(w.setting(ctx, "server_backup.last_ok"), 10, 64)
	since, _ := strconv.ParseInt(w.setting(ctx, "server_backup.since"), 10, 64)
	ref := max(okAt, since)
	if now.Sub(time.UnixMilli(ref)) < missedAfter {
		return nil
	}
	if n, err := w.St.Q.CountEventsFor(ctx, db.CountEventsForParams{Kind: "panel.backup.missed", SubjectID: "panel", CreatedAt: ref}); err != nil || n > 0 {
		return err
	}
	last := "never"
	if okAt > 0 {
		last = time.UnixMilli(okAt).UTC().Format("2006-01-02 15:04 UTC")
	}
	text := fmt.Sprintf("The panel's own backup has not succeeded in 26 hours (last: %s).", last)
	return w.St.Tx(ctx, func(q *db.Queries) error {
		return announceIncident(ctx, q, now.UnixMilli(), "panel.backup.missed", "panel", map[string]string{"last": last}, app.PanelBackupMissed(app.LastGood(okAt), text),
			"panel-backup", "open", "The panel", false)
	})
}
