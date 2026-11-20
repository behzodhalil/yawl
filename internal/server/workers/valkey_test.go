package workers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func addValkey(t *testing.T, st *store.Store, backup int64) string {
	ctx := context.Background()
	id, err := st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_cache", EnvironmentID: "env_1", Name: "cache", Kind: "valkey",
		Version: 8, BackupAt: "03:00", Backup: backup, CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "recovery.pubkey", Value: "cmVjb3Zlcnk="})
	return id
}

func TestEnsureSendsKind(t *testing.T) {
	st, _ := seed(t, "web")
	id := addValkey(t, st, 0)
	gw := &fakeSender{}
	dep, _ := deployOnce(t, st, id, gw)
	if d, _ := st.Q.GetDeploy(context.Background(), dep); d.State != "live" || len(gw.sent) != 1 || gw.sent[0].Name != proto.CmdEnsureDatabase {
		t.Fatalf("state %s, sent %v", d.State, gw.names())
	}
	var a proto.EnsureDatabaseArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	if a.Kind != "valkey" || a.Version != 8 || a.OutsidePort != 0 || a.PrivatePort != 0 || a.ShareHere || a.PrivateIP != "" {
		t.Fatalf("args %+v", a)
	}
	if url, err := app.DatabaseVar(context.Background(), st.Q, "env_1", id, "URL"); err != nil || url != "url-a" {
		t.Fatalf("URL box %q %v", url, err)
	}
}

func TestRefToValkey(t *testing.T) {
	st, svc := seed(t, "web")
	id := addValkey(t, st, 0)
	deployOnce(t, st, id, &fakeSender{})
	if _, err := app.FindRef(context.Background(), st.Q, "env_1", "REDIS_URL", "cache.url"); err != nil {
		t.Fatalf("FindRef: %v", err)
	}
	st.Q.UpsertEnvVar(context.Background(), db.UpsertEnvVarParams{ID: "v1", EnvironmentID: "env_1", ServiceID: svc, Key: "REDIS_URL", Ref: "cache.url"})
	st.DB.Exec(`UPDATE services SET release_json = '["migrate"]' WHERE id = ?`, svc)
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	for _, c := range gw.sent {
		if c.Name == proto.CmdSnapshot {
			t.Fatal("a snapshot of a Valkey before a release: pg_dump cannot read it")
		}
	}
}

func TestValkeyBackupSchedule(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	ctx := context.Background()
	on := addValkey(t, st, 1)
	for _, k := range []string{"PASSWORD", "URL"} {
		st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "c" + k, EnvironmentID: "env_1", ServiceID: on, Key: k, AgentBox: "x", RecoveryBox: "r"})
	}
	st.DB.Exec(`UPDATE services SET created_at = 1 WHERE id = ?`, on)
	c.t = c.t.Add(2 * 60e9)
	b.Tick(ctx)
	b.Tick(ctx)
	var sent *proto.BackupArgs
	for _, cmd := range gw.sent {
		var a proto.BackupArgs
		json.Unmarshal(cmd.Args, &a)
		if cmd.Name == proto.CmdBackup && a.Name == "cache" {
			sent = &a
		}
	}
	if sent == nil || sent.Kind != "valkey" {
		t.Fatalf("no valkey backup sent: %v", gw.names())
	}
	rows, _ := st.Q.ListBackups(ctx, on)
	if len(rows) != 1 || !strings.HasSuffix(rows[0].ObjectKey, ".tar.age") {
		t.Fatalf("rows %+v", rows)
	}

	st.DB.Exec(`UPDATE services SET backup = 0 WHERE id = ?`, on)
	st.DB.Exec(`DELETE FROM backups WHERE service_id = ?`, on)
	gw.sent = nil
	c.t = c.t.Add(30 * 3600e9)
	b.Tick(ctx)
	for _, cmd := range gw.sent {
		if strings.Contains(string(cmd.Args), `"cache"`) {
			t.Fatal("a Valkey without backup was backed up")
		}
	}
	for _, m := range outbox(t, st) {
		if strings.Contains(m, "cache") {
			t.Fatalf("missed backup alert for a Valkey without backup: %s", m)
		}
	}
}

func TestRestoreValkeyIntoNew(t *testing.T) {
	st, _ := seed(t, "web")
	ctx := context.Background()
	id := addValkey(t, st, 1)
	st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "cu", EnvironmentID: "env_1", ServiceID: id, Key: "URL", AgentBox: "u", RecoveryBox: "r"})
	st.Q.InsertBackup(ctx, db.InsertBackupParams{ID: "bak_v", ServiceID: id, ObjectKey: "yawl/acme/production/cache/x.tar.age", CreatedAt: 1, UpdatedAt: 1})
	st.Q.FinishBackup(ctx, db.FinishBackupParams{State: "ok", UpdatedAt: 1, ID: "bak_v"})
	a := app.New(st)
	dep, err := a.Restore(ctx, id, "bak_v", "cache2", "sealed", "owner")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := st.Q.GetDeploy(ctx, dep)
	svc, _ := st.Q.GetService(ctx, d.ServiceID)
	if svc.Kind != "valkey" || svc.Version != 8 || svc.Backup != 1 {
		t.Fatalf("new service %+v", svc)
	}
}

func TestRefUsersOfValkey(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	addValkey(t, st, 0)
	st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "v1", EnvironmentID: "env_1", ServiceID: svc, Key: "REDIS_URL", Ref: "cache.url"})
	st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "v2", EnvironmentID: "env_1", ServiceID: "", Key: "CACHE", Ref: "cache.url"})
	us, err := st.Q.RefUsers(ctx, db.RefUsersParams{EnvironmentID: "env_1", Ref: "cache.%"})
	if err != nil || len(us) != 1 || us[0] != "api" {
		t.Fatalf("%v %v", us, err)
	}
}

func TestCrossEnvRefToValkeyRefused(t *testing.T) {
	st, _ := seed(t, "web")
	ctx := context.Background()
	addValkey(t, st, 0)
	st.Q.UpsertEnvironment(ctx, db.UpsertEnvironmentParams{ID: "env_2", ProjectID: "prj_1", Name: "staging", ServerID: "srv_1", CreatedAt: 1, UpdatedAt: 1})
	_, err := app.FindRef(ctx, st.Q, "env_2", "REDIS_URL", "production/cache.url")
	if err == nil || !strings.Contains(err.Error(), "only Postgres is reached from another environment") {
		t.Fatalf("%v", err)
	}
}

type deadlines struct {
	fakeSender
	got map[string]time.Duration
}

func (d *deadlines) Send(ctx context.Context, server string, c proto.Command) (proto.Result, error) {
	if dl, ok := ctx.Deadline(); ok {
		d.got[c.Name] = time.Until(dl)
	}
	return d.fakeSender.Send(ctx, server, c)
}

func TestEnsureHasTimeToPull(t *testing.T) {
	st, _ := seed(t, "web")
	id := addValkey(t, st, 0)
	gw := &deadlines{got: map[string]time.Duration{}}
	deployOnce(t, st, id, gw)
	if got := gw.got[proto.CmdEnsureDatabase]; got < 17*time.Minute {
		t.Fatalf("ensure has %v", got)
	}
}
