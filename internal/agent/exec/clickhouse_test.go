package exec

import (
	"bytes"
	"context"
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

func TestClickHouseDumpLoad(t *testing.T) {
	e := engines["clickhouse"]
	for _, c := range [][]string{e.dump("events"), e.load("events")} {
		s := clientSpec(e, "clickhouse/clickhouse-server:25.8", "yawl-acme-production", "events", "s3cret-pw", c)
		if len(s.Cmd) != 5 || s.Cmd[0] != "sh" || s.Cmd[1] != "-c" || s.Cmd[4] != "events" || !slices.Equal(s.Env, []string{"CLICKHOUSE_PASSWORD=s3cret-pw"}) {
			t.Fatalf("%q %q", s.Cmd[:2], s.Env)
		}
		if strings.Contains(strings.Join(s.Cmd, " "), "s3cret-pw") || strings.Contains(s.Cmd[2], "--password") {
			t.Fatal("a password in argv")
		}
	}
	dump, load := e.dump("events")[2], e.load("events")[2]
	for _, want := range []string{"FROM system.tables WHERE database = 'app'", "NOT startsWith(name, '.inner')", "ORDER BY v, name",
		"SELECT create_table_query", "FORMAT Native", "tar -cf - ."} {
		if !strings.Contains(dump, want) {
			t.Errorf("the dump has no %q", want)
		}
	}
	rows := chObjects[strings.Index(chObjects, "engine LIKE"):strings.Index(chObjects, ", name")]
	for _, no := range []string{"View", "Dictionary", "Distributed"} {
		if strings.Contains(rows, no) {
			t.Errorf("rows dumped for %s: %s", no, rows)
		}
	}
	at := func(s string) int {
		i := strings.Index(load, s)
		if i < 0 {
			t.Fatalf("the load has no %q", s)
		}
		return i
	}
	if !(at("tar -xf -") < at("DROP DATABASE IF EXISTS app") && at("DROP DATABASE") < at("create t*.sql") &&
		at("create t*.sql") < at("INSERT INTO app.") && at("INSERT INTO app.") < at("create v*.sql")) {
		t.Fatal("the load must drop, make tables, load rows, then make views")
	}
}

func TestImportClickHouseRefused(t *testing.T) {
	d, rt, seal := importer(t)
	a := proto.ImportArgs{Kind: "clickhouse", Project: "acme", Env: "production", Name: "events", Version: 25, Password: seal("pw"), Path: "x.sql"}
	if res := d.Handle(context.Background(), cmd(t, proto.CmdImport, a)); res.OK || !strings.Contains(res.Cause, "does not import into ClickHouse") || len(rt.Ran) != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestRestoreClickHouse(t *testing.T) {
	pub, priv, _ := seal.GenerateKey()
	r, _ := backup.Recipient(pub)
	var enc bytes.Buffer
	w, _ := age.Encrypt(&enc, r)
	io.WriteString(w, "a tar of the dump")
	w.Close()
	rt := &runtime.Fake{}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdRestore, proto.RestoreArgs{Kind: "clickhouse", Project: "acme", Env: "production", Name: "events-restored", Version: 25,
		Password: sealTo("pw"), Key: "k", RecoveryKey: sealTo(seal.EncodeRecovery(priv)),
		SHA256: sumOf(enc.Bytes()), Storage: proto.Storage{Endpoint: s3Serving(t, enc.Bytes()), Bucket: "backups", Region: "us-east-1", AccessKey: "ak", SecretKey: "sk", Insecure: true}}))
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	got := rt.Ran[0]
	if string(got.Stdin) != "a tar of the dump" || got.Spec.Cmd[2] != chLoad || got.Spec.Cmd[4] != "events-restored" || got.Spec.Image != "clickhouse/clickhouse-server:25.8" {
		t.Fatalf("%+v", got.Spec)
	}
}
