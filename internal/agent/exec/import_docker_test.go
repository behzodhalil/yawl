//go:build docker

package exec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestImportPostgresReal(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys, d.ImportDir, d.SnapDir = kr, t.TempDir(), t.TempDir()
	project := fmt.Sprintf("t%d", time.Now().UnixNano()%1_000_000)
	args, _, _ := ensureArgs(t, sealTo("pw-import-1"))
	args.Project = project
	container, volume, network, _ := dbNames("", project, "production", "acme-db", 17)
	t.Cleanup(func() {
		rt.Remove(ctx, container)
		rt.C.VolumeRemove(ctx, volume, client.VolumeRemoveOptions{Force: true})
		rt.C.NetworkRemove(ctx, network, client.NetworkRemoveOptions{})
	})
	if res := d.Handle(ctx, cmd(t, proto.CmdEnsureDatabase, args)); !res.OK {
		t.Fatalf("%+v", res)
	}
	snap := cmd(t, proto.CmdSnapshot, proto.SnapshotArgs{Project: project, Env: "production", Name: "acme-db", Version: 17, Password: sealTo("pw-import-1")})
	snap.Key = "dep_imp:snapshot:acme-db"
	if res := d.Handle(ctx, snap); !res.OK {
		t.Fatalf("snapshot %+v", res)
	}
	os.WriteFile(filepath.Join(d.ImportDir, "heroku.sql.gz"), gz("CREATE TABLE users (id int);\nINSERT INTO users VALUES (1), (2), (3);\n"), 0o600)
	res := d.Handle(ctx, cmd(t, proto.CmdImport, proto.ImportArgs{Project: project, Env: "production", Name: "acme-db", Version: 17,
		Password: sealTo("pw-import-1"), Path: "heroku.sql.gz"}))
	if !res.OK {
		t.Fatalf("import %+v", res)
	}
	var out strings.Builder
	exit, stderr, err := rt.RunOnce(ctx, runtime.ContainerSpec{Image: "postgres:17", Network: network, Env: []string{"PGPASSWORD=pw-import-1"},
		Cmd: []string{"psql", "-h", "acme-db", "-U", "app", "-d", "app", "-tAc", "select count(*) from users"}}, nil, &out)
	if err != nil || exit != 0 || strings.TrimSpace(out.String()) != "3" {
		t.Fatalf("count %q exit %d %s %v", out.String(), exit, stderr, err)
	}
	res = d.Handle(ctx, cmd(t, proto.CmdImport, proto.ImportArgs{Project: project, Env: "production", Name: "acme-db", Version: 17,
		Password: sealTo("pw-import-1"), Path: "heroku.sql.gz"}))
	if res.OK || !strings.Contains(res.Cause, `relation "users" already exists`) {
		t.Fatalf("%+v", res)
	}
}
