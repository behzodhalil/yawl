package workers

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

func TestBackupCopies(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO storage_copies (name, endpoint, bucket, region, created_at, updated_at) VALUES ('eu', 'eu.s3.example', 'eu-bucket', 'eu-1', 1, 1)`)
	vault.SetSecret(ctx, st.Q, b.V, "storage.copy.eu.access_key", []byte("EU-AK"))
	vault.SetSecret(ctx, st.Q, b.V, "storage.copy.eu.secret_key", []byte("EU-SK"))
	seedOK(t, st, "bak_early", "2026-10-24 01:00")
	c.t = utc("2026-10-24 03:10")
	a := app.New(st)
	a.Now = c.now
	id, _ := a.Backup(ctx, "svc_db", "owner")
	b.Tick(ctx)
	b.Wait()
	var cp proto.CopyBackupArgs
	var delCopy bool
	for _, s := range gw.sent {
		switch s.Name {
		case proto.CmdCopyBackup:
			json.Unmarshal(s.Args, &cp)
		case proto.CmdDeleteBackups:
			var d proto.DeleteBackupsArgs
			json.Unmarshal(s.Args, &d)
			delCopy = delCopy || (d.Storage.Bucket == "eu-bucket" && len(d.Keys) == 1 && d.Keys[0] == "obj/bak_early")
		}
	}
	if cp.To.Bucket != "eu-bucket" || cp.To.SecretKey != "EU-SK" || cp.From.Bucket != "acme-backups" || cp.Key == "" {
		t.Fatalf("%+v", cp)
	}
	if !delCopy {
		t.Fatal("retention left the copy's old object")
	}
	var state string
	st.DB.QueryRow(`SELECT state FROM backup_copies WHERE backup_id = ? AND copy = 'eu'`, id).Scan(&state)
	if state != "ok" {
		t.Fatalf("copy state %q", state)
	}
	gw.fail = map[string]string{proto.CmdCopyBackup: "eu.s3.example: no such host"}
	c.t = utc("2026-10-25 03:10")
	id2, _ := a.Backup(ctx, "svc_db", "owner")
	b.Tick(ctx)
	b.Wait()
	if r, _ := st.Q.GetBackup(ctx, id2); r.State != "ok" {
		t.Fatalf("backup %s", r.State)
	}
	var cause string
	st.DB.QueryRow(`SELECT state || ': ' || cause FROM backup_copies WHERE backup_id = ? AND copy = 'eu'`, id2).Scan(&cause)
	if cause != "failed: eu.s3.example: no such host" {
		t.Fatalf("%q", cause)
	}
	if got := outbox(t, st); !strings.Contains(strings.Join(got, "\n"), "acme-db backup copy to eu failed: eu.s3.example: no such host") {
		t.Fatalf("%q", got)
	}
}

func TestRunnerRestoreFromCopy(t *testing.T) {
	st, _ := restoreDeploy(t)
	ctx := context.Background()
	v := mustVault(t, st)
	st.DB.Exec(`INSERT INTO storage_copies (name, endpoint, bucket, region, created_at, updated_at) VALUES ('eu', 'https://eu.s3.example', 'eu-bucket', '', 1, 1)`)
	vault.SetSecret(ctx, st.Q, v, "storage.copy.eu.access_key", []byte("EU-AK"))
	vault.SetSecret(ctx, st.Q, v, "storage.copy.eu.secret_key", []byte("EU-SK"))
	st.DB.Exec(`INSERT INTO backup_copies (backup_id, copy, state, updated_at) VALUES ('bak_1', 'eu', 'ok', 1)`)
	id, err := app.New(st).RestoreFrom(ctx, "svc_db", "bak_1", "acme-db-3", "unused", "eu", "owner")
	if err != nil {
		t.Fatal(err)
	}
	gw := &fakeSender{}
	r := NewRunner(st, gw, nil)
	r.RestoreKeys = keys{id: "sealed-recovery"}
	r.V = v
	r.Run(ctx, id)
	for _, c := range gw.sent {
		if c.Name == proto.CmdRestore {
			var a proto.RestoreArgs
			json.Unmarshal(c.Args, &a)
			if a.Storage.Bucket != "eu-bucket" || a.Storage.Endpoint != "eu.s3.example" || a.Storage.SecretKey != "EU-SK" {
				t.Fatalf("%+v", a.Storage)
			}
			return
		}
	}
	t.Fatalf("no restore sent: %v", gw.names())
}

type copyFails struct {
	fakeSender
	fail string
}

func (c *copyFails) Send(ctx context.Context, id string, cmd proto.Command) (proto.Result, error) {
	if cmd.Name == proto.CmdCopyBackup && strings.Contains(string(cmd.Args), c.fail) {
		return proto.Result{Key: cmd.Key, Cause: "no such host"}, nil
	}
	return c.fakeSender.Send(ctx, id, cmd)
}

func TestCopyIncidentPerVolume(t *testing.T) {
	b, st, _, _ := backupWorld(t, true)
	ctx := context.Background()
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "watch.url", Value: "https://watch.example"})
	st.DB.Exec(`INSERT INTO storage_copies (name, endpoint, bucket, created_at, updated_at) VALUES ('eu', 'eu.s3.example', 'eu-bucket', 1, 1)`)
	vault.SetSecret(ctx, st.Q, b.V, "storage.copy.eu.access_key", []byte("AK"))
	vault.SetSecret(ctx, st.Q, b.V, "storage.copy.eu.secret_key", []byte("SK"))
	b.GW = &copyFails{fail: "vol-a"}
	svc, _ := st.Q.GetService(ctx, "svc_1")
	b.copyOut(ctx, svc, db.Backup{ID: "bk_a", ServiceID: "svc_1", Volume: "a", ObjectKey: "vol-a.tar.age"}, proto.Storage{Bucket: "main"})
	b.copyOut(ctx, svc, db.Backup{ID: "bk_b", ServiceID: "svc_1", Volume: "b", ObjectKey: "vol-b.tar.age"}, proto.Storage{Bucket: "main"})
	rows, _ := st.DB.Query(`SELECT incident, action FROM outbox WHERE incident LIKE 'backupcopy:%' ORDER BY created_at, rowid`)
	defer rows.Close()
	state := map[string]string{}
	for rows.Next() {
		var k, a string
		rows.Scan(&k, &a)
		state[k] = a
	}
	open := 0
	for _, a := range state {
		if a == "open" {
			open++
		}
	}
	if len(state) != 2 || open != 1 {
		t.Fatalf("%v", state)
	}
}

type deleteFails struct {
	fakeSender
	bucket string
}

func (d *deleteFails) Send(ctx context.Context, id string, cmd proto.Command) (proto.Result, error) {
	if cmd.Name == proto.CmdDeleteBackups && d.bucket != "" && strings.Contains(string(cmd.Args), d.bucket) {
		return proto.Result{Key: cmd.Key, Cause: "access denied"}, nil
	}
	return d.fakeSender.Send(ctx, id, cmd)
}

func withCopy(t *testing.T, b *Backups) {
	ctx := context.Background()
	b.St.DB.Exec(`INSERT INTO storage_copies (name, endpoint, bucket, region, created_at, updated_at) VALUES ('eu', 'eu.s3.example', 'eu-bucket', '', 1, 1)`)
	vault.SetSecret(ctx, b.St.Q, b.V, "storage.copy.eu.access_key", []byte("EU-AK"))
	vault.SetSecret(ctx, b.St.Q, b.V, "storage.copy.eu.secret_key", []byte("EU-SK"))
}

func TestCopyDeleteRetried(t *testing.T) {
	b, st, _, c := backupWorld(t, true)
	ctx := context.Background()
	withCopy(t, b)
	gw := &deleteFails{bucket: "eu-bucket"}
	b.GW = gw
	seedOK(t, st, "bak_early", "2026-10-24 01:00")
	st.DB.Exec(`INSERT INTO backup_copies (backup_id, copy, state, updated_at) VALUES ('bak_early', 'eu', 'ok', 1)`)
	a := app.New(st)
	a.Now = c.now
	c.t = utc("2026-10-24 03:10")
	a.Backup(ctx, "svc_db", "owner")
	b.Tick(ctx)
	b.Wait()
	rows := func() (n int) {
		st.DB.QueryRow(`SELECT COUNT(*) FROM backup_copies WHERE backup_id = 'bak_early'`).Scan(&n)
		return
	}
	if r, _ := st.Q.GetBackup(ctx, "bak_early"); r.State != "deleted" || rows() != 1 {
		t.Fatalf("main %s, copy rows %d: the copy's object is no longer tracked", r.State, rows())
	}
	gw.bucket = ""
	c.t = utc("2026-10-25 03:10")
	a.Backup(ctx, "svc_db", "owner")
	b.Tick(ctx)
	b.Wait()
	again := false
	for _, s := range gw.sent {
		var d proto.DeleteBackupsArgs
		json.Unmarshal(s.Args, &d)
		again = again || (s.Name == proto.CmdDeleteBackups && d.Storage.Bucket == "eu-bucket" && strings.Contains(strings.Join(d.Keys, ","), "obj/bak_early"))
	}
	if !again || rows() != 0 {
		t.Fatalf("tried again %v, rows %d", again, rows())
	}
}

func TestCopyMissingKeysSaid(t *testing.T) {
	b, st, _, _ := backupWorld(t, true)
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO storage_copies (name, endpoint, bucket, region, created_at, updated_at) VALUES ('eu', 'eu.s3.example', 'eu-bucket', '', 1, 1)`)
	svc, _ := st.Q.GetService(ctx, "svc_db")
	b.copyOut(ctx, svc, db.Backup{ID: "bak_x", ServiceID: "svc_db", ObjectKey: "obj/bak_x"}, proto.Storage{Bucket: "main"})
	var state, cause string
	st.DB.QueryRow(`SELECT state, cause FROM backup_copies WHERE backup_id = 'bak_x' AND copy = 'eu'`).Scan(&state, &cause)
	if state != "failed" || !strings.Contains(cause, "keys") {
		t.Fatalf("%q %q", state, cause)
	}
	if got := outbox(t, st); !strings.Contains(strings.Join(got, "\n"), "acme-db backup copy to eu failed") {
		t.Fatalf("%q", got)
	}
}

func TestCopyRetriedAfterRestart(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	ctx := context.Background()
	withCopy(t, b)
	seedOK(t, st, "bak_old", "2026-10-23 03:00")
	seedOK(t, st, "bak_cut", "2026-10-24 01:00")
	c.t = utc("2026-10-24 02:00")
	b.Tick(ctx)
	b.Wait()
	b.Tick(ctx)
	b.Wait()
	var keys []string
	for _, s := range gw.sent {
		if s.Name == proto.CmdCopyBackup {
			var a proto.CopyBackupArgs
			json.Unmarshal(s.Args, &a)
			keys = append(keys, a.Key)
		}
	}
	var state string
	st.DB.QueryRow(`SELECT state FROM backup_copies WHERE backup_id = 'bak_cut' AND copy = 'eu'`).Scan(&state)
	if strings.Join(keys, ",") != "obj/bak_cut" || state != "ok" {
		t.Fatalf("copied %v, state %q", keys, state)
	}
}

func TestShortKeepDeletesFromCopies(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET keep_json = '{"daily":1,"weekly":0,"monthly":0}' WHERE id = 'svc_db'`)
	st.DB.Exec(`INSERT INTO storage_copies (name, endpoint, bucket, region, created_at, updated_at) VALUES ('eu', 'eu.s3.example', 'eu-bucket', 'eu-1', 1, 1)`)
	vault.SetSecret(ctx, st.Q, b.V, "storage.copy.eu.access_key", []byte("EU-AK"))
	vault.SetSecret(ctx, st.Q, b.V, "storage.copy.eu.secret_key", []byte("EU-SK"))
	seedOK(t, st, "bak_yesterday", "2026-10-23 03:00")
	c.t = utc("2026-10-24 03:10")
	a := app.New(st)
	a.Now = c.now
	a.Backup(ctx, "svc_db", "dev@acme.uz")
	b.Tick(ctx)
	b.Wait()
	buckets := map[string]bool{}
	for _, s := range gw.sent {
		var d proto.DeleteBackupsArgs
		if s.Name == proto.CmdDeleteBackups && json.Unmarshal(s.Args, &d) == nil && slices.Contains(d.Keys, "obj/bak_yesterday") {
			buckets[d.Storage.Bucket] = true
		}
	}
	if !buckets["acme-backups"] || !buckets["eu-bucket"] {
		t.Fatalf("deleted from %v", buckets)
	}
}
