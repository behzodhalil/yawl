//go:build docker

package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/moby/moby/client"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
)

func TestPITRReal(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	tag := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
	project := "t" + tag
	s3, err := rt.Start(ctx, runtime.ContainerSpec{Name: "yawl-test-s3-" + tag, Image: "chrislusf/seaweedfs", HostNetwork: true,
		Env: []string{"AWS_ACCESS_KEY_ID=yawltest", "AWS_SECRET_ACCESS_KEY=yawltest-secret"},
		Cmd: []string{"server", "-dir=/data", "-ip=127.0.0.1", "-ip.bind=127.0.0.1", "-s3", "-s3.port=19000",
			"-master.port=19333", "-volume.port=19080", "-filer.port=19888",
			"-master.port.grpc=29333", "-volume.port.grpc=29080", "-filer.port.grpc=29888", "-s3.port.grpc=29000"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, s3) })
	storage := proto.Storage{Endpoint: "127.0.0.1:19000", Bucket: "backups", Region: "us-east-1", AccessKey: "yawltest", SecretKey: "yawltest-secret", Insecure: true}
	mc, err := minio.New(storage.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(storage.AccessKey, storage.SecretKey, ""), Region: storage.Region})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(300 * time.Millisecond) {
		if err := mc.MakeBucket(ctx, "backups", minio.MakeBucketOptions{Region: "us-east-1"}); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("s3: %v", err)
		}
	}
	network := "yawl-" + project + "-production"
	t.Cleanup(func() { rt.C.NetworkRemove(ctx, network, client.NetworkRemoveOptions{}) })
	recPub, recPriv, _ := seal.GenerateKey()
	ensure := func(name string, pitr bool) (proto.EnsureDatabaseArgs, string) {
		a, _, _ := ensureArgs(t, "")
		a.Project, a.Name, a.PITR, a.RecoveryPubkey = project, name, pitr, recPub.String()
		container, volume, _, _ := dbNames("", project, "production", name, 17)
		t.Cleanup(func() {
			rt.Remove(ctx, container)
			rt.C.VolumeRemove(ctx, volume, client.VolumeRemoveOptions{Force: true})
		})
		res := d.Handle(ctx, cmd(t, proto.CmdEnsureDatabase, a))
		if !res.OK {
			t.Fatalf("ensure %s: %+v", name, res)
		}
		var data proto.EnsureDatabaseData
		json.Unmarshal(res.Data, &data)
		a.Password = data.PasswordBox
		return a, data.PasswordBox
	}
	psql := func(host, box, sql string) string {
		pw, _ := kr.Open(box)
		var out bytes.Buffer
		for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(500 * time.Millisecond) {
			out.Reset()
			exit, stderr, err := rt.RunOnce(ctx, runtime.ContainerSpec{Image: "postgres:17", Network: network, Env: []string{"PGPASSWORD=" + string(pw)},
				Cmd: []string{"psql", "-h", host, "-U", "app", "-d", "app", "-tAc", sql}}, nil, &out)
			if err == nil && exit == 0 {
				return strings.TrimSpace(out.String())
			}
			if time.Now().After(deadline) {
				t.Fatalf("psql %s %q: exit %d %s %v", host, sql, exit, stderr, err)
			}
		}
	}
	_, srcBox := ensure("shop-db", true)
	psql("shop-db", srcBox, "CREATE TABLE notes (note text); INSERT INTO notes VALUES ('before-base')")
	res := d.Handle(ctx, cmd(t, proto.CmdBackup, proto.BackupArgs{Base: true, Project: project, Env: "production", Name: "shop-db", Version: 17,
		Password: srcBox, Storage: storage, Key: "base.tar.age", RecoveryPubkey: recPub.String()}))
	var base proto.BackupData
	if json.Unmarshal(res.Data, &base); !res.OK {
		t.Fatalf("base: %+v", res)
	}
	psql("shop-db", srcBox, "INSERT INTO notes VALUES ('t1')")
	time.Sleep(2 * time.Second)
	mid := time.Now().UTC()
	time.Sleep(2 * time.Second)
	psql("shop-db", srcBox, "INSERT INTO notes VALUES ('t2'); SELECT pg_switch_wal()")
	time.Sleep(walQuiet + 2*time.Second)
	res = d.Handle(ctx, cmd(t, proto.CmdShipWAL, proto.ShipWALArgs{Project: project, Env: "production", Name: "shop-db",
		Storage: storage, KeyPrefix: "wal/", RecoveryPubkey: recPub.String()}))
	var ship proto.ShipWALData
	json.Unmarshal(res.Data, &ship)
	if !res.OK || ship.Batch == nil {
		t.Fatalf("ship: %+v %+v", res, ship)
	}
	ens, newBox := ensure("shop-db-2", false)
	res = d.Handle(ctx, cmd(t, proto.CmdRestore, proto.RestoreArgs{Ensure: &ens, Project: project, Env: "production", Name: "shop-db-2", Version: 17,
		Password: newBox, Storage: storage, Key: "base.tar.age", SHA256: base.SHA256, RecoveryKey: sealTo(seal.EncodeRecovery(recPriv)),
		PITR: &proto.PITRRestore{WAL: []string{ship.Batch.Key}, WALSHA256: []string{ship.Batch.SHA256}, At: mid.Format("2006-01-02 15:04:05+00"), SourcePassword: srcBox}}))
	if !res.OK {
		t.Fatalf("recover: %+v", res)
	}
	if got := psql("shop-db-2", newBox, "SELECT string_agg(note, ',' ORDER BY note) FROM notes"); got != "before-base,t1" {
		t.Fatalf("restored to %s: %q", mid, got)
	}
}
