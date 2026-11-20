//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dockerclient "github.com/moby/moby/client"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/server"
)

func dropPITR(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"ledger-db", "ledger-db-2"} {
		rt.Remove(ctx, "yawl-e2e-production-"+n)
		rt.C.VolumeRemove(ctx, "yawl-e2e-production-"+n+"-data", dockerclient.VolumeRemoveOptions{Force: true})
	}
}

func pgOut(t *testing.T, host, password, sql string) string {
	t.Helper()
	rt, _ := runtime.NewDocker()
	var out bytes.Buffer
	exit, stderr, err := rt.RunOnce(context.Background(), runtime.ContainerSpec{Image: "postgres:17", Network: "yawl-e2e-production",
		Env: []string{"PGPASSWORD=" + password}, Cmd: []string{"psql", "-h", host, "-U", "app", "-d", "app", "-tAc", sql}}, nil, &out)
	if err != nil || exit != 0 {
		t.Fatalf("psql %q: exit %d %s %v", sql, exit, stderr, err)
	}
	return strings.TrimSpace(out.String())
}

func TestE2EPITR(t *testing.T) {
	b := setupApp(t, "hello")
	dropPITR(t)
	t.Cleanup(func() { dropPITR(t) })
	out, err := run(t, "", "recovery-key", "init")
	if err != nil {
		t.Fatal(err)
	}
	key := ""
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "yawl-recovery-") {
			key = l
		}
	}
	s3(t)
	if err := server.Run(context.Background(), []string{"--data", filepath.Join(b.dir, "data"), "--etc", filepath.Join(b.dir, "etc"),
		"storage", "set", "http://127.0.0.1:19000", "backups", "yawltest", "yawltest-secret", "us-east-1"}, "dev"); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(b.repoDir, "yawl.yaml"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("  - name: ledger-db\n    kind: postgres\n    pitr: true\n    backup_at: \"" + time.Now().UTC().Format("15:04") + "\"\n")
	f.Close()
	if _, err := run(t, "", "apply", "--yes"); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, "", "deploy", "ledger-db"); err != nil {
		t.Fatalf("deploy ledger-db: %v\n%s", err, out)
	}
	pw, err := run(t, "", "env", "reveal", "ledger-db", "PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	pw = strings.TrimSpace(pw)
	window := func() string {
		out, _ := run(t, "", "db", "backups", "ledger-db")
		if i := strings.Index(out, "Restore to any second from "); i >= 0 {
			return out[i:]
		}
		return ""
	}
	waitFor(t, 4*time.Minute, "the first base backup", func() bool { return window() != "" })
	psql(t, "ledger-db", pw, "CREATE TABLE entries (note text); INSERT INTO entries VALUES ('t1')")
	time.Sleep(2 * time.Second)
	mid := time.Now().UTC().Truncate(time.Second)
	time.Sleep(2 * time.Second)
	psql(t, "ledger-db", pw, "INSERT INTO entries VALUES ('t2')")
	waitFor(t, 8*time.Minute, "the window to pass "+mid.Format(time.RFC3339), func() bool {
		f := strings.Fields(window())
		if len(f) < 8 {
			return false
		}
		to, err := time.Parse(time.RFC3339, strings.TrimSuffix(f[7], ":"))
		return err == nil && !to.Before(mid)
	})
	if out, err := run(t, key+"\n", "db", "restore", "ledger-db", "--at", mid.Format(time.RFC3339), "--into", "ledger-db-2"); err != nil {
		t.Fatalf("restore: %v\n%s", err, out)
	}
	pw2, err := run(t, "", "env", "reveal", "ledger-db-2", "PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	if pw2 = strings.TrimSpace(pw2); pw2 == pw {
		t.Fatal("the restored database has the source's password")
	}
	if got := pgOut(t, "ledger-db-2", pw2, "SELECT string_agg(note, ',' ORDER BY note) FROM entries"); got != "t1" {
		t.Fatalf("restored to %s: %q", mid, got)
	}
}
