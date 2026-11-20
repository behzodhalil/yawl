package workers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestMySQLBackupKey(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	ctx := context.Background()
	id, _ := st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_my", EnvironmentID: "env_1", Name: "shop-db", Kind: "mysql",
		Version: 8, BackupAt: "03:00", Backup: 0, CreatedAt: 1, UpdatedAt: 1})
	for _, k := range []string{"PASSWORD", "URL"} {
		st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "m" + k, EnvironmentID: "env_1", ServiceID: id, Key: k, AgentBox: "x", RecoveryBox: "r"})
	}
	c.t = c.t.Add(2 * 60e9)
	b.Tick(ctx)
	b.Tick(ctx)
	rows, _ := st.Q.ListBackups(ctx, id)
	if len(rows) != 1 || !strings.HasSuffix(rows[0].ObjectKey, ".sql.age") {
		t.Fatalf("rows %+v", rows)
	}
	found := false
	for _, cmd := range gw.sent {
		var a proto.BackupArgs
		json.Unmarshal(cmd.Args, &a)
		found = found || (cmd.Name == proto.CmdBackup && a.Kind == "mysql")
	}
	if !found {
		t.Fatalf("no mysql backup sent: %v", gw.names())
	}
}

func TestMongoBackupKey(t *testing.T) {
	b, st, _, c := backupWorld(t, true)
	ctx := context.Background()
	id, _ := st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_mg", EnvironmentID: "env_1", Name: "docs", Kind: "mongodb",
		Version: 8, BackupAt: "03:00", CreatedAt: 1, UpdatedAt: 1})
	for _, k := range []string{"PASSWORD", "URL"} {
		st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "g" + k, EnvironmentID: "env_1", ServiceID: id, Key: k, AgentBox: "x", RecoveryBox: "r"})
	}
	c.t = c.t.Add(2 * 60e9)
	b.Tick(ctx)
	rows, _ := st.Q.ListBackups(ctx, id)
	if len(rows) != 1 || !strings.HasSuffix(rows[0].ObjectKey, ".archive.age") {
		t.Fatalf("rows %+v", rows)
	}
}
