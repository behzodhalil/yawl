package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func utc(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func backupWorld(t *testing.T, withStorage bool) (*Backups, *store.Store, *fakeSender, *clock) {
	st, _ := seed(t, "web")
	ctx := context.Background()
	dbID := addDB(t, st)
	st.DB.Exec(`UPDATE services SET created_at = ? WHERE id = ?`, utc("2026-10-20 00:00").UnixMilli(), dbID)
	for _, k := range []string{"PASSWORD", "URL"} {
		st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "v" + k, EnvironmentID: "env_1", ServiceID: dbID, Key: k, AgentBox: strings.ToLower(k) + "-box", RecoveryBox: "r"})
	}
	v := mustVault(t, st)
	if withStorage {
		st.Q.SetSetting(ctx, db.SetSettingParams{Key: "storage.endpoint", Value: "s3.example.com"})
		st.Q.SetSetting(ctx, db.SetSettingParams{Key: "storage.bucket", Value: "acme-backups"})
		vault.SetSecret(ctx, st.Q, v, "storage.access_key", []byte("AK"))
		vault.SetSecret(ctx, st.Q, v, "storage.secret_key", []byte("storage-secret"))
	}
	c := &clock{t: utc("2026-10-24 02:59")}
	gw := &fakeSender{}
	return &Backups{St: st, GW: gw, V: v, Now: c.now}, st, gw, c
}

func backupRows(t *testing.T, st *store.Store) []db.Backup {
	rows, err := st.Q.ListBackups(context.Background(), "svc_db")
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func outbox(t *testing.T, st *store.Store) []string {
	rows, _ := st.Q.ListOutbox(context.Background())
	var out []string
	for _, r := range rows {
		out = append(out, r.Body)
	}
	return out
}

func TestScheduleOncePerDay(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	ctx := context.Background()
	b.Tick(ctx)
	if n := len(backupRows(t, st)); n != 0 {
		t.Fatalf("02:59: %d backups", n)
	}
	c.t = utc("2026-10-24 03:00")
	b.Tick(ctx)
	c.t = utc("2026-10-24 03:30")
	b.Tick(ctx)
	if rows := backupRows(t, st); len(rows) != 1 || rows[0].State != "ok" || len(gw.sent) != 1 {
		t.Fatalf("after 03:00 and 03:30: %+v, sent %v", rows, gw.names())
	}
	c.t = utc("2026-10-25 05:10")
	b.Tick(ctx)
	b.Tick(ctx)
	if n := len(backupRows(t, st)); n != 2 {
		t.Fatalf("next day after downtime: %d backups, want 2", n)
	}
}

func TestBackupRunsAndRecords(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	c.t = utc("2026-10-24 03:00")
	b.Tick(context.Background())
	var a proto.BackupArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	if gw.sent[0].Name != proto.CmdBackup || a.Name != "acme-db" || a.Password != "password-box" || a.RecoveryPubkey != "cmVjb3Zlcnk=" ||
		a.Storage != (proto.Storage{Endpoint: "s3.example.com", Bucket: "acme-backups", AccessKey: "AK", SecretKey: "storage-secret"}) ||
		!strings.HasPrefix(a.Key, "yawl/acme/production/acme-db/20261024T030000Z-bak_") {
		t.Fatalf("args %+v", a)
	}
	r := backupRows(t, st)[0]
	if r.State != "ok" || r.Size != 1234 || r.Sha256 != "abc123" || !r.FinishedAt.Valid {
		t.Fatalf("row %+v", r)
	}
}

func TestBackupFailureRecorded(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	gw.fail = map[string]string{proto.CmdBackup: "upload to acme-backups: 403 key expired"}
	c.t = utc("2026-10-24 03:00")
	b.Tick(context.Background())
	if r := backupRows(t, st)[0]; r.State != "failed" || r.Cause != "upload to acme-backups: 403 key expired" {
		t.Fatalf("row %+v", r)
	}
	if got := outbox(t, st); len(got) == 0 || got[0] != "acme-db backup failed: upload to acme-backups: 403 key expired" {
		t.Fatalf("outbox %q", got)
	}
}

func TestBackupStuckRunningFails(t *testing.T) {
	b, st, _, _ := backupWorld(t, true)
	ctx := context.Background()
	st.Q.InsertBackup(ctx, db.InsertBackupParams{ID: "bak_old", ServiceID: "svc_db", ObjectKey: "k", CreatedAt: 1, UpdatedAt: 1})
	st.Q.StartBackup(ctx, db.StartBackupParams{StartedAt: sqlNull(utc("2026-10-23 23:00").UnixMilli()), UpdatedAt: 1, ID: "bak_old"})
	b.Tick(ctx)
	r, _ := st.Q.GetBackup(ctx, "bak_old")
	if r.State != "failed" || r.Cause != "the agent did not finish this backup (server restarted or agent lost)" {
		t.Fatalf("row %+v", r)
	}
}

func TestMissedOncePerGap(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	ctx := context.Background()
	gw.fail = map[string]string{proto.CmdBackup: "boom"}
	st.DB.Exec(`UPDATE services SET backup_at = '23:59' WHERE id = 'svc_db'`)
	c.t = utc("2026-10-21 02:01")
	b.Tick(ctx)
	b.Tick(ctx)
	msgs := outbox(t, st)
	if len(msgs) != 1 || msgs[0] != "acme-db: no backup has succeeded in 26 hours (last: never)." {
		t.Fatalf("outbox %q", msgs)
	}
	gw.fail = nil
	a := app.New(st)
	a.Now = c.now
	a.Backup(ctx, "svc_db", "owner")
	b.Tick(ctx)
	c.t = utc("2026-10-22 04:02")
	b.last = time.Time{}
	b.Tick(ctx)
	msgs = outbox(t, st)
	if len(msgs) != 2 || msgs[1] != "acme-db: no backup has succeeded in 26 hours (last: 2026-10-21 02:01 UTC)." {
		t.Fatalf("outbox %q", msgs)
	}
}

func TestMissedYoungDatabase(t *testing.T) {
	b, st, _, c := backupWorld(t, false)
	st.DB.Exec(`UPDATE services SET created_at = ?, backup_at = '23:59' WHERE id = 'svc_db'`, utc("2026-10-24 00:00").UnixMilli())
	c.t = utc("2026-10-25 01:59")
	b.Tick(context.Background())
	if got := outbox(t, st); len(got) != 0 {
		t.Fatalf("a database under 26 h old: %q", got)
	}
}

func TestMissedSaysStorageUnset(t *testing.T) {
	b, st, _, c := backupWorld(t, false)
	st.DB.Exec(`UPDATE services SET backup_at = '23:59' WHERE id = 'svc_db'`)
	c.t = utc("2026-10-21 02:01")
	b.Tick(context.Background())
	msgs := outbox(t, st)
	if len(msgs) != 1 || msgs[0] != "acme-db: no backup has succeeded in 26 hours (last: never). Backup storage is not set: run yawl server storage set." {
		t.Fatalf("outbox %q", msgs)
	}
}

func TestBackupWithoutStorageFails(t *testing.T) {
	b, st, gw, c := backupWorld(t, false)
	c.t = utc("2026-10-24 03:00")
	b.Tick(context.Background())
	if r := backupRows(t, st)[0]; r.State != "failed" || r.Cause != "backup storage is not set: run yawl server storage set" || len(gw.sent) != 0 {
		t.Fatalf("row %+v sent %v", r, gw.names())
	}
}

var vaults = map[*store.Store]*vault.Vault{}

func mustVault(t *testing.T, st *store.Store) *vault.Vault {
	if v, ok := vaults[st]; ok {
		return v
	}
	v, err := vault.LoadOrCreate(filepath.Join(t.TempDir(), "etc"))
	if err != nil {
		t.Fatal(err)
	}
	vaults[st] = v
	return v
}

func TestStorageEndpointScheme(t *testing.T) {
	b, st, _, _ := backupWorld(t, true)
	ctx := context.Background()
	for endpoint, want := range map[string]proto.Storage{
		"http://10.0.0.5:9000":     {Endpoint: "10.0.0.5:9000", Insecure: true},
		"https://s3.example.com/":  {Endpoint: "s3.example.com"},
		"s3.eu-central-003.b2.com": {Endpoint: "s3.eu-central-003.b2.com"},
	} {
		st.Q.SetSetting(ctx, db.SetSettingParams{Key: "storage.endpoint", Value: endpoint})
		got, err := b.storage(ctx)
		if err != nil || got.Endpoint != want.Endpoint || got.Insecure != want.Insecure {
			t.Errorf("%s: %+v %v", endpoint, got, err)
		}
	}
}

func seedOK(t *testing.T, st *store.Store, id, ts string) {
	ctx := context.Background()
	st.Q.InsertBackup(ctx, db.InsertBackupParams{ID: id, ServiceID: "svc_db", ObjectKey: "obj/" + id, CreatedAt: utc(ts).UnixMilli(), UpdatedAt: 1})
	st.Q.FinishBackup(ctx, db.FinishBackupParams{State: "ok", UpdatedAt: 1, ID: id})
}

func TestRetentionOnlyAfterRecordedSuccess(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	ctx := context.Background()
	seedOK(t, st, "bak_early", "2026-10-24 01:00")
	gw.fail = map[string]string{proto.CmdBackup: "boom"}
	c.t = utc("2026-10-24 03:00")
	b.Tick(ctx)
	for _, s := range gw.sent {
		var a struct{ Delete []string }
		json.Unmarshal(s.Args, &a)
		if s.Name == proto.CmdDeleteBackups || len(a.Delete) > 0 {
			t.Fatalf("a failed backup asked to delete: %s %s", s.Name, s.Args)
		}
	}
	gw.fail = nil
	c.t = utc("2026-10-24 03:10")
	a := app.New(st)
	a.Now = c.now
	a.Backup(ctx, "svc_db", "owner")
	b.Tick(ctx)
	last := gw.sent[len(gw.sent)-1]
	var del proto.DeleteBackupsArgs
	json.Unmarshal(last.Args, &del)
	if last.Name != proto.CmdDeleteBackups || len(del.Keys) != 1 || del.Keys[0] != "obj/bak_early" || del.Storage.Bucket != "acme-backups" {
		t.Fatalf("last command %s %+v", last.Name, del)
	}
	if r, _ := st.Q.GetBackup(ctx, "bak_early"); r.State != "deleted" {
		t.Fatalf("bak_early is %s", r.State)
	}
}

func TestVolumeMissed(t *testing.T) {
	b, st, _, c := backupWorld(t, true)
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET volumes_json = '[{"name":"uploads","path":"/u"}]', backup_at = '03:00', current_deploy_id = 'dep_x', created_at = ? WHERE id = 'svc_1'`, utc("2026-10-20 00:00").UnixMilli())
	b.GW = &fakeSender{fail: map[string]string{proto.CmdBackupVolume: "disk full"}}
	c.t = utc("2026-10-24 05:00")
	b.Tick(ctx)
	b.Tick(ctx)
	n := 0
	for _, m := range outbox(t, st) {
		if strings.Contains(m, "api volume uploads: no backup has succeeded in 26 hours") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d messages in %q", n, outbox(t, st))
	}
}

func TestVolumeMissedCloses(t *testing.T) {
	b, st, _, c := backupWorld(t, true)
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET volumes_json = '[{"name":"uploads","path":"/u"}]', backup_at = '03:00', current_deploy_id = 'dep_x', created_at = ? WHERE id = 'svc_1'`, utc("2026-10-20 00:00").UnixMilli())
	b.GW = &fakeSender{fail: map[string]string{proto.CmdBackupVolume: "disk full"}}
	c.t = utc("2026-10-24 05:00")
	b.Tick(ctx)
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "watch.url", Value: "https://watch.example"})
	b.GW = &fakeSender{}
	c.t = utc("2026-10-25 05:00")
	b.Tick(ctx)
	var closes int
	st.DB.QueryRow(`SELECT COUNT(*) FROM outbox WHERE incident = 'backup:svc_1:uploads' AND action = 'close'`).Scan(&closes)
	if closes != 1 {
		t.Fatalf("the missed incident was closed %d times after a good backup", closes)
	}
}

func TestRetentionByRule(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET keep_json = '{"daily":1,"weekly":0,"monthly":0}' WHERE id = 'svc_db'`)
	seedOK(t, st, "bak_yesterday", "2026-10-23 03:00")
	c.t = utc("2026-10-24 03:10")
	a := app.New(st)
	a.Now = c.now
	a.Backup(ctx, "svc_db", "owner")
	b.Tick(ctx)
	last := gw.sent[len(gw.sent)-1]
	var del proto.DeleteBackupsArgs
	json.Unmarshal(last.Args, &del)
	if last.Name != proto.CmdDeleteBackups || len(del.Keys) != 1 || del.Keys[0] != "obj/bak_yesterday" {
		t.Fatalf("last command %s %+v", last.Name, del)
	}
}

func TestMissedCountsFromBackupOn(t *testing.T) {
	b, st, _, c := backupWorld(t, false)
	st.DB.Exec(`UPDATE services SET backup_at = '23:59' WHERE id = 'svc_db'`)
	c.t = utc("2026-10-22 02:00")
	st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "backup.since.svc_db", Value: fmt.Sprint(utc("2026-10-22 01:00").UnixMilli())})
	b.Tick(context.Background())
	if got := outbox(t, st); len(got) != 0 {
		t.Fatalf("backup on for an hour: %q", got)
	}
	c.t = utc("2026-10-23 03:01")
	b.last = time.Time{}
	b.Tick(context.Background())
	if got := outbox(t, st); len(got) != 1 {
		t.Fatalf("26 h after: %q", got)
	}
}

func TestVolumeMissedNoStorage(t *testing.T) {
	b, st, _, c := backupWorld(t, false)
	st.DB.Exec(`UPDATE services SET volumes_json = '[{"name":"uploads","path":"/u"}]', backup_at = '03:00', current_deploy_id = 'dep_x', created_at = ? WHERE id = 'svc_1'`, utc("2026-10-20 00:00").UnixMilli())
	c.t = utc("2026-10-24 05:00")
	b.Tick(context.Background())
	for _, m := range outbox(t, st) {
		if strings.Contains(m, "api volume uploads: no backup") && strings.Contains(m, "Backup storage is not set") {
			return
		}
	}
	t.Fatalf("%q", outbox(t, st))
}

func TestScheduleFollowsBackupOn(t *testing.T) {
	for _, c := range []struct {
		kind   string
		backup int
		want   int
	}{{"postgres", 0, 1}, {"mariadb", 0, 1}, {"clickhouse", 1, 1}, {"clickhouse", 0, 0}, {"valkey", 0, 0}, {"valkey", 1, 1}} {
		b, st, _, clk := backupWorld(t, true)
		st.DB.Exec(`UPDATE services SET kind = ?, backup = ? WHERE id = 'svc_db'`, c.kind, c.backup)
		clk.t = utc("2026-10-24 03:00")
		b.Tick(context.Background())
		b.Wait()
		if n := len(backupRows(t, st)); n != c.want {
			t.Errorf("%s backup=%d: %d backups, want %d", c.kind, c.backup, n, c.want)
		}
	}
}

type agentAway struct {
	fakeSender
	up bool
}

func (a *agentAway) Connected(string) bool { return a.up }

func TestBackupWaitsForAgent(t *testing.T) {
	b, st, _, c := backupWorld(t, true)
	gw := &agentAway{}
	b.GW = gw
	c.t = utc("2026-10-24 03:00")
	b.Tick(context.Background())
	if rows := backupRows(t, st); len(rows) != 1 || rows[0].State != "queued" || len(gw.sent) != 0 {
		t.Fatalf("rows %+v sent %v", rows, gw.names())
	}
	gw.up = true
	b.Tick(context.Background())
	if rows := backupRows(t, st); len(rows) != 1 || rows[0].State != "ok" {
		t.Fatalf("rows %+v", rows)
	}
}

func TestLateBackupPastMidnight(t *testing.T) {
	b, st, _, c := backupWorld(t, true)
	st.DB.Exec(`UPDATE services SET backup_at = '23:55' WHERE id = 'svc_db'`)
	c.t = utc("2026-10-24 23:50")
	b.Tick(context.Background())
	c.t = utc("2026-10-25 00:10")
	b.Tick(context.Background())
	b.Tick(context.Background())
	if n := len(backupRows(t, st)); n != 1 {
		t.Fatalf("%d backups", n)
	}
}

type badAnswer struct{ fakeSender }

func (a *badAnswer) Send(ctx context.Context, id string, c proto.Command) (proto.Result, error) {
	if c.Name == proto.CmdBackup {
		return proto.Result{Key: c.Key, OK: true, Data: []byte("not json")}, nil
	}
	return a.fakeSender.Send(ctx, id, c)
}

func TestBackupUnreadableAnswerFails(t *testing.T) {
	b, st, _, c := backupWorld(t, true)
	b.GW = &badAnswer{}
	c.t = utc("2026-10-24 03:00")
	b.Tick(context.Background())
	if r := backupRows(t, st)[0]; r.State != "failed" || r.Cause != "the agent's answer could not be read" {
		t.Fatalf("row %+v", r)
	}
}
