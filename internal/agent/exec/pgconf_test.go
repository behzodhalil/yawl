package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestHBA(t *testing.T) {
	gw, subnet := netip.MustParseAddr("172.20.0.1"), netip.MustParsePrefix("172.20.0.0/16")
	got := HBA([]netip.Prefix{netip.MustParsePrefix("203.0.113.4/32"), netip.MustParsePrefix("2001:db8::/32")}, gw, subnet, false)
	want := "local all all trust\n" +
		"host all all 127.0.0.1/32 trust\n" +
		"hostssl app app 203.0.113.4/32 scram-sha-256\n" +
		"hostssl app app 2001:db8::/32 scram-sha-256\n" +
		"host all all 172.20.0.1/32 reject\n" +
		"host app all 172.20.0.0/16 scram-sha-256\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if got := HBA(nil, gw, subnet, false); bytes.Contains([]byte(got), []byte("hostssl")) {
		t.Fatalf("no allow, yet:\n%s", got)
	}
}

func TestEnsureTLSKept(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "conf")
	ip := netip.MustParseAddr("5.6.7.8")
	if err := ensureTLS(dir, ip); err != nil {
		t.Fatal(err)
	}
	key1, _ := os.ReadFile(filepath.Join(dir, "server.key"))
	if err := ensureTLS(dir, ip); err != nil {
		t.Fatal(err)
	}
	key2, _ := os.ReadFile(filepath.Join(dir, "server.key"))
	fi, _ := os.Stat(filepath.Join(dir, "server.key"))
	if !bytes.Equal(key1, key2) || len(key1) == 0 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key changed or mode %v", fi.Mode())
	}
}

func outsideDispatcher(t *testing.T) (*Dispatcher, *runtime.Fake) {
	rt := &runtime.Fake{}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys, d.DBDir = kr, t.TempDir()
	return d, rt
}

func ensure(t *testing.T, d *Dispatcher, a proto.EnsureDatabaseArgs) proto.EnsureDatabaseData {
	t.Helper()
	res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, a))
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	var data proto.EnsureDatabaseData
	json.Unmarshal(res.Data, &data)
	return data
}

func file(s runtime.ContainerSpec, path string) *runtime.File {
	for i := range s.Files {
		if s.Files[i].Path == path {
			return &s.Files[i]
		}
	}
	return nil
}

func TestRecreateOnAllowChange(t *testing.T) {
	d, rt := outsideDispatcher(t)
	args, _, _ := ensureArgs(t, "")
	args.OutsidePort, args.Allow, args.PublicIP = 15432, []string{"203.0.113.4/32"}, "5.6.7.8"
	first := ensure(t, d, args)
	args.Password = first.PasswordBox
	ensure(t, d, args)
	if len(rt.Started) != 1 {
		t.Fatalf("started %d", len(rt.Started))
	}
	s := rt.Started[0]
	hba := file(s, "/etc/yawl-pg/pg_hba.conf")
	key := file(s, "/etc/yawl-pg/server.key")
	if hba == nil || key == nil || key.UID != 999 || key.Mode != 0o600 || !bytes.Contains(hba.Data, []byte("hostssl app app 203.0.113.4/32")) {
		t.Fatalf("files %+v", s.Files)
	}
	if s.Ports[15432] != 5432 || !slices.Contains(s.Cmd, "hba_file=/etc/yawl-pg/pg_hba.conf") || !slices.Contains(s.Cmd, "ssl=on") {
		t.Fatalf("ports %v cmd %v", s.Ports, s.Cmd)
	}
	args.Allow = []string{"198.51.100.0/24"}
	ensure(t, d, args)
	if len(rt.Started) != 2 || len(rt.Stopped) != 1 {
		t.Fatalf("started %d stopped %d", len(rt.Started), len(rt.Stopped))
	}
	if rt.Graces[0] < time.Minute {
		t.Fatalf("grace %v", rt.Graces[0])
	}
	n := rt.Started[1]
	if n.Labels["yawl.pgconf"] == s.Labels["yawl.pgconf"] || !slices.Equal(n.Mounts, s.Mounts) ||
		!bytes.Contains(file(n, "/etc/yawl-pg/pg_hba.conf").Data, []byte("198.51.100.0/24")) ||
		!bytes.Equal(file(n, "/etc/yawl-pg/server.key").Data, key.Data) {
		t.Fatalf("recreated %+v", n)
	}
	pw := func(s runtime.ContainerSpec) string {
		for _, e := range s.Env {
			if len(e) > 18 && e[:18] == "POSTGRES_PASSWORD=" {
				return e
			}
		}
		return ""
	}
	if pw(n) != pw(s) || pw(n) == "" {
		t.Fatal("password regenerated")
	}
}

func TestOutsideURL(t *testing.T) {
	d, _ := outsideDispatcher(t)
	args, _, _ := ensureArgs(t, "")
	args.OutsidePort, args.Allow, args.PublicIP = 15432, []string{"203.0.113.4/32"}, "5.6.7.8"
	data := ensure(t, d, args)
	pw, _ := d.Keys.Open(data.PasswordBox)
	url, err := d.Keys.Open(data.OutsideURLBox)
	if err != nil || string(url) != "postgres://app:"+string(pw)+"@5.6.7.8:15432/app?sslmode=require" || data.OutsideURLRecoveryBox == "" {
		t.Fatalf("outside url %q %v", url, err)
	}
	args.Password, args.OutsidePort, args.Allow = data.PasswordBox, 0, nil
	if data := ensure(t, d, args); data.OutsideURLBox != "" {
		t.Fatal("outside url without outside access")
	}
}

func TestOutsidePortRefusedKeepsDatabase(t *testing.T) {
	d, rt := outsideDispatcher(t)
	rt.StartHook = func(s runtime.ContainerSpec) error {
		if len(s.Ports) > 0 {
			return errors.New("Bind for 0.0.0.0:15432 failed: port is already allocated")
		}
		return nil
	}
	args, _, _ := ensureArgs(t, "")
	args.OutsidePort, args.Allow, args.PublicIP = 15432, []string{"203.0.113.4/32"}, "5.6.7.8"
	res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, args))
	if res.OK || !strings.Contains(res.Cause, "port is already allocated") || !strings.Contains(res.Cause, "acme-db runs without outside access") {
		t.Fatalf("%+v", res)
	}
	last := rt.Started[len(rt.Started)-1]
	if len(last.Ports) != 0 || last.Name != "yawl-acme-production-acme-db" {
		t.Fatalf("the database was not started again without the port: %+v", last)
	}
	rt.StartHook = nil
	d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, args))
	if last := rt.Started[len(rt.Started)-1]; last.Ports[15432] != 5432 {
		t.Fatalf("not retried: %+v", last.Ports)
	}
}
