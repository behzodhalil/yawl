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

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/moby/moby/client"
)

func TestEnsureMySQLReal(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	project := fmt.Sprintf("t%d", time.Now().UnixNano()%1_000_000)
	ensure := func(name string) string {
		a := mysqlArgs(t)
		a.Project, a.Name = project, name
		container, volume, _, _ := dbNames("mysql", project, "production", name, 8)
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
		pw, _ := kr.Open(data.PasswordBox)
		return string(pw)
	}
	network := "yawl-" + project + "-production"
	t.Cleanup(func() { rt.C.NetworkRemove(ctx, network, client.NetworkRemoveOptions{}) })
	pw1 := ensure("shop-db")
	e := engines["mysql"]
	run := func(pw string, stdin []byte, args ...string) string {
		var out bytes.Buffer
		exit, stderr, err := rt.RunOnce(ctx, clientSpec(e, "mysql:8.4", network, "", pw, args), bytes.NewReader(stdin), &out)
		if err != nil || exit != 0 {
			t.Fatalf("%v: exit %d %s %v", args, exit, stderr, err)
		}
		return out.String()
	}
	run(pw1, nil, "mysql", "-h", "shop-db", "-u", "app", "app", "-e", "CREATE TABLE notes (body TEXT); INSERT INTO notes VALUES ('salom')")
	dump := run(pw1, nil, e.dump("shop-db")...)
	pw2 := ensure("shop-db-2")
	if pw2 == pw1 {
		t.Fatal("two databases, one password")
	}
	run(pw2, []byte(dump), e.load("shop-db-2")...)
	if got := run(pw2, nil, "mysql", "-h", "shop-db-2", "-u", "app", "app", "-N", "-e", "SELECT body FROM notes"); strings.TrimSpace(got) != "salom" {
		t.Fatalf("after the load: %q", got)
	}
}
