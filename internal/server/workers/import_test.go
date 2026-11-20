package workers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestRunImport(t *testing.T) {
	st, svc := seed(t, "postgres")
	ctx := context.Background()
	st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "v_pw", EnvironmentID: "env_1", ServiceID: svc, Key: "PASSWORD", AgentBox: "pw-box", RecoveryBox: "r"})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "recovery.pubkey", Value: "age1x"})
	file := filepath.Join(t.TempDir(), "imp_upload")
	os.WriteFile(file, []byte("CREATE TABLE t ();"), 0o600)
	depID, err := app.New(st).Import(ctx, svc, "upload", "heroku.sql", file, "owner")
	if err != nil {
		t.Fatal(err)
	}
	gw := &fakeSender{}
	r := NewRunner(st, gw, nil)
	r.PublicURL = "https://panel.example"
	if err := r.Run(ctx, depID); err != nil {
		t.Fatal(err)
	}
	var snap proto.SnapshotArgs
	var imp proto.ImportArgs
	for _, c := range gw.sent {
		switch c.Name {
		case proto.CmdSnapshot:
			json.Unmarshal(c.Args, &snap)
		case proto.CmdImport:
			json.Unmarshal(c.Args, &imp)
		}
	}
	d, _ := st.Q.GetDeploy(ctx, depID)
	if d.State != "live" || snap.Name != "api" || snap.Kind != "postgres" || imp.Password != "pw-box" ||
		!strings.HasPrefix(imp.URL, "https://panel.example/agent/imports/") || !strings.Contains(imp.URL, "?t=") {
		t.Fatalf("state %s snap %+v import %+v", d.State, snap, imp)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("the upload outlived its import")
	}
	for src, ref := range map[string]string{"path": "old.sql.gz", "url": "https://bucket.example/d.sql?X-Amz-Signature=s"} {
		id, err := app.New(st).Import(ctx, svc, src, ref, "", "owner")
		if err != nil {
			t.Fatal(err)
		}
		gw := &fakeSender{}
		r := NewRunner(st, gw, nil)
		r.Run(ctx, id)
		for _, c := range gw.sent {
			if c.Name == proto.CmdImport {
				json.Unmarshal(c.Args, &imp)
			}
		}
		if (src == "path" && imp.Path != ref) || (src == "url" && imp.URL != ref) {
			t.Fatalf("%s: %+v", src, imp)
		}
	}
}

func TestSweepUploads(t *testing.T) {
	st, svc := seed(t, "postgres")
	ctx := context.Background()
	dir := t.TempDir()
	ended, orphan, fresh := filepath.Join(dir, "upl_ended"), filepath.Join(dir, "upl_orphan"), filepath.Join(dir, "upl_fresh")
	for _, f := range []string{ended, orphan, fresh} {
		os.WriteFile(f, []byte("x"), 0o600)
	}
	old := time.Now().Add(-25 * time.Hour)
	os.Chtimes(orphan, old, old)
	id, err := app.New(st).Import(ctx, svc, "upload", "d.sql", ended, "owner")
	if err != nil {
		t.Fatal(err)
	}
	st.DB.Exec(`UPDATE deploys SET state = 'superseded' WHERE id = ?`, id)
	r := NewRunner(st, &fakeSender{}, nil)
	r.ImportDir = dir
	r.SweepUploads(ctx, time.Now())
	for f, want := range map[string]bool{ended: false, orphan: false, fresh: true} {
		if _, err := os.Stat(f); (err == nil) != want {
			t.Errorf("%s kept %v", filepath.Base(f), err == nil)
		}
	}
}
