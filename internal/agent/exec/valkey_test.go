package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/behzodhalil/yawl/internal/agent/backup"
	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
)

func valkeyArgs(t *testing.T) proto.EnsureDatabaseArgs {
	a, _, _ := ensureArgs(t, "")
	a.Kind, a.Name, a.Version = "valkey", "cache", 8
	return a
}

func TestEnsureValkeyFake(t *testing.T) {
	rt := &runtime.Fake{}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, valkeyArgs(t)))
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	var data proto.EnsureDatabaseData
	json.Unmarshal(res.Data, &data)
	pw, _ := kr.Open(data.PasswordBox)
	url, _ := kr.Open(data.URLBox)
	if string(url) != "redis://default:"+string(pw)+"@cache:6379" {
		t.Fatalf("url %q", url)
	}
	s := rt.Started[0]
	wantCmd := []string{"valkey-server", "--requirepass", string(pw), "--appendonly", "yes", "--appendfsync", "everysec", "--maxmemory-policy", "noeviction"}
	if s.Image != "valkey/valkey:8" || !slices.Equal(s.Cmd, wantCmd) || len(s.Ports) != 0 || len(s.Env) != 0 ||
		!slices.Equal(s.Aliases, []string{"cache"}) || s.Mounts[0].Source != "yawl-acme-production-cache-data" || s.Mounts[0].Target != "/data" {
		t.Fatalf("spec %+v", s)
	}
	probe := rt.Ran[len(rt.Ran)-1].Spec
	if probe.Image != "valkey/valkey:8" || strings.Join(probe.Cmd, " ") != "valkey-cli -h cache -a "+string(pw)+" --no-auth-warning ping" {
		t.Fatalf("probe %+v", probe)
	}
}

func TestEnsureEmptyKindIsPostgres(t *testing.T) {
	specOf := func(kind string) runtime.ContainerSpec {
		rt := &runtime.Fake{}
		kr, _ := keyring(t)
		d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
		d.Keys = kr
		a, _, _ := ensureArgs(t, "")
		a.Kind = kind
		if res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, a)); !res.OK {
			t.Fatalf("%q: %+v", kind, res)
		}
		s := rt.Started[0]
		s.Env, s.Files = nil, nil
		return s
	}
	if a, b := specOf(""), specOf("postgres"); !reflect.DeepEqual(a, b) || a.Image != "pgvector/pgvector:pg17-trixie" {
		t.Fatalf("\n%+v\n%+v", a, b)
	}
}

func TestRestoreValkeyFake(t *testing.T) {
	pub, priv, _ := seal.GenerateKey()
	recovery := seal.EncodeRecovery(priv)
	r, _ := backup.Recipient(pub)
	var enc bytes.Buffer
	w, _ := age.Encrypt(&enc, r)
	io.WriteString(w, "a tar of the data volume")
	w.Close()
	rt := &runtime.Fake{}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	ens := valkeyArgs(t)
	ens.Name, ens.Password = "cache2", sealTo("pw-of-cache2")
	if res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, ens)); !res.OK {
		t.Fatalf("ensure %+v", res)
	}
	res := d.Handle(context.Background(), cmd(t, proto.CmdRestore, proto.RestoreArgs{Kind: "valkey", Ensure: &ens,
		Project: "acme", Env: "production", Name: "cache2", Version: 8, Password: sealTo("pw-of-cache2"), Key: "k", RecoveryKey: sealTo(recovery),
		SHA256: sumOf(enc.Bytes()), Storage: proto.Storage{Endpoint: s3Serving(t, enc.Bytes()), Bucket: "backups", Region: "us-east-1", AccessKey: "ak", SecretKey: "sk", Insecure: true}}))
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	if !slices.Contains(rt.Stopped, "yawl-acme-production-cache2") {
		t.Fatalf("not stopped: %v", rt.Stopped)
	}
	var loaded bool
	for _, run := range rt.Ran {
		if string(run.Stdin) == "a tar of the data volume" {
			loaded = true
		}
	}
	last := rt.Started[len(rt.Started)-1]
	if !loaded || last.Name != "yawl-acme-production-cache2" || last.Image != "valkey/valkey:8" {
		t.Fatalf("loaded %v, last start %+v", loaded, last)
	}
}

func TestValkeyMaxmemory(t *testing.T) {
	e := engines["valkey"]
	with := e.cmd("pw", 256<<20)
	if i := slices.Index(with, "--maxmemory"); i < 0 || with[i+1] != "201326592" {
		t.Fatalf("256MB limit: %v", with)
	}
	if slices.Contains(e.cmd("pw", 0), "--maxmemory") {
		t.Fatal("a maxmemory without a limit")
	}
}

func TestBackupValkeySnapshot(t *testing.T) {
	rewriting := 2
	rt := &runtime.Fake{OnRun: func(s runtime.ContainerSpec, _ []byte) (string, int, string, error) {
		switch {
		case slices.Contains(s.Cmd, "INFO"):
			if rewriting--; rewriting > 0 {
				return "aof_rewrite_in_progress:1\r\naof_rewrite_scheduled:0\r\n", 0, "", nil
			}
			return "aof_rewrite_in_progress:0\r\naof_rewrite_scheduled:0\r\n", 0, "", nil
		case slices.Contains(s.Cmd, "GET"):
			return "auto-aof-rewrite-percentage\n100\n", 0, "", nil
		}
		return "", 0, "", nil
	}}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	pub, _, _ := seal.GenerateKey()
	res := d.Handle(context.Background(), cmd(t, proto.CmdBackup, proto.BackupArgs{Kind: "valkey", Project: "acme", Env: "production", Name: "cache", Version: 8,
		Password: sealTo("pw"), Key: "k", RecoveryPubkey: pub.String(),
		Storage: proto.Storage{Endpoint: s3Serving(t, nil), Bucket: "backups", Region: "us-east-1", AccessKey: "a", SecretKey: "s", Insecure: true}}))
	var cmds []string
	for _, r := range rt.Ran {
		cmds = append(cmds, strings.Join(r.Spec.Cmd, " "))
	}
	joined := strings.Join(cmds, "\n")
	pause, wait, back := strings.Index(joined, "CONFIG SET auto-aof-rewrite-percentage 0"), strings.LastIndex(joined, "INFO persistence"), strings.Index(joined, "CONFIG SET auto-aof-rewrite-percentage 100")
	if pause < 0 || wait < pause || back < wait || strings.Count(joined, "INFO persistence") < 2 || !strings.HasPrefix(res.Cause, "upload to backups") {
		t.Fatalf("pause, wait, then put back after the upload: %q\n%s", res.Cause, joined)
	}
}

func TestEnsureRecreatesOnCommandChange(t *testing.T) {
	rt := &runtime.Fake{}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	a := valkeyArgs(t)
	res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, a))
	var data proto.EnsureDatabaseData
	json.Unmarshal(res.Data, &data)
	a.Password = data.PasswordBox
	label := func(v string) {
		st := rt.States["c1"]
		st.Labels = map[string]string{"yawl.project": "acme", "yawl.env": "production", "yawl.service": "cache"}
		if v != "" {
			st.Labels["yawl.cmd"] = v
		}
		rt.States["c1"] = st
	}
	label("")
	d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, a))
	if len(rt.Started) != 1 {
		t.Fatalf("an unlabelled database restarted: %d", len(rt.Started))
	}
	label("old")
	if res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, a)); !res.OK || len(rt.Started) != 2 {
		t.Fatalf("a changed command did not recreate: %+v %d", res, len(rt.Started))
	}
	if l := rt.Started[1].Labels["yawl.cmd"]; l == "" || strings.Contains(l, "--requirepass") {
		t.Fatalf("label %q", l)
	}
}
