package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/behzodhalil/yawl/internal/agent/backup"
	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
)

func mysqlArgs(t *testing.T) proto.EnsureDatabaseArgs {
	a, _, _ := ensureArgs(t, "")
	a.Kind, a.Name, a.Version = "mysql", "shop-db", 8
	return a
}

func TestEnsureMySQLFake(t *testing.T) {
	rt := &runtime.Fake{}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, mysqlArgs(t)))
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	var data proto.EnsureDatabaseData
	json.Unmarshal(res.Data, &data)
	pw, _ := kr.Open(data.PasswordBox)
	url, _ := kr.Open(data.URLBox)
	if string(url) != "mysql://app:"+string(pw)+"@shop-db:3306/app" {
		t.Fatalf("url %q", url)
	}
	s := rt.Started[0]
	for _, e := range []string{"MYSQL_DATABASE=app", "MYSQL_USER=app", "MYSQL_PASSWORD=" + string(pw), "MYSQL_ROOT_PASSWORD=" + string(pw), "MYSQL_ROOT_HOST=localhost"} {
		if !slices.Contains(s.Env, e) {
			t.Errorf("env lacks %q: %v", e, s.Env)
		}
	}
	if s.Image != "mysql:8.4" || s.Mounts[0].Target != "/var/lib/mysql" || len(s.Cmd) != 0 {
		t.Fatalf("spec %+v", s)
	}
	probe := rt.Ran[len(rt.Ran)-1].Spec
	if strings.Join(probe.Cmd, " ") != "mysql -h shop-db -u app -e SELECT 1 app" || !slices.Contains(probe.Env, "MYSQL_PWD="+string(pw)) || strings.Contains(strings.Join(probe.Cmd, " "), string(pw)) {
		t.Fatalf("probe %+v", probe)
	}
}

func TestBackupMySQLDump(t *testing.T) {
	e := engines["mysql"]
	s := clientSpec(e, "mysql:8.4", "yawl-acme-production", "shop-db", "pw", e.dump("shop-db"))
	if strings.Join(s.Cmd, " ") != "mysqldump --single-transaction --no-tablespaces --routines --triggers --events -h shop-db -u app app" || !slices.Equal(s.Env, []string{"MYSQL_PWD=pw"}) || s.Image != "mysql:8.4" {
		t.Fatalf("dump %+v", s)
	}
}

func TestRestoreMySQLLoad(t *testing.T) {
	pub, priv, _ := seal.GenerateKey()
	recovery := seal.EncodeRecovery(priv)
	r, _ := backup.Recipient(pub)
	var enc bytes.Buffer
	w, _ := age.Encrypt(&enc, r)
	io.WriteString(w, "CREATE TABLE t (x int);")
	w.Close()
	rt := &runtime.Fake{}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdRestore, proto.RestoreArgs{Kind: "mysql", Project: "acme", Env: "production", Name: "shop-db2", Version: 8,
		Password: sealTo("pw2"), Key: "k", RecoveryKey: sealTo(recovery),
		SHA256: sumOf(enc.Bytes()), Storage: proto.Storage{Endpoint: s3Serving(t, enc.Bytes()), Bucket: "backups", Region: "us-east-1", AccessKey: "a", SecretKey: "s", Insecure: true}}))
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	s := rt.Ran[0]
	if strings.Join(s.Spec.Cmd, " ") != "mysql -h shop-db2 -u app app" || string(s.Stdin) != "CREATE TABLE t (x int);" || !slices.Contains(s.Spec.Env, "MYSQL_PWD=pw2") {
		t.Fatalf("load %+v %q", s.Spec, s.Stdin)
	}
	if len(rt.Stopped) != 0 {
		t.Fatal("a MySQL restore stopped the container")
	}
}
