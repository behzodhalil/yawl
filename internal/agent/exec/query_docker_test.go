//go:build docker

package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/moby/moby/client"
)

func TestQueryReal(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	for _, k := range []struct {
		kind    string
		version int
		insert  string
		big     string
		blob    string
	}{
		{"postgres", 17, `INSERT INTO notes VALUES (1, E'a,"b"\nc'), (2, ''), (3, NULL)`, "SELECT generate_series(1, 5000) AS n", ""},
		{"mysql", 8, `INSERT INTO notes VALUES (1, 'a,"b"\nc'), (2, ''), (3, NULL)`, "SELECT 1 AS n FROM information_schema.columns a, information_schema.columns b LIMIT 5000", "SELECT x'0001ff' AS b"},
		{"mariadb", 11, `INSERT INTO notes VALUES (1, 'a,"b"\nc'), (2, ''), (3, NULL)`, "SELECT 1 AS n FROM information_schema.columns a, information_schema.columns b LIMIT 5000", "SELECT x'0001ff' AS b"},
	} {
		t.Run(k.kind, func(t *testing.T) {
			project := fmt.Sprintf("q%d", time.Now().UnixNano()%1_000_000)
			a, _, _ := ensureArgs(t, "")
			a.Kind, a.Project, a.Name, a.Version = k.kind, project, "shop-db", k.version
			container, volume, network, _ := dbNames(k.kind, project, "production", "shop-db", k.version)
			t.Cleanup(func() {
				rt.Remove(ctx, container)
				rt.C.VolumeRemove(ctx, volume, client.VolumeRemoveOptions{Force: true})
				rt.C.NetworkRemove(ctx, network, client.NetworkRemoveOptions{})
			})
			res := d.Handle(ctx, cmd(t, proto.CmdEnsureDatabase, a))
			if !res.OK {
				t.Fatalf("ensure: %+v", res)
			}
			var data proto.EnsureDatabaseData
			json.Unmarshal(res.Data, &data)
			run := func(sql string, write bool) proto.Result {
				return d.Handle(ctx, cmd(t, proto.CmdQuery, proto.QueryArgs{Kind: k.kind, Project: project, Env: "production", Name: "shop-db",
					Version: k.version, Password: data.PasswordBox, SQL: sql, Write: write}))
			}
			if res := run("CREATE TABLE notes (id int, body text)", false); res.OK || !strings.Contains(strings.ToLower(res.Cause), "read") {
				t.Fatalf("read mode made a table: %+v", res)
			}
			q := queryData(t, run("CREATE TABLE notes (id int, body text); "+k.insert, true))
			if q.Changed == nil || *q.Changed != 3 {
				t.Fatalf("write: %+v", q)
			}
			if res := run("DELETE FROM notes", false); res.OK {
				t.Fatalf("read mode deleted: %+v", res)
			}
			refused := []string{"SET SESSION transaction_read_only=OFF; DELETE FROM notes", "START TRANSACTION READ WRITE; DELETE FROM notes; COMMIT"}
			denied, escapes := "denied", []string{`\! `, `system `}
			if k.kind == "postgres" {
				refused = []string{"SET transaction_read_only=off; DELETE FROM notes", "BEGIN READ WRITE; CREATE TABLE t(x int); COMMIT",
					"SET transaction_read_only=off; CREATE TEMP TABLE o(l text); COPY o FROM PROGRAM 'id'; SELECT l FROM o", "SET ROLE app; DELETE FROM notes"}
				denied, escapes = "permission denied", []string{`\! `, `\o |`}
			}
			for _, sql := range refused {
				if res := run(sql, false); res.OK || !strings.Contains(res.Cause, denied) {
					t.Fatalf("%s: %+v", sql, res)
				}
			}
			pw, _ := d.open(proto.SealedVar{Key: "PASSWORD", Box: data.PasswordBox})
			read := readPassword(pw)
			for _, esc := range escapes {
				shell := esc + `env; psql -X -h shop-db -U app -d app -c 'CREATE TABLE pwned(x int)' 2>&1 | tr -d '",'`
				if k.kind != "postgres" {
					my := k.kind + ` -h shop-db -e 'CREATE TABLE pwned (x int)' app 2>&1 -u `
					shell = esc + `(echo ERROR && env && ` + my + `app || ` + my + `root) >&2` + "\nSELECT nope"
				}
				res := run(shell, false)
				seen := string(res.Data) + res.Cause
				if strings.Contains(seen, string(pw)) || !strings.Contains(seen, "PASSWORD="+read) && !strings.Contains(seen, "MYSQL_PWD="+read) ||
					!strings.Contains(seen, "authentication failed for user app") && !strings.Contains(seen, "Access denied for user 'app'") {
					t.Fatalf("%s: %s", esc, seen)
				}
				if k.kind != "postgres" && !strings.Contains(seen, "Access denied for user 'root'") {
					t.Fatalf("%s: root: %s", esc, seen)
				}
			}
			if q := queryData(t, run("SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'pwned'", true)); *q.Rows[0][0] != "0" {
				t.Fatal("the shell made a table as app")
			}
			var wg sync.WaitGroup
			failed := make(chan proto.Result, 8)
			for range 8 {
				wg.Go(func() {
					if res := run("SELECT 1", false); !res.OK {
						failed <- res
					}
				})
			}
			wg.Wait()
			close(failed)
			for res := range failed {
				t.Fatalf("parallel read: %+v", res)
			}
			q = queryData(t, run("SELECT id, body FROM notes ORDER BY id", false))
			if fmt.Sprint(q.Columns) != "[id body]" || fmt.Sprint(cells(q.Rows)) != fmt.Sprint([][]string{{"1", "a,\"b\"\nc"}, {"2", ""}, {"3", "<nil>"}}) {
				t.Fatalf("read: %+v %q", q.Columns, cells(q.Rows))
			}
			if q = queryData(t, run(k.big, false)); len(q.Rows) != 1000 || !q.More {
				t.Fatalf("big: %d rows, more %v", len(q.Rows), q.More)
			}
			if k.blob != "" {
				if q = queryData(t, run(k.blob, false)); len(q.Rows) != 1 || !strings.EqualFold(*q.Rows[0][0], "0x0001FF") {
					t.Fatalf("blob: %q", cells(q.Rows))
				}
			}
		})
	}
}
