package exec

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
)

func ensureArgs(t *testing.T, password string) (proto.EnsureDatabaseArgs, seal.PublicKey, seal.PrivateKey) {
	recPub, recPriv, _ := seal.GenerateKey()
	return proto.EnsureDatabaseArgs{Project: "acme", Env: "production", Name: "acme-db", Version: 17,
		Password: password, RecoveryPubkey: recPub.String()}, recPub, recPriv
}

func TestEnsureCreatesAndSeals(t *testing.T) {
	rt := &runtime.Fake{}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	args, recPub, recPriv := ensureArgs(t, "")
	res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, args))
	var data proto.EnsureDatabaseData
	json.Unmarshal(res.Data, &data)
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	pw, err := kr.Open(data.PasswordBox)
	if err != nil || len(pw) != 43 {
		t.Fatalf("password %q %v", pw, err)
	}
	url, _ := kr.Open(data.URLBox)
	if want := "postgres://app:" + string(pw) + "@acme-db:5432/app?sslmode=disable"; string(url) != want {
		t.Fatalf("url %q, want %q", url, want)
	}
	for _, box := range []string{data.PasswordRecoveryBox, data.URLRecoveryBox} {
		if _, err := seal.Open(recPub, recPriv, box); err != nil {
			t.Fatalf("recovery box does not open: %v", err)
		}
	}
	s := rt.Started[0]
	if s.Name != "yawl-acme-production-acme-db" || s.Image != "pgvector/pgvector:pg17-trixie" || s.Network != "yawl-acme-production" ||
		!slices.Equal(s.Aliases, []string{"acme-db"}) || len(s.Mounts) != 1 ||
		s.Mounts[0] != (runtime.Mount{Source: "yawl-acme-production-acme-db-data", Target: "/var/lib/postgresql/data", Volume: true}) ||
		!slices.Contains(s.Env, "POSTGRES_PASSWORD="+string(pw)) || !slices.Contains(s.Env, "POSTGRES_USER=app") ||
		!slices.Contains(s.Env, "PGDATA=/var/lib/postgresql/data") {
		t.Fatalf("container %+v", s)
	}
	if len(rt.Ran) == 0 || rt.Ran[0].Spec.Cmd[0] != "pg_isready" {
		t.Fatalf("no readiness check: %+v", rt.Ran)
	}
	if regexp.MustCompile(string(pw)).MatchString(d.Keys.Masker().Mask("x " + string(pw))) {
		t.Fatal("the generated password is not masked")
	}
}

func TestEnsureIdempotent(t *testing.T) {
	rt := &runtime.Fake{}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	args, _, _ := ensureArgs(t, "")
	d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, args))
	args.Password = sealTo("an-existing-password")
	c := cmd(t, proto.CmdEnsureDatabase, args)
	c.Key = "dep_2:ensure"
	res := d.Handle(context.Background(), c)
	var data proto.EnsureDatabaseData
	json.Unmarshal(res.Data, &data)
	if !res.OK || len(rt.Started) != 1 || data.PasswordBox != "" {
		t.Fatalf("%+v started=%d data=%+v", res, len(rt.Started), data)
	}
}

func TestEnsureRefusesForeignVolume(t *testing.T) {
	rt := &runtime.Fake{Volumes: map[string]bool{"yawl-acme-production-acme-db-data": true}}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	args, _, _ := ensureArgs(t, "")
	res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, args))
	if res.OK || len(rt.Started) != 0 || res.Cause != "acme-db has data but Yawl has no password for it; it was created outside this server" {
		t.Fatalf("%+v started=%d", res, len(rt.Started))
	}
}

func TestEnsureFailedFirstCreateRecovers(t *testing.T) {
	ready := false
	rt := &runtime.Fake{OnRun: func(runtime.ContainerSpec, []byte) (string, int, string, error) {
		if ready {
			return "", 0, "", nil
		}
		return "", 2, "no response", nil
	}}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys, d.DBDir = kr, t.TempDir()
	defer func(old time.Duration) { dbReady = old }(dbReady)
	dbReady = 0
	args, _, _ := ensureArgs(t, "")
	if res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, args)); res.OK {
		t.Fatalf("first create should fail: %+v", res)
	}
	firstPassword := ""
	for _, e := range rt.Started[0].Env {
		if v, ok := strings.CutPrefix(e, "POSTGRES_PASSWORD="); ok {
			firstPassword = v
		}
	}
	ready = true
	c := cmd(t, proto.CmdEnsureDatabase, args)
	c.Key = "dep_2:ensure"
	res := d.Handle(context.Background(), c)
	var data proto.EnsureDatabaseData
	json.Unmarshal(res.Data, &data)
	if !res.OK || data.PasswordBox == "" {
		t.Fatalf("retry: %+v", res)
	}
	if pw, _ := kr.Open(data.PasswordBox); string(pw) != firstPassword {
		t.Fatalf("retry returned password %q, the volume was made with %q", pw, firstPassword)
	}
}

func TestEnsureKeepsDatabaseWhenImageMissing(t *testing.T) {
	rt := &runtime.Fake{}
	kr, seal := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	a, _, _ := ensureArgs(t, seal("db-password-123456"))
	ctx := context.Background()
	id, _ := rt.Start(ctx, runtime.ContainerSpec{Name: "yawl-acme-production-acme-db", Image: "postgres:17"})
	rt.States[id] = runtime.State{Running: true, Labels: map[string]string{"yawl.pgconf": "old"}}
	rt.PrepareErr = errors.New("toomanyrequests: pull rate limit")
	res := d.Handle(ctx, cmd(t, proto.CmdEnsureDatabase, a))
	if res.OK || !strings.Contains(res.Cause, "pgvector/pgvector:pg17-trixie") || !strings.Contains(res.Cause, "keeps running as it was") {
		t.Fatalf("%+v", res)
	}
	if st, err := rt.Inspect(ctx, id); err != nil || !st.Running {
		t.Fatal("the running database was removed")
	}
}
