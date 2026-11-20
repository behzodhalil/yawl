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

func dropMongos(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"docs", "docs-2"} {
		rt.Remove(ctx, "yawl-e2e-production-"+n)
		rt.C.VolumeRemove(ctx, "yawl-e2e-production-"+n+"-data", dockerclient.VolumeRemoveOptions{Force: true})
	}
}

func mongoEval(t *testing.T, host, password, script string) string {
	t.Helper()
	rt, _ := runtime.NewDocker()
	var out bytes.Buffer
	exit, stderr, err := rt.RunOnce(context.Background(), runtime.ContainerSpec{Image: "mongo:8.0", Network: "yawl-e2e-production",
		Env: []string{"MONGO_URL=mongodb://app:" + password + "@" + host + ":27017/?authSource=admin&directConnection=true"},
		Cmd: []string{"sh", "-c", `mongosh "$MONGO_URL" --quiet --eval '` + script + `'`}}, nil, &out)
	if err != nil || exit != 0 {
		t.Fatalf("mongosh %q: exit %d %s %s %v", script, exit, out.String(), stderr, err)
	}
	return strings.TrimSpace(out.String())
}

func TestE2EMongo(t *testing.T) {
	b := setupApp(t, "hello")
	dropMongos(t)
	t.Cleanup(func() { dropMongos(t) })
	f, _ := os.OpenFile(filepath.Join(b.repoDir, "yawl.yaml"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("  - name: docs\n    kind: mongodb\n")
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
	if out, err := run(t, "", "deploy", "docs"); err != nil {
		t.Fatalf("deploy docs: %v\n%s", err, out)
	}
	pw, err := run(t, "", "env", "reveal", "docs", "PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	pw = strings.TrimSpace(pw)
	mongoEval(t, "docs", pw, `db.getSiblingDB("app").orders.insertOne({item: "from-before-backup"})`)

	s3(t)
	if err := server.Run(context.Background(), []string{"--data", filepath.Join(b.dir, "data"), "--etc", filepath.Join(b.dir, "etc"),
		"storage", "set", "http://127.0.0.1:19000", "backups", "yawltest", "yawltest-secret", "us-east-1"}, "dev"); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, "", "db", "backup", "docs")
	if err != nil {
		t.Fatalf("backup: %v\n%s", err, out)
	}
	id := strings.Fields(out)[1]
	if out, err := run(t, key+"\n", "db", "restore", "docs", id, "--into", "docs-2"); err != nil {
		t.Fatalf("restore: %v\n%s", err, out)
	}
	pw2, err := run(t, "", "env", "reveal", "docs-2", "PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	if pw2 = strings.TrimSpace(pw2); pw2 == pw {
		t.Fatal("the restored MongoDB has the old one's password")
	}
	if got := mongoEval(t, "docs-2", pw2, `print(db.getSiblingDB("app").orders.findOne().item)`); got != "from-before-backup" {
		t.Fatalf("on the restored MongoDB: %q", got)
	}
}
