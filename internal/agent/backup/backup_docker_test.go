//go:build docker

package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/minio/minio-go/v7"
	"github.com/moby/moby/client"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/seal"
)

const minioAddr = "127.0.0.1:19000"

var (
	S3TestImage = "chrislusf/seaweedfs"
	S3TestCmd   = []string{"server", "-dir=/data", "-ip=127.0.0.1", "-ip.bind=127.0.0.1", "-s3", "-s3.port=19000",
		"-master.port=19333", "-volume.port=19080", "-filer.port=19888",
		"-master.port.grpc=29333", "-volume.port.grpc=29080", "-filer.port.grpc=29888", "-s3.port.grpc=29000"}
)

// world starts an S3 server (SeaweedFS: MinIO no longer publishes images) on the host network and a postgres:17 with one row in
// notes, and returns what Dump needs.
func world(t *testing.T) (*runtime.Docker, DB, Target) {
	t.Helper()
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	tag := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
	mid, err := rt.Start(ctx, runtime.ContainerSpec{Name: "yawl-test-s3-" + tag, Image: S3TestImage, HostNetwork: true,
		Env: []string{"AWS_ACCESS_KEY_ID=yawltest", "AWS_SECRET_ACCESS_KEY=yawltest-secret"}, Cmd: S3TestCmd})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, mid) })
	net := "yawl-test-net-bk-" + tag
	if err := rt.EnsureNetwork(ctx, net); err != nil {
		t.Fatal(err)
	}
	// registered first, so it runs after the containers on it are gone:
	// a network per run would otherwise use up Docker's address pools
	t.Cleanup(func() { rt.C.NetworkRemove(ctx, net, client.NetworkRemoveOptions{}) })
	pid, err := rt.Start(ctx, runtime.ContainerSpec{Name: "yawl-test-pg-" + tag, Image: "postgres:17", Network: net, Aliases: []string{"db"},
		Env: []string{"POSTGRES_USER=app", "POSTGRES_DB=app", "POSTGRES_PASSWORD=pw-test-123"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, pid) })
	db := DB{Image: "postgres:17", Network: net, Host: "db", Password: "pw-test-123"}
	psql(t, rt, db, "CREATE TABLE notes (note text); INSERT INTO notes VALUES ('from-before-backup');")
	tg := Target{Endpoint: minioAddr, Bucket: "backups", Region: "us-east-1", AccessKey: "yawltest", SecretKey: "yawltest-secret", Insecure: true}
	c, _ := tg.client()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(300 * time.Millisecond) {
		if err := c.MakeBucket(ctx, "backups", minio.MakeBucketOptions{Region: "us-east-1"}); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("s3: %v", err)
		}
	}
	return rt, db, tg
}

func psql(t *testing.T, rt *runtime.Docker, db DB, sql string) string {
	t.Helper()
	var out bytes.Buffer
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(500 * time.Millisecond) {
		out.Reset()
		exit, stderr, err := rt.RunOnce(context.Background(), runtime.ContainerSpec{Image: db.Image, Network: db.Network, Env: []string{"PGPASSWORD=" + db.Password},
			Cmd: []string{"psql", "-h", db.Host, "-U", "app", "-d", "app", "-tAc", sql}}, nil, &out)
		if err == nil && exit == 0 {
			return out.String()
		}
		if time.Now().After(deadline) {
			t.Fatalf("psql %q: exit %d %s %v", sql, exit, stderr, err)
		}
	}
}

func TestDumpToMinio(t *testing.T) {
	rt, db, tg := world(t)
	pub, priv, _ := seal.GenerateKey()
	ctx := context.Background()
	size, sum, err := Dump(ctx, rt, db, pub, tg, "yawl/t/p/db/one.dump.age")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := tg.client()
	obj, err := c.GetObject(ctx, "backups", "yawl/t/p/db/one.dump.age", minio.GetObjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(obj)
	h := sha256.Sum256(raw)
	if int64(len(raw)) != size || hex.EncodeToString(h[:]) != sum {
		t.Fatalf("object %d bytes %x, Dump said %d %s", len(raw), h, size, sum)
	}
	id, _ := Identity(priv)
	rd, err := age.Decrypt(bytes.NewReader(raw), id)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(rd)
	if !bytes.HasPrefix(plain, []byte("PGDMP")) {
		t.Fatalf("not a custom-format dump: %q", plain[:min(len(plain), 16)])
	}
}

func TestDumpFailureRemovesObject(t *testing.T) {
	rt, db, tg := world(t)
	db.Password = "wrong-password"
	pub, _, _ := seal.GenerateKey()
	ctx := context.Background()
	if _, _, err := Dump(ctx, rt, db, pub, tg, "yawl/t/p/db/bad.dump.age"); err == nil {
		t.Fatal("a dump with the wrong password succeeded")
	}
	c, _ := tg.client()
	if _, err := c.StatObject(ctx, "backups", "yawl/t/p/db/bad.dump.age", minio.StatObjectOptions{}); err == nil {
		t.Fatal("the failed backup left an object behind")
	}
}

func TestRestoreFromMinio(t *testing.T) {
	rt, db, tg := world(t)
	pub, priv, _ := seal.GenerateKey()
	ctx := context.Background()
	_, sum, err := Dump(ctx, rt, db, pub, tg, "yawl/t/p/db/r.dump.age")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := rt.Start(ctx, runtime.ContainerSpec{Name: fmt.Sprintf("yawl-test-pg2-%d", time.Now().UnixNano()), Image: "postgres:17",
		Network: db.Network, Aliases: []string{"db2"}, Env: []string{"POSTGRES_USER=app", "POSTGRES_DB=app", "POSTGRES_PASSWORD=pw2-test-123"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, id2) })
	db2 := DB{Image: "postgres:17", Network: db.Network, Host: "db2", Password: "pw2-test-123"}
	psql(t, rt, db2, "select 1")
	ident, _ := Identity(priv)
	if err := Restore(ctx, rt, db2, ident, tg, "yawl/t/p/db/r.dump.age", sum); err != nil {
		t.Fatal(err)
	}
	if got := psql(t, rt, db2, "SELECT note FROM notes"); got != "from-before-backup\n" {
		t.Fatalf("restored db reads %q", got)
	}
	_, other, _ := seal.GenerateKey()
	wrong, _ := Identity(other)
	if err := Restore(ctx, rt, db2, wrong, tg, "yawl/t/p/db/r.dump.age", sum); err == nil {
		t.Fatal("restored with the wrong recovery key")
	}
}

func TestVolumeRoundTrip(t *testing.T) {
	rt, _, tg := world(t)
	ctx := context.Background()
	vol := fmt.Sprintf("yawl-test-vol-%d", time.Now().UnixNano()%1_000_000)
	t.Cleanup(func() { rt.RemoveVolume(ctx, vol) })
	sh := func(cmd string) string {
		var out bytes.Buffer
		exit, stderr, err := rt.RunOnce(ctx, runtime.ContainerSpec{Image: "alpine:3", Mounts: []runtime.Mount{{Source: vol, Target: "/data", Volume: true}},
			Cmd: []string{"sh", "-c", cmd}}, nil, &out)
		if err != nil || exit != 0 {
			t.Fatalf("%s: %d %s %v", cmd, exit, stderr, err)
		}
		return out.String()
	}
	sh("mkdir -p /data/up && echo kept > /data/up/note")
	pub, priv, _ := seal.GenerateKey()
	_, sum, err := DumpVolume(ctx, rt, vol, pub, tg, "yawl/t/p/api/volumes/up/one.tar.age")
	if err != nil {
		t.Fatal(err)
	}
	sh("rm -rf /data/up && echo stray > /data/other")
	id, _ := Identity(priv)
	if err := RestoreVolume(ctx, rt, vol, id, tg, "yawl/t/p/api/volumes/up/one.tar.age", sum); err != nil {
		t.Fatal(err)
	}
	if got := sh("cat /data/up/note; ls /data"); got != "kept\nup\n" {
		t.Fatalf("after restore: %q", got)
	}
}

func TestCopy(t *testing.T) {
	_, _, tg := world(t)
	ctx := context.Background()
	c, _ := tg.client()
	body := bytes.Repeat([]byte("encrypted-bytes-"), 1<<16)
	if _, err := c.PutObject(ctx, "backups", "yawl/t/p/db/k.dump.age", bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{}); err != nil {
		t.Fatal(err)
	}
	to := tg
	to.Bucket = "copies"
	if err := c.MakeBucket(ctx, "copies", minio.MakeBucketOptions{Region: "us-east-1"}); err != nil {
		t.Fatal(err)
	}
	n, err := Copy(ctx, tg, to, "yawl/t/p/db/k.dump.age")
	if err != nil || n != int64(len(body)) {
		t.Fatalf("%d %v", n, err)
	}
	obj, _ := c.GetObject(ctx, "copies", "yawl/t/p/db/k.dump.age", minio.GetObjectOptions{})
	got, _ := io.ReadAll(obj)
	if !bytes.Equal(got, body) {
		t.Fatalf("copied %d bytes, want %d", len(got), len(body))
	}
	if _, err := Copy(ctx, tg, to, "yawl/t/p/db/missing.age"); err == nil {
		t.Fatal("a missing object copied")
	}
}
