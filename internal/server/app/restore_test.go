package app

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func restoreSeed(t *testing.T) (*App, *store.Store) {
	st, _ := seed(t)
	ctx := context.Background()
	st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_db", EnvironmentID: "env_1", Name: "acme-db", Kind: "postgres", Version: 16, BackupAt: "04:30", CreatedAt: 1, UpdatedAt: 1})
	st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "v", EnvironmentID: "env_1", ServiceID: "svc_db", Key: "URL", AgentBox: "u", RecoveryBox: "r"})
	for _, b := range []struct{ id, state string }{{"bak_ok", "ok"}, {"bak_bad", "failed"}} {
		st.Q.InsertBackup(ctx, db.InsertBackupParams{ID: b.id, ServiceID: "svc_db", ObjectKey: "k/" + b.id, CreatedAt: 1, UpdatedAt: 1})
		st.Q.FinishBackup(ctx, db.FinishBackupParams{State: b.state, UpdatedAt: 1, ID: b.id})
	}
	return New(st), st
}

func TestRestoreRefusesExisting(t *testing.T) {
	a, _ := restoreSeed(t)
	for _, into := range []string{"acme-db", "api"} {
		_, err := a.Restore(context.Background(), "svc_db", "bak_ok", into, "sealed-key", "owner")
		if err == nil || err.Error() != into+" already exists; restore goes into a new database." {
			t.Fatalf("into %s: %v", into, err)
		}
	}
}

func TestRestoreRefusesFailedBackup(t *testing.T) {
	a, st := restoreSeed(t)
	ctx := context.Background()
	if _, err := a.Restore(ctx, "svc_db", "bak_bad", "acme-db-2", "sealed-key", "owner"); err == nil || err.Error() != "backup bak_bad is failed, not ok; pick one from yawl db backups" {
		t.Fatalf("failed backup: %v", err)
	}
	st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_other", EnvironmentID: "env_1", Name: "other-db", Kind: "postgres", Version: 17, BackupAt: "03:00", CreatedAt: 1, UpdatedAt: 1})
	if _, err := a.Restore(ctx, "svc_other", "bak_ok", "acme-db-2", "sealed-key", "owner"); err == nil || !strings.Contains(err.Error(), "is not a backup of other-db") {
		t.Fatalf("another database's backup: %v", err)
	}
	st.Q.InsertBaseBackup(ctx, db.InsertBaseBackupParams{ID: "bak_base", ServiceID: "svc_db", ObjectKey: "k/base", CreatedAt: 1, UpdatedAt: 1})
	st.Q.FinishBackup(ctx, db.FinishBackupParams{State: "ok", UpdatedAt: 1, ID: "bak_base"})
	if _, err := a.Restore(ctx, "svc_db", "bak_base", "acme-db-2", "sealed-key", "owner"); err == nil || err.Error() != "bak_base is a base backup for restoring to a time; pick a dump from yawl db backups" {
		t.Fatalf("a base backup: %v", err)
	}
	if _, err := a.Restore(ctx, "svc_db", "bak_ok", "Bad_Name", "sealed-key", "owner"); err == nil {
		t.Fatal("a bad name was accepted")
	}
}

func TestRestoreQueuesNewDatabase(t *testing.T) {
	a, st := restoreSeed(t)
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET keep_json = '{"daily":14,"weekly":0,"monthly":6}' WHERE id = 'svc_db'`)
	id, err := a.Restore(ctx, "svc_db", "bak_ok", "acme-db-2", "sealed-key", "owner")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := st.Q.GetDeploy(ctx, id)
	svc, _ := st.Q.GetService(ctx, d.ServiceID)
	if svc.Name != "acme-db-2" || svc.Kind != "postgres" || svc.Version != 16 || svc.BackupAt != "04:30" || d.RestoreFrom != "bak_ok" || d.State != "queued" ||
		svc.KeepJson != `{"daily":14,"weekly":0,"monthly":6}` {
		t.Fatalf("service %+v deploy %+v", svc, d)
	}
	if k, ok := a.TakeRestoreKey(id); !ok || k != "sealed-key" {
		t.Fatalf("restore key %q %v", k, ok)
	}
	if _, ok := a.TakeRestoreKey(id); ok {
		t.Fatal("the restore key was handed out twice")
	}
}

func TestRestoreKeyNotStored(t *testing.T) {
	a, st := restoreSeed(t)
	if _, err := a.Restore(context.Background(), "svc_db", "bak_ok", "acme-db-2", "SEALED-RECOVERY-KEY-MARKER", "owner"); err != nil {
		t.Fatal(err)
	}
	tables, _ := st.DB.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	var names []string
	for tables.Next() {
		var n string
		tables.Scan(&n)
		names = append(names, n)
	}
	tables.Close()
	for _, n := range names {
		var hits int
		cols, _ := st.DB.Query(`SELECT name FROM pragma_table_info(?)`, n)
		var cs []string
		for cols.Next() {
			var c string
			cols.Scan(&c)
			cs = append(cs, c)
		}
		cols.Close()
		for _, c := range cs {
			st.DB.QueryRow(`SELECT COUNT(*) FROM "` + n + `" WHERE CAST("` + c + `" AS TEXT) LIKE '%SEALED-RECOVERY-KEY-MARKER%'`).Scan(&hits)
			if hits > 0 {
				t.Fatalf("%s.%s holds the recovery key box", n, c)
			}
		}
	}
}

func pitrSeed(t *testing.T) (*App, *store.Store) {
	a, st := restoreSeed(t)
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET pitr = 1 WHERE id = 'svc_db'`)
	ms := func(s string) int64 { v, _ := time.Parse("2006-01-02 15:04", s); return v.UnixMilli() }
	for _, b := range []struct{ id, at, done string }{{"base_a", "2026-10-20 03:00", "2026-10-20 03:05"}, {"base_b", "2026-10-21 03:00", "2026-10-21 03:05"}} {
		st.Q.InsertBaseBackup(ctx, db.InsertBaseBackupParams{ID: b.id, ServiceID: "svc_db", ObjectKey: "k/" + b.id, CreatedAt: ms(b.at), UpdatedAt: 1})
		st.Q.FinishBackup(ctx, db.FinishBackupParams{State: "ok", UpdatedAt: 1, ID: b.id, FinishedAt: sql.NullInt64{Int64: ms(b.done), Valid: true}})
	}
	for i, at := range []string{"2026-10-20 04:00", "2026-10-21 02:00", "2026-10-21 04:00", "2026-10-21 05:00", "2026-10-21 06:00"} {
		id := fmt.Sprintf("w%d", i+1)
		st.Q.InsertWALBatch(ctx, db.InsertWALBatchParams{ID: id, ServiceID: "svc_db", ObjectKey: "k/" + id, FirstWal: "a", LastWal: "b", Size: 1, CreatedAt: ms(at)})
	}
	return a, st
}

func TestRestoreAtWindow(t *testing.T) {
	a, _ := pitrSeed(t)
	for _, at := range []string{"2026-10-20 03:00", "2026-10-21 07:00"} {
		ts, _ := time.Parse("2006-01-02 15:04", at)
		_, err := a.RestoreAt(context.Background(), "svc_db", ts, "acme-db-2", "sealed-key", "owner")
		if err == nil || err.Error() != "Choose a time between 2026-10-20 03:05:00 UTC and 2026-10-21 05:57:00 UTC." {
			t.Fatalf("at %s: %v", at, err)
		}
	}
	_, st := pitrSeed(t)
	st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "pitr.shipped.svc_db", Value: fmt.Sprint(time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC).UnixMilli())})
	if _, err := New(st).RestoreAt(context.Background(), "svc_db", time.Date(2026, 10, 21, 7, 0, 0, 0, time.UTC), "acme-db-2", "k", "owner"); err != nil {
		t.Fatalf("a quiet hour: %v", err)
	}
	_, st = restoreSeed(t)
	if _, err := New(st).RestoreAt(context.Background(), "svc_db", time.Now(), "acme-db-2", "k", "owner"); err == nil || err.Error() != "acme-db has no base backup yet; turn on PITR and the first one is taken at its backup time." {
		t.Fatalf("no base: %v", err)
	}
}

func TestRestoreAtQueuesNewDatabase(t *testing.T) {
	a, st := pitrSeed(t)
	ctx := context.Background()
	for _, c := range []struct {
		at, base string
		wal      []string
	}{{"2026-10-21 04:30", "base_b", []string{"k/w3", "k/w4"}}, {"2026-10-21 02:30", "base_a", []string{"k/w1", "k/w2", "k/w3"}}} {
		at, _ := time.Parse("2006-01-02 15:04", c.at)
		into := "acme-db-" + strings.ReplaceAll(c.at[11:], ":", "")
		id, err := a.RestoreAt(ctx, "svc_db", at, into, "sealed-key", "owner")
		if err != nil {
			t.Fatal(err)
		}
		d, _ := st.Q.GetDeploy(ctx, id)
		if d.RestoreFrom != c.base || d.RestoreAt != at.UnixMilli() {
			t.Fatalf("%s: deploy %+v", c.at, d)
		}
		base, _ := st.Q.GetBackup(ctx, d.RestoreFrom)
		batches, _ := st.Q.ListWALBatches(ctx, "svc_db")
		if got, _, _ := WALFor(batches, base, at); !slices.Equal(got, c.wal) {
			t.Fatalf("%s: wal %v, want %v", c.at, got, c.wal)
		}
		if k, ok := a.TakeRestoreKey(id); !ok || k != "sealed-key" {
			t.Fatal("key not kept")
		}
	}
}

func TestRestoreAtRefusesGap(t *testing.T) {
	a, st := pitrSeed(t)
	seg := func(n int) string { return fmt.Sprintf("0000000100000000%08X", n) }
	for i, r := range [][2]int{{1, 2}, {3, 4}, {5, 6}, {9, 10}, {11, 11}} {
		st.DB.Exec(`UPDATE wal_batches SET first_wal = ?, last_wal = ? WHERE id = ?`, seg(r[0]), seg(r[1]), fmt.Sprintf("w%d", i+1))
	}
	at := time.Date(2026, 10, 21, 4, 30, 0, 0, time.UTC)
	_, err := a.RestoreAt(context.Background(), "svc_db", at, "acme-db-2", "k", "owner")
	if err == nil || err.Error() != "Changes after 2026-10-21 03:57:00 UTC were not all uploaded, so 2026-10-21 04:30:00 UTC cannot be reached. Choose an earlier time." {
		t.Fatalf("%v", err)
	}
	if _, err := a.RestoreAt(context.Background(), "svc_db", time.Date(2026, 10, 21, 3, 30, 0, 0, time.UTC), "acme-db-3", "k", "owner"); err != nil {
		t.Fatalf("before the hole: %v", err)
	}
	if got := nextSegment("0000000100000000000000FF"); got != "000000010000000100000000" {
		t.Fatalf("next of ...FF: %s", got)
	}
}

func TestRestoreFromCopy(t *testing.T) {
	a, st := restoreSeed(t)
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO storage_copies (name, endpoint, bucket, created_at, updated_at) VALUES ('eu', 'e', 'b', 1, 1), ('us', 'e', 'b', 1, 1)`)
	st.DB.Exec(`INSERT INTO backup_copies (backup_id, copy, state, cause, updated_at) VALUES ('bak_ok', 'eu', 'ok', '', 1), ('bak_ok', 'us', 'failed', 'x', 1)`)
	id, err := a.RestoreFrom(ctx, "svc_db", "bak_ok", "acme-db-2", "sealed-key", "eu", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := st.Q.GetDeploy(ctx, id); d.RestoreStorage != "eu" {
		t.Fatalf("%+v", d)
	}
	for copy, want := range map[string]string{"us": "bak_ok has no good copy in us", "mars": "bak_ok has no good copy in mars"} {
		if _, err := a.RestoreFrom(ctx, "svc_db", "bak_ok", "acme-db-3", "sealed-key", copy, "owner"); err == nil || err.Error() != want {
			t.Errorf("%s: %v", copy, err)
		}
	}
}

func TestRestoreVolumeRefusesDetached(t *testing.T) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, image, state, created_at, updated_at) VALUES ('dep_1', ?, 'c', 'img:1', 'live', 1, 1)`, svc)
	st.DB.Exec(`UPDATE services SET current_deploy_id = 'dep_1', volumes_json = '[{"name":"data","path":"/data"}]', volumes_kept = '["old"]' WHERE id = ?`, svc)
	for _, v := range []string{"data", "old"} {
		st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, volume, state, created_at, updated_at) VALUES (?, ?, ?, ?, 'ok', 1, 1)`, "bk_"+v, svc, "k/"+v, v)
	}
	if _, err := a.RestoreVolume(ctx, svc, "bk_old", "sealed", "owner"); err == nil || err.Error() != "api no longer has the volume old" {
		t.Fatalf("detached: %v", err)
	}
	if _, err := a.RestoreVolume(ctx, svc, "bk_data", "sealed", "owner"); err != nil {
		t.Fatal(err)
	}
}
