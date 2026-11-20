package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestEnsureSendsPITR(t *testing.T) {
	st, _ := seed(t, "web")
	dbID := addDB(t, st)
	st.DB.Exec(`UPDATE services SET pitr = 1 WHERE id = ?`, dbID)
	gw := &fakeSender{}
	deployOnce(t, st, dbID, gw)
	var a proto.EnsureDatabaseArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	if !a.PITR {
		t.Fatalf("args %+v", a)
	}
}

func TestPITRWorkerRecordsBatches(t *testing.T) {
	b, st, _, c := backupWorld(t, true)
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET pitr = 1 WHERE id = 'svc_db'`)
	gw := &walSender{}
	w := &PITR{St: st, GW: gw, V: b.V, Now: c.now}
	if err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gw.sent) != 1 || gw.sent[0].Name != proto.CmdShipWAL {
		t.Fatalf("sent %v", gw.sent)
	}
	var a proto.ShipWALArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	if a.KeyPrefix != "yawl/acme/production/acme-db/wal/" || a.Storage.Bucket != "acme-backups" {
		t.Fatalf("args %+v", a)
	}
	rows, _ := st.Q.ListWALBatches(ctx, "svc_db")
	if len(rows) != 1 || rows[0].FirstWal != "000000010000000000000003" || rows[0].Sha256 != "sum-k" {
		t.Fatalf("rows %+v", rows)
	}
	if v, _ := st.Q.GetSetting(ctx, "pitr.shipped.svc_db"); v != fmt.Sprint(c.now().UnixMilli()) {
		t.Fatalf("shipped %q", v)
	}
	w.Tick(ctx)
	var a2 proto.ShipWALArgs
	json.Unmarshal(gw.sent[1].Args, &a2)
	if !slices.Equal(a2.Remove, []string{"000000010000000000000003"}) {
		t.Fatalf("remove %v", a2.Remove)
	}
	if rows, _ := st.Q.ListWALBatches(ctx, "svc_db"); rows[0].Files != "" || rows[1].Files != `["000000010000000000000003"]` {
		t.Fatalf("files %q %q", rows[0].Files, rows[1].Files)
	}
	gw.pending, gw.fail = 3<<30, true
	w.Tick(ctx)
	w.Tick(ctx)
	n := 0
	for _, m := range outbox(t, st) {
		if strings.Contains(m, "acme-db") && strings.Contains(m, "WAL") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("lag messages %d", n)
	}
}

type walSender struct {
	sent    []proto.Command
	pending int64
	fail    bool
}

func (s *walSender) Send(_ context.Context, _ string, c proto.Command) (proto.Result, error) {
	s.sent = append(s.sent, c)
	if s.fail {
		b, _ := json.Marshal(proto.ShipWALData{Pending: s.pending})
		return proto.Result{Cause: "upload to backups: 503", Data: b}, nil
	}
	b, _ := json.Marshal(proto.ShipWALData{Pending: s.pending, Batch: &proto.WALBatch{Key: "k", First: "000000010000000000000003", Last: "000000010000000000000003", Size: 9, SHA256: "sum-k",
		Files: []string{"000000010000000000000003"}}})
	return proto.Result{OK: true, Data: b}, nil
}

func TestPITRNightlyTakesBase(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET pitr = 1 WHERE id = 'svc_db'`)
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "recovery.pubkey", Value: "age1x"})
	c.t = utc("2026-10-24 03:00")
	if err := b.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var keys []string
	bases := 0
	for _, s := range gw.sent {
		if s.Name == proto.CmdBackup {
			var a proto.BackupArgs
			json.Unmarshal(s.Args, &a)
			keys = append(keys, a.Key)
			if a.Base {
				bases++
				if !strings.HasPrefix(a.Key, "yawl/acme/production/acme-db/base/") || !strings.HasSuffix(a.Key, ".tar.age") {
					t.Fatalf("base key %s", a.Key)
				}
			}
		}
	}
	if len(keys) != 2 || bases != 1 {
		t.Fatalf("backups %v", keys)
	}
	c.t = utc("2026-10-24 03:30")
	b.Tick(ctx)
	if n := len(backupRows(t, st)); n != 2 {
		t.Fatalf("rows %d after a second tick", n)
	}
	if l, err := st.Q.LatestOKBackup(ctx, "svc_db"); err != nil || l.Kind != "dump" {
		t.Fatalf("latest %+v %v", l, err)
	}
}

func TestPITRRetention(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET pitr = 1 WHERE id = 'svc_db'`)
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "recovery.pubkey", Value: "age1x"})
	for d := 16; d <= 23; d++ {
		id := fmt.Sprintf("base_%d", d)
		st.Q.InsertBaseBackup(ctx, db.InsertBaseBackupParams{ID: id, ServiceID: "svc_db", ObjectKey: "obj/" + id, CreatedAt: utc(fmt.Sprintf("2026-10-%d 03:00", d)).UnixMilli(), UpdatedAt: 1})
		st.Q.FinishBackup(ctx, db.FinishBackupParams{State: "ok", UpdatedAt: 1, ID: id})
	}
	seedOK(t, st, "bak_23", "2026-10-23 03:00")
	for _, w := range []struct{ id, at string }{{"wal_old", "2026-10-17 12:00"}, {"wal_new", "2026-10-18 12:00"}} {
		st.Q.InsertWALBatch(ctx, db.InsertWALBatchParams{ID: w.id, ServiceID: "svc_db", ObjectKey: "obj/" + w.id, FirstWal: "a", LastWal: "b", Size: 1, CreatedAt: utc(w.at).UnixMilli()})
	}
	c.t = utc("2026-10-24 03:00")
	if err := b.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var deleted []string
	for _, s := range gw.sent {
		if s.Name == proto.CmdDeleteBackups {
			var a proto.DeleteBackupsArgs
			json.Unmarshal(s.Args, &a)
			deleted = append(deleted, a.Keys...)
		}
	}
	slices.Sort(deleted)
	if want := []string{"obj/base_16", "obj/base_17", "obj/wal_old"}; !slices.Equal(deleted, want) {
		t.Fatalf("deleted %v, want %v", deleted, want)
	}
	if r, _ := st.Q.GetBackup(ctx, "base_17"); r.State != "deleted" {
		t.Fatalf("base_17 %s", r.State)
	}
	if r, _ := st.Q.GetBackup(ctx, "bak_23"); r.State != "ok" {
		t.Fatalf("the dump bak_23 %s", r.State)
	}
	rows, _ := st.Q.ListWALBatches(ctx, "svc_db")
	if len(rows) != 1 || rows[0].ID != "wal_new" {
		t.Fatalf("batches %+v", rows)
	}
}

func TestBuildSendsBuilder(t *testing.T) {
	st, svc := seed(t, "web")
	st.DB.Exec(`UPDATE services SET builder = 'railpack' WHERE id = ?`, svc)
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	var a proto.BuildArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	if gw.sent[0].Name != proto.CmdBuild || a.Builder != "railpack" {
		t.Fatalf("%s %+v", gw.sent[0].Name, a)
	}
}

func TestPITRRetentionWaitsForRestore(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET pitr = 1 WHERE id = 'svc_db'`)
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "recovery.pubkey", Value: "age1x"})
	for d := 16; d <= 23; d++ {
		id := fmt.Sprintf("base_%d", d)
		st.Q.InsertBaseBackup(ctx, db.InsertBaseBackupParams{ID: id, ServiceID: "svc_db", ObjectKey: "obj/" + id, CreatedAt: utc(fmt.Sprintf("2026-10-%d 03:00", d)).UnixMilli(), UpdatedAt: 1})
		st.Q.FinishBackup(ctx, db.FinishBackupParams{State: "ok", UpdatedAt: 1, ID: id})
	}
	seedOK(t, st, "bak_23", "2026-10-23 03:00")
	if _, err := st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, restore_from, created_at, updated_at) VALUES ('dep_r', 'svc_db', '', 'restoring', 'base_16', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	c.t = utc("2026-10-24 03:00")
	b.Tick(ctx)
	if slices.Contains(gw.names(), proto.CmdDeleteBackups) {
		t.Fatal("retention ran during a restore")
	}
	st.DB.Exec(`UPDATE deploys SET state = 'live' WHERE id = 'dep_r'`)
	c.t = utc("2026-10-25 03:00")
	b.Tick(ctx)
	if !slices.Contains(gw.names(), proto.CmdDeleteBackups) {
		t.Fatal("retention did not run after the restore")
	}
}

func TestPITROneBasePerDay(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET pitr = 1 WHERE id = 'svc_db'`)
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "recovery.pubkey", Value: "age1x"})
	for d := 17; d <= 23; d++ {
		id := fmt.Sprintf("base_%d", d)
		st.Q.InsertBaseBackup(ctx, db.InsertBaseBackupParams{ID: id, ServiceID: "svc_db", ObjectKey: "obj/" + id, CreatedAt: utc(fmt.Sprintf("2026-10-%d 03:00", d)).UnixMilli(), UpdatedAt: 1})
		st.Q.FinishBackup(ctx, db.FinishBackupParams{State: "ok", UpdatedAt: 1, ID: id})
		if d >= 18 {
			w := fmt.Sprintf("wal_%d", d)
			st.Q.InsertWALBatch(ctx, db.InsertWALBatchParams{ID: w, ServiceID: "svc_db", ObjectKey: "obj/" + w, FirstWal: "a", LastWal: "b", Size: 1, CreatedAt: utc(fmt.Sprintf("2026-10-%d 12:00", d)).UnixMilli()})
		}
	}
	seedOK(t, st, "bak_23", "2026-10-23 03:00")
	bases := 0
	for m := 0; m < 7; m++ {
		st.DB.Exec(`UPDATE services SET backup_at = ? WHERE id = 'svc_db'`, fmt.Sprintf("10:%02d", m))
		c.t = utc(fmt.Sprintf("2026-10-24 10:%02d", m)).Add(30 * time.Second)
		if err := b.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	deleted := map[string]bool{}
	for _, s := range gw.sent {
		switch s.Name {
		case proto.CmdBackup:
			var a proto.BackupArgs
			if json.Unmarshal(s.Args, &a) == nil && a.Base {
				bases++
			}
		case proto.CmdDeleteBackups:
			var a proto.DeleteBackupsArgs
			json.Unmarshal(s.Args, &a)
			for _, k := range a.Keys {
				deleted[k] = true
			}
		}
	}
	if bases != 1 {
		t.Fatalf("%d bases in one day", bases)
	}
	for k := range deleted {
		if strings.HasPrefix(k, "obj/") && k != "obj/base_17" {
			t.Fatalf("deleted %s", k)
		}
	}
	if !deleted["obj/base_17"] {
		t.Fatalf("deleted %v", deleted)
	}
	for d := 18; d <= 23; d++ {
		if r, _ := st.Q.GetBackup(ctx, fmt.Sprintf("base_%d", d)); r.State != "ok" {
			t.Fatalf("base_%d %s", d, r.State)
		}
	}
	if rows, _ := st.Q.ListWALBatches(ctx, "svc_db"); len(rows) != 6 {
		t.Fatalf("batches %+v", rows)
	}
	st.DB.Exec(`UPDATE backups SET state = 'failed' WHERE kind = 'base' AND created_at >= ?`, utc("2026-10-24 00:00").UnixMilli())
	st.DB.Exec(`UPDATE services SET backup_at = '10:07' WHERE id = 'svc_db'`)
	c.t = utc("2026-10-24 10:07").Add(30 * time.Second)
	b.Tick(ctx)
	var today int
	st.DB.QueryRow(`SELECT COUNT(*) FROM backups WHERE kind = 'base' AND state <> 'failed' AND created_at >= ?`, utc("2026-10-24 00:00").UnixMilli()).Scan(&today)
	if today != 1 {
		t.Fatalf("%d bases after a failed one", today)
	}
}
