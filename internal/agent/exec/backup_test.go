package exec

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
)

func TestBackupErrorMasked(t *testing.T) {
	const password = "db-password-123456"
	rt := &runtime.Fake{OnRun: func(s runtime.ContainerSpec, _ []byte) (string, int, string, error) {
		return "", 1, "FATAL: password authentication failed for " + password, nil
	}}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	rec, _, _ := seal.GenerateKey()
	s3 := fakeS3(t)
	res := d.Handle(context.Background(), cmd(t, proto.CmdBackup, proto.BackupArgs{
		Project: "acme", Env: "production", Name: "acme-db", Version: 17, Password: sealTo(password),
		Storage: proto.Storage{Endpoint: s3, Bucket: "backups", Region: "us-east-1", AccessKey: "ak", SecretKey: "storage-secret-key-1", Insecure: true},
		Key:     "yawl/acme/production/acme-db/x.dump.age", RecoveryPubkey: rec.String()}))
	if res.OK || !strings.Contains(res.Cause, "pg_dump exited 1") {
		t.Fatalf("%+v", res)
	}
	if strings.Contains(res.Cause, password) || strings.Contains(res.Cause, "storage-secret-key-1") {
		t.Fatalf("cause carries a secret: %q", res.Cause)
	}
	s := rt.Ran[0].Spec
	if s.Image != "pgvector/pgvector:pg17-trixie" || s.Network != "yawl-acme-production" || s.Cmd[0] != "pg_dump" ||
		!strings.Contains(strings.Join(s.Cmd, " "), "--format=custom -h acme-db -U app app") {
		t.Fatalf("dump container %+v", s)
	}
}

func TestBackupStorageDownSaysUpload(t *testing.T) {
	kr, sealTo := keyring(t)
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	rec, _, _ := seal.GenerateKey()
	res := d.Handle(context.Background(), cmd(t, proto.CmdBackup, proto.BackupArgs{
		Project: "acme", Env: "production", Name: "acme-db", Version: 17, Password: sealTo("pw-123456"),
		Storage: proto.Storage{Endpoint: "127.0.0.1:1", Bucket: "backups", AccessKey: "ak", SecretKey: "sk-123456", Insecure: true},
		Key:     "k", RecoveryPubkey: rec.String()}))
	if res.OK || !strings.HasPrefix(res.Cause, "upload to backups: ") {
		t.Fatalf("cause %q", res.Cause)
	}
}

func fakeS3(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.URL.Query()["uploads"]; ok && r.Method == "POST" {
			io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>backups</Bucket><Key>k</Key><UploadId>u1</UploadId></InitiateMultipartUploadResult>`)
			return
		}
		io.Copy(io.Discard, r.Body)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestBackupRunOnceErrorNotUpload(t *testing.T) {
	rt := &runtime.Fake{OnRun: func(runtime.ContainerSpec, []byte) (string, int, string, error) {
		return "", 0, "", errors.New("network yawl-acme-production not found")
	}}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	rec, _, _ := seal.GenerateKey()
	res := d.Handle(context.Background(), cmd(t, proto.CmdBackup, proto.BackupArgs{
		Project: "acme", Env: "production", Name: "acme-db", Version: 17, Password: sealTo("pw-123456"),
		Storage: proto.Storage{Endpoint: fakeS3(t), Bucket: "backups", Region: "us-east-1", AccessKey: "ak", SecretKey: "sk-123456", Insecure: true},
		Key:     "k", RecoveryPubkey: rec.String()}))
	if res.OK || res.Cause != "network yawl-acme-production not found" {
		t.Fatalf("cause %q", res.Cause)
	}
}

func TestCopyBackupUnreachable(t *testing.T) {
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	st := proto.Storage{Endpoint: "127.0.0.1:1", Bucket: "b", AccessKey: "AK", SecretKey: "sk-copy-secret", Insecure: true}
	res := d.Handle(context.Background(), cmd(t, proto.CmdCopyBackup, proto.CopyBackupArgs{From: st, To: st, Key: "k"}))
	if res.OK || res.Cause == "" || strings.Contains(res.Cause, "sk-copy-secret") {
		t.Fatalf("%+v", res)
	}
}
