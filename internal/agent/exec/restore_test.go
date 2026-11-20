package exec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/behzodhalil/yawl/internal/agent/backup"
	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
)

func s3Serving(t *testing.T, body []byte) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.Header().Set("ETag", `"x"`)
		w.Header().Set("Content-Type", "application/octet-stream")
		if r.Method == "GET" || r.Method == "HEAD" {
			w.Write(body)
		}
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func sumOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestRestoreRefusesSwappedObject(t *testing.T) {
	pub, priv, _ := seal.GenerateKey()
	r, _ := backup.Recipient(pub)
	encrypt := func(s string) []byte {
		var b bytes.Buffer
		w, _ := age.Encrypt(&b, r)
		io.WriteString(w, s)
		w.Close()
		return b.Bytes()
	}
	recorded, planted := sumOf(encrypt("PGDMP the backup")), encrypt("PGDMP planted")
	for _, sum := range []string{recorded, ""} {
		rt := &runtime.Fake{}
		kr, sealTo := keyring(t)
		d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
		d.Keys = kr
		storage := proto.Storage{Endpoint: s3Serving(t, planted), Bucket: "backups", Region: "us-east-1", AccessKey: "ak", SecretKey: "sk", Insecure: true}
		want := "changed in storage"
		if sum == "" {
			want = "no SHA-256 was recorded"
		}
		for _, k := range []string{"", "mysql"} {
			res := d.Handle(context.Background(), cmd(t, proto.CmdRestore, proto.RestoreArgs{Kind: k, Project: "acme", Env: "production", Name: "acme-db-2", Version: 17,
				Password: sealTo("db-password-123456"), Key: "k", SHA256: sum, RecoveryKey: sealTo(seal.EncodeRecovery(priv)), Storage: storage}))
			if res.OK || !strings.Contains(res.Cause, want) {
				t.Fatalf("database %q, sum %q: %+v", k, sum, res)
			}
		}
		res := d.Handle(context.Background(), cmd(t, proto.CmdRestoreVolume, proto.RestoreVolumeArgs{Project: "acme", Env: "production", Service: "api", Name: "uploads",
			Key: "k", SHA256: sum, RecoveryKey: sealTo(seal.EncodeRecovery(priv)), Storage: storage}))
		if res.OK || !strings.Contains(res.Cause, want) {
			t.Fatalf("volume, sum %q: %+v", sum, res)
		}
		if len(rt.Ran) != 0 {
			t.Fatalf("sum %q ran %+v", sum, rt.Ran)
		}
	}
}

func TestRestoreKeyMasked(t *testing.T) {
	pub, priv, _ := seal.GenerateKey()
	recovery := seal.EncodeRecovery(priv)
	r, _ := backup.Recipient(pub)
	var enc bytes.Buffer
	w, _ := age.Encrypt(&enc, r)
	io.WriteString(w, "PGDMP fake dump")
	w.Close()
	const password = "db-password-123456"
	rt := &runtime.Fake{OnRun: func(s runtime.ContainerSpec, stdin []byte) (string, int, string, error) {
		return "", 1, "pg_restore: error: " + recovery + " " + password, nil
	}}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdRestore, proto.RestoreArgs{Project: "acme", Env: "production", Name: "acme-db-2", Version: 17,
		Password: sealTo(password), Key: "k", RecoveryKey: sealTo(recovery),
		SHA256: sumOf(enc.Bytes()), Storage: proto.Storage{Endpoint: s3Serving(t, enc.Bytes()), Bucket: "backups", Region: "us-east-1", AccessKey: "ak", SecretKey: "sk-123456", Insecure: true}}))
	if res.OK || !strings.Contains(res.Cause, "pg_restore exited 1") {
		t.Fatalf("%+v", res)
	}
	if strings.Contains(res.Cause, recovery) || strings.Contains(res.Cause, password) {
		t.Fatalf("cause carries a secret: %q", res.Cause)
	}
	if got := string(rt.Ran[0].Stdin); got != "PGDMP fake dump" {
		t.Fatalf("pg_restore got %q on stdin", got)
	}
	s := rt.Ran[0].Spec
	if !strings.Contains(strings.Join(s.Cmd, " "), "pg_restore --no-owner --no-privileges --exit-on-error --single-transaction -h acme-db-2 -U app -d app") {
		t.Fatalf("cmd %v", s.Cmd)
	}
	if strings.Contains(d.Keys.Masker().Mask(recovery), "••••") {
		t.Fatal("the recovery key was kept in the agent's lifelong masker")
	}
}

func TestRestoreEmptyRecoveryKey(t *testing.T) {
	kr, sealTo := keyring(t)
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdRestore, proto.RestoreArgs{Project: "acme", Env: "production", Name: "acme-db-2", Version: 17,
		Password: sealTo("db-password-123456"), Key: "k", RecoveryKey: sealTo("  ")}))
	if res.OK || strings.Contains(res.Cause, "••••") || !strings.HasPrefix(res.Cause, "the recovery key does not decode") {
		t.Fatalf("%+v", res)
	}
}

func TestHiderEmptyKeyDoesNotInterleaveMask(t *testing.T) {
	if got := hider("")("disk full").Cause; got != "disk full" {
		t.Fatalf("empty key: %q", got)
	}
	if got := hider("k3y")("bad k3y").Cause; got != "bad ••••" {
		t.Fatalf("key: %q", got)
	}
}
