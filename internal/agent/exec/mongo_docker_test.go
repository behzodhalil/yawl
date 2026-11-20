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

func TestEnsureMongoReal(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	project := fmt.Sprintf("t%d", time.Now().UnixNano()%1_000_000)
	network := "yawl-" + project + "-production"
	t.Cleanup(func() { rt.C.NetworkRemove(ctx, network, client.NetworkRemoveOptions{}) })
	ensure := func(name string) string {
		a := mongoArgs(t)
		a.Project, a.Name = project, name
		container, volume, _, _ := dbNames("mongodb", project, "production", name, 8)
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
		if again := d.Handle(ctx, cmd(t, proto.CmdEnsureDatabase, a)); !again.OK {
			t.Fatalf("second ensure %s: %+v", name, again)
		}
		pw, _ := kr.Open(data.PasswordBox)
		return string(pw)
	}
	e := engines["mongodb"]
	run := func(host, pw string, stdin []byte, args ...string) string {
		var out bytes.Buffer
		exit, stderr, err := rt.RunOnce(ctx, clientSpec(e, "mongo:8.0", network, host, pw, args), bytes.NewReader(stdin), &out)
		if err != nil || exit != 0 {
			t.Fatalf("%v: exit %d %s %s %v", args, exit, out.String(), stderr, err)
		}
		return out.String()
	}
	pw1 := ensure("docs")
	run("docs", pw1, nil, mongoShell(`const s = db.getMongo().startSession(); s.startTransaction(); s.getDatabase("app").notes.insertOne({body: "salom"}); s.commitTransaction(); print("ok")`)...)
	run("docs", pw1, nil, mongoShell(`db.getSiblingDB("shop").orders.insertOne({item: "named in code"})`)...)
	dump := run("docs", pw1, nil, e.dump("docs")...)
	pw2 := ensure("docs-2")
	run("docs-2", pw2, []byte(dump), e.load("docs-2")...)
	got := run("docs-2", pw2, nil, mongoShell(`print(db.getSiblingDB("app").notes.findOne().body)`)...)
	if strings.TrimSpace(got) != "salom" {
		t.Fatalf("after the restore: %q", got)
	}
	if got := run("docs-2", pw2, nil, mongoShell(`print(db.getSiblingDB("shop").orders.findOne().item)`)...); strings.TrimSpace(got) != "named in code" {
		t.Fatalf("a database the app named itself: %q", got)
	}
	if member := run("docs-2", pw2, nil, mongoShell(`print(rs.conf().members[0].host)`)...); strings.TrimSpace(member) != "docs-2:27017" {
		t.Fatalf("the restored replica set's member: %q", member)
	}
}
