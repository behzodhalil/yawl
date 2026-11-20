//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockerclient "github.com/moby/moby/client"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/server"
)

func dropMySQLs(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"shop-db", "shop-db-2"} {
		rt.Remove(ctx, "yawl-e2e-production-"+n)
		rt.C.VolumeRemove(ctx, "yawl-e2e-production-"+n+"-data", dockerclient.VolumeRemoveOptions{Force: true})
	}
}

func mysqlRun(t *testing.T, host, password, sql string) string {
	t.Helper()
	rt, _ := runtime.NewDocker()
	var out bytes.Buffer
	exit, stderr, err := rt.RunOnce(context.Background(), runtime.ContainerSpec{Image: "mysql:8.4", Network: "yawl-e2e-production",
		Env: []string{"MYSQL_PWD=" + password}, Cmd: []string{"mysql", "-h", host, "-u", "app", "app", "-N", "-e", sql}}, nil, &out)
	if err != nil || exit != 0 {
		t.Fatalf("mysql %q: exit %d %s %v", sql, exit, stderr, err)
	}
	return strings.TrimSpace(out.String())
}

func TestE2EMySQL(t *testing.T) {
	b := setupApp(t, "hello")
	dropMySQLs(t)
	t.Cleanup(func() { dropMySQLs(t) })
	f, _ := os.OpenFile(filepath.Join(b.repoDir, "yawl.yaml"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("  - name: shop-db\n    kind: mysql\n")
	f.Close()
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
	if _, err := run(t, "", "apply", "--yes"); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, "", "deploy", "shop-db"); err != nil {
		t.Fatalf("deploy shop-db: %v\n%s", err, out)
	}
	pw, err := run(t, "", "env", "reveal", "shop-db", "PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	pw = strings.TrimSpace(pw)
	mysqlRun(t, "shop-db", pw, "CREATE TABLE orders (item TEXT); INSERT INTO orders VALUES ('from-before-backup')")

	s3(t)
	if err := server.Run(context.Background(), []string{"--data", filepath.Join(b.dir, "data"), "--etc", filepath.Join(b.dir, "etc"),
		"storage", "set", "http://127.0.0.1:19000", "backups", "yawltest", "yawltest-secret", "us-east-1"}, "dev"); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, "", "db", "backup", "shop-db")
	if err != nil {
		t.Fatalf("backup: %v\n%s", err, out)
	}
	id := strings.Fields(out)[1]
	if out, err := run(t, key+"\n", "db", "restore", "shop-db", id, "--into", "shop-db-2"); err != nil {
		t.Fatalf("restore: %v\n%s", err, out)
	}
	pw2, err := run(t, "", "env", "reveal", "shop-db-2", "PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	if pw2 = strings.TrimSpace(pw2); pw2 == pw {
		t.Fatal("the restored MySQL has the old one's password")
	}
	if got := mysqlRun(t, "shop-db-2", pw2, "SELECT item FROM orders"); got != "from-before-backup" {
		t.Fatalf("on the restored MySQL: %q", got)
	}
}
