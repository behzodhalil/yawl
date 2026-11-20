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

func TestClickHouseDumpLoadReal(t *testing.T) {
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
		a, _, _ := ensureArgs(t, "")
		a.Kind, a.Project, a.Name, a.Version = "clickhouse", project, name, 25
		container, volume, _, _ := dbNames("clickhouse", project, "production", name, 25)
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
	e := engines["clickhouse"]
	run := func(host, pw string, stdin []byte, args ...string) string {
		var out bytes.Buffer
		exit, stderr, err := rt.RunOnce(ctx, clientSpec(e, "clickhouse/clickhouse-server:25.8", network, host, pw, args), bytes.NewReader(stdin), &out)
		if err != nil || exit != 0 {
			t.Fatalf("%.80v: exit %d %s %v", args, exit, stderr, err)
		}
		return out.String()
	}
	sql := func(host, pw, q string) string {
		return strings.TrimSpace(run(host, pw, nil, "clickhouse-client", "--host", host, "--user", "app", "--database", "app", "--multiquery", "--query", q))
	}
	pw1 := ensure("events")
	sql("events", pw1, `CREATE TABLE hits (at DateTime, path String) ENGINE = MergeTree ORDER BY at;
		CREATE TABLE "odd name" (n UInt8) ENGINE = Log;
		CREATE TABLE per_path (path String, n UInt64) ENGINE = SummingMergeTree ORDER BY path;
		CREATE MATERIALIZED VIEW per_path_mv TO per_path AS SELECT path, count() AS n FROM hits GROUP BY path;
		CREATE VIEW recent AS SELECT * FROM hits WHERE at > now() - INTERVAL 1 YEAR;
		CREATE VIEW recent_paths AS SELECT DISTINCT path FROM recent;
		INSERT INTO hits VALUES (now(), '/'), (now(), '/'), (now(), '/api');
		INSERT INTO "odd name" VALUES (7)`)
	dump := run("events", pw1, nil, e.dump("events")...)
	pw2 := ensure("events-2")
	run("events-2", pw2, []byte(dump), e.load("events-2")...)
	for q, want := range map[string]string{
		"SELECT count() FROM hits":                                 "3",
		`SELECT n FROM "odd name"`:                                 "7",
		"SELECT sum(n) FROM per_path":                              "3",
		"SELECT count() FROM recent_paths":                         "2",
		"SELECT count() FROM system.tables WHERE database = 'app'": "6",
	} {
		if got := sql("events-2", pw2, q); got != want {
			t.Errorf("%s: %q, want %s", q, got, want)
		}
	}
}
