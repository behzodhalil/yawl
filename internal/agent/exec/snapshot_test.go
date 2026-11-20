package exec

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func snapArgs(sealTo func(string) string) proto.SnapshotArgs {
	return proto.SnapshotArgs{Project: "acme", Env: "production", Name: "acme-db", Version: 17, Password: sealTo("db-password-123456")}
}

func TestSnapshotWritesAndKeepsThree(t *testing.T) {
	rt := &runtime.Fake{OnRun: func(s runtime.ContainerSpec, _ []byte) (string, int, string, error) { return "DUMP", 0, "", nil }}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys, d.SnapDir = kr, t.TempDir()
	dir := filepath.Join(d.SnapDir, "yawl-acme-production-acme-db")
	for i, id := range []string{"dep_1", "dep_2", "dep_3", "dep_4"} {
		c, _ := proto.NewCommand(id+":snapshot", proto.CmdSnapshot, snapArgs(sealTo))
		res := d.Handle(context.Background(), c)
		if !res.OK {
			t.Fatalf("%+v", res)
		}
		os.Chtimes(filepath.Join(dir, id+".dump"), time.Unix(int64(1000+i), 0), time.Unix(int64(1000+i), 0))
	}
	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
		if fi, _ := e.Info(); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", e.Name(), fi.Mode())
		}
	}
	if strings.Join(names, ",") != "dep_2.dump,dep_3.dump,dep_4.dump" {
		t.Fatalf("kept %v", names)
	}
	if s := rt.Ran[0].Spec; s.Image != "pgvector/pgvector:pg17-trixie" || s.Cmd[0] != "pg_dump" || !strings.Contains(strings.Join(s.Cmd, " "), "--format=custom -h acme-db -U app app") {
		t.Fatalf("dump container %+v", s)
	}
}

type fullWriter struct{}

func (fullWriter) Write([]byte) (int, error) { return 0, syscall.ENOSPC }
func (fullWriter) Close() error              { return nil }

func TestSnapshotNoSpace(t *testing.T) {
	rt := &runtime.Fake{OnRun: func(s runtime.ContainerSpec, _ []byte) (string, int, string, error) { return "DUMP", 0, "", nil }}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys, d.SnapDir = kr, t.TempDir()
	var made string
	d.SnapFile = func(path string) (io.WriteCloser, error) {
		made = path
		os.WriteFile(path, []byte("partial"), 0o600)
		return fullWriter{}, nil
	}
	res := d.Handle(context.Background(), cmd(t, proto.CmdSnapshot, snapArgs(sealTo)))
	if res.OK || !strings.HasPrefix(res.Cause, "Not enough free disk for a snapshot of acme-db (") || !strings.HasSuffix(res.Cause, " MB free).") {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(made); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial file left: %v", err)
	}
}

func TestSnapshotOtherEngines(t *testing.T) {
	rt := &runtime.Fake{}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	kr, sealTo := keyring(t)
	d.Keys, d.SnapDir = kr, t.TempDir()
	for kind, want := range map[string]string{"mysql": "mysqldump", "mongodb": "mongodump"} {
		c := cmd(t, proto.CmdSnapshot, proto.SnapshotArgs{Kind: kind, Project: "acme", Env: "production", Name: "db", Password: sealTo("pw")})
		c.Key = "dep_" + kind + ":snapshot:db"
		if res := d.Handle(context.Background(), c); !res.OK || !strings.Contains(strings.Join(rt.Ran[len(rt.Ran)-1].Spec.Cmd, " "), want) {
			t.Fatalf("%s: %+v %v", kind, res, rt.Ran[len(rt.Ran)-1].Spec.Cmd)
		}
	}
}
