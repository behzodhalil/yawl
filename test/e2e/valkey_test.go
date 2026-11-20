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

func dropValkeys(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"cache", "cache-2"} {
		rt.Remove(ctx, "yawl-e2e-production-"+n)
		rt.C.VolumeRemove(ctx, "yawl-e2e-production-"+n+"-data", dockerclient.VolumeRemoveOptions{Force: true})
	}
}

func valkeyCLI(t *testing.T, host, password string, args ...string) string {
	t.Helper()
	rt, _ := runtime.NewDocker()
	var out bytes.Buffer
	exit, stderr, err := rt.RunOnce(context.Background(), runtime.ContainerSpec{Image: "valkey/valkey:8", Network: "yawl-e2e-production",
		Cmd: append([]string{"valkey-cli", "-h", host, "-a", password, "--no-auth-warning"}, args...)}, nil, &out)
	if err != nil || exit != 0 {
		t.Fatalf("valkey-cli %v: exit %d %s %v", args, exit, stderr, err)
	}
	return strings.TrimSpace(out.String())
}

func TestE2EValkey(t *testing.T) {
	b := setupApp(t, "hello")
	dropValkeys(t)
	t.Cleanup(func() { dropValkeys(t) })
	f, _ := os.OpenFile(filepath.Join(b.repoDir, "yawl.yaml"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("  - name: cache\n    kind: valkey\n    backup: true\n")
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
	if out, err := run(t, "", "deploy", "cache"); err != nil {
		t.Fatalf("deploy cache: %v\n%s", err, out)
	}
	password, err := run(t, "", "env", "reveal", "cache", "PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	password = strings.TrimSpace(password)
	if got := valkeyCLI(t, "cache", password, "SET", "greeting", "from-before-backup"); got != "OK" {
		t.Fatalf("SET: %q", got)
	}
	valkeyCLI(t, "cache", password, "BGREWRITEAOF")

	s3(t)
	if err := server.Run(context.Background(), []string{"--data", filepath.Join(b.dir, "data"), "--etc", filepath.Join(b.dir, "etc"),
		"storage", "set", "http://127.0.0.1:19000", "backups", "yawltest", "yawltest-secret", "us-east-1"}, "dev"); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, "", "db", "backup", "cache")
	if err != nil {
		t.Fatalf("backup: %v\n%s", err, out)
	}
	id := strings.Fields(out)[1]
	if out, err := run(t, key+"\n", "db", "restore", "cache", id, "--into", "cache-2"); err != nil {
		t.Fatalf("restore: %v\n%s", err, out)
	}
	pw2, err := run(t, "", "env", "reveal", "cache-2", "PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	if pw2 = strings.TrimSpace(pw2); pw2 == password {
		t.Fatal("the restored Valkey has the old one's password")
	}
	if got := valkeyCLI(t, "cache-2", pw2, "GET", "greeting"); got != "from-before-backup" {
		t.Fatalf("on the restored Valkey: %q", got)
	}
}
