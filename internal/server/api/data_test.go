package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type querySender struct {
	mu  sync.Mutex
	got []proto.QueryArgs
	res proto.Result
}

func (q *querySender) Send(_ context.Context, _ string, c proto.Command) (proto.Result, error) {
	var a proto.QueryArgs
	json.Unmarshal(c.Args, &a)
	q.mu.Lock()
	defer q.mu.Unlock()
	q.got = append(q.got, a)
	return q.res, nil
}

func (q *querySender) last() proto.QueryArgs {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.got[len(q.got)-1]
}

func answer(d proto.QueryData) proto.Result {
	b, _ := json.Marshal(d)
	return proto.Result{OK: true, Data: b}
}

func str(s string) *string { return &s }

func dataPanel(t *testing.T) (*httptest.Server, string, *querySender) {
	t.Helper()
	_, tok, a := setupApp(t)
	gw := &querySender{}
	srv := httptest.NewServer(New(Deps{App: a, GW: gw}))
	t.Cleanup(srv.Close)
	call(t, srv, tok, "POST", "/api/apply", "project: acme\nenvironment: production\nserver: hel1\nservices:\n"+
		"  - {name: db, kind: postgres}\n  - {name: my, kind: mysql}\n  - {name: cache, kind: valkey}\n  - {name: api, kind: web, repo: \"https://x/y\"}\n")
	for _, name := range []string{"db", "my", "cache"} {
		svc, err := a.St.Q.GetServiceByName(t.Context(), db.GetServiceByNameParams{Project: "acme", Env: "production", Service: name})
		if err != nil {
			t.Fatal(err)
		}
		a.St.Q.UpsertEnvVar(t.Context(), db.UpsertEnvVarParams{ID: "var_" + name, EnvironmentID: svc.EnvironmentID, ServiceID: svc.ID, Key: "PASSWORD", AgentBox: "box-" + name, RecoveryBox: "r"})
	}
	return srv, tok, gw
}

func auditTexts(t *testing.T, pattern string) []string {
	t.Helper()
	rows, err := testDB.Query(`SELECT body_json FROM events WHERE kind = 'audit' AND subject_id = ? ORDER BY created_at, rowid`, pattern)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		rows.Scan(&b)
		var v struct{ Text string }
		json.Unmarshal([]byte(b), &v)
		out = append(out, v.Text)
	}
	return out
}

const dq = "/api/services/acme/production/"

func TestAPIDataQuery(t *testing.T) {
	srv, tok, gw := dataPanel(t)
	gw.res = answer(proto.QueryData{Columns: []string{"email"}, Rows: [][]*string{{str("a@acme.uz")}, {nil}}})
	res, b := call(t, srv, tok, "POST", dq+"db/data/query", `{"sql":"select email from users"}`)
	if res.StatusCode != 200 || string(b) != `{"columns":["email"],"rows":[["a@acme.uz"],[null]]}` {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if g := gw.last(); g.Kind != "postgres" || g.Project != "acme" || g.Env != "production" || g.Name != "db" || g.Version != 17 ||
		g.Password != "box-db" || g.SQL != "select email from users" || g.Write {
		t.Fatalf("%+v", g)
	}
	if got := auditTexts(t, "POST /api/services/{project}/{env}/{name}/data/query"); len(got) != 1 || got[0] != "ran SQL on acme/production/db: select email from users" {
		t.Fatalf("audit %q", got)
	}
	var n int
	testDB.QueryRow(`SELECT count(*) FROM events WHERE body_json LIKE '%a@acme.uz%'`).Scan(&n)
	if n != 0 {
		t.Fatal("a result reached the events table")
	}
	gw.res = proto.Result{Cause: `ERROR:  relation "users" does not exist`}
	if res, b := call(t, srv, tok, "POST", dq+"db/data/query", `{"sql":"select 1 from users"}`); res.StatusCode != 422 || strings.TrimSpace(string(b)) != gw.res.Cause {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	gw.res = proto.Result{Cause: "unknown command Query"}
	if res, b := call(t, srv, tok, "POST", dq+"db/data/query", `{"sql":"select 1"}`); res.StatusCode != 502 || !strings.Contains(string(b), "The agent on hel1 is older than the panel") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if got := auditTexts(t, "POST /api/services/{project}/{env}/{name}/data/query"); len(got) != 3 {
		t.Fatalf("audit %q", got)
	}
}

func TestAPIDataRefusals(t *testing.T) {
	srv, tok, gw := dataPanel(t)
	for path, want := range map[string]string{
		"api/data/query":   "not a database",
		"cache/data/query": "does not browse Valkey",
		"nope/data/query":  "no such service",
	} {
		if res, b := call(t, srv, tok, "POST", dq+path, `{"sql":"select 1"}`); res.StatusCode < 400 || !strings.Contains(string(b), want) {
			t.Fatalf("%s: %d %s", path, res.StatusCode, b)
		}
	}
	for body, want := range map[string]string{`{"sql":"  "}`: "Write some SQL", `{"sql":"` + strings.Repeat("x", 16<<10+1) + `"}`: "at most 16 KB"} {
		if res, b := call(t, srv, tok, "POST", dq+"db/data/query", body); res.StatusCode != 400 || !strings.Contains(string(b), want) {
			t.Fatalf("%d %s", res.StatusCode, b)
		}
	}
	testDB.Exec(`DELETE FROM env_vars WHERE id = 'var_db'`)
	if res, b := call(t, srv, tok, "POST", dq+"db/data/query", `{"sql":"select 1"}`); res.StatusCode != 409 || !strings.Contains(string(b), "Deploy db first") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if len(gw.got) != 0 {
		t.Fatalf("sent %+v", gw.got)
	}
}

func TestAPIDataTablesAndRows(t *testing.T) {
	srv, tok, gw := dataPanel(t)
	gw.res = answer(proto.QueryData{Columns: []string{"table_schema", "table_name"}, Rows: [][]*string{{str("public"), str("users")}, {str("audit"), str("events")}}})
	res, b := call(t, srv, tok, "POST", dq+"db/data/tables", "")
	if res.StatusCode != 200 || string(b) != `{"tables":[{"schema":"public","name":"users"},{"schema":"audit","name":"events"}]}`+"\n" {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
	if g := gw.last(); !strings.Contains(g.SQL, "information_schema.tables") || g.Write {
		t.Fatalf("%+v", g)
	}
	rows := make([][]*string, 101)
	for i := range rows {
		rows[i] = []*string{str("x")}
	}
	gw.res = answer(proto.QueryData{Columns: []string{"id"}, Rows: rows})
	res, b = call(t, srv, tok, "POST", dq+"db/data/rows", `{"schema":"public","table":"us\"ers","page":2}`)
	var out proto.QueryData
	if json.Unmarshal(b, &out); res.StatusCode != 200 || len(out.Rows) != 100 || !out.More {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if g := gw.last(); g.SQL != `SELECT * FROM "public"."us""ers" LIMIT 101 OFFSET 200` || g.Write {
		t.Fatalf("%+v", g)
	}
	call(t, srv, tok, "POST", dq+"my/data/rows", "{\"table\":\"or`ders\"}")
	if g := gw.last(); g.Kind != "mysql" || g.SQL != "SELECT * FROM `or``ders` LIMIT 101 OFFSET 0" {
		t.Fatalf("%+v", g)
	}
	if got := auditTexts(t, "POST /api/services/{project}/{env}/{name}/data/rows"); len(got) != 2 || got[0] != `read table public.us"ers of acme/production/db` {
		t.Fatalf("audit %q", got)
	}
	if got := auditTexts(t, "POST /api/services/{project}/{env}/{name}/data/tables"); len(got) != 0 {
		t.Fatalf("the table list is logged: %q", got)
	}
	if res, _ := call(t, srv, tok, "POST", dq+"db/data/rows", `{"table":"t","page":-1}`); res.StatusCode != 400 {
		t.Fatalf("page -1: %d", res.StatusCode)
	}
}

func TestAPIDataRoles(t *testing.T) {
	srv, tok, gw := dataPanel(t)
	gw.res = answer(proto.QueryData{Changed: new(int64(2))})
	res, b := call(t, srv, tok, "POST", dq+"db/data/write", `{"sql":"update users set a = 1"}`)
	if res.StatusCode != 200 || string(b) != `{"columns":null,"rows":null,"changed":2}` || !gw.last().Write {
		t.Fatalf("%d %s %+v", res.StatusCode, b, gw.last())
	}
	if got := auditTexts(t, "POST /api/services/{project}/{env}/{name}/data/write"); len(got) != 1 || got[0] != "ran SQL with writes on acme/production/db: update users set a = 1" {
		t.Fatalf("audit %q", got)
	}
	dev := memberToken(t, srv, tok, "dev@acme.uz", "developer")
	if res, b := call(t, srv, dev, "POST", dq+"db/data/write", `{"sql":"delete from users"}`); res.StatusCode != 403 || !strings.Contains(string(b), "Developers cannot write to a database") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	gw.res = answer(proto.QueryData{Columns: []string{"n"}, Rows: [][]*string{}})
	for _, p := range []string{"data/query", "data/tables", "data/rows"} {
		if res, b := call(t, srv, dev, "POST", dq+"db/"+p, `{"sql":"select 1","table":"t"}`); res.StatusCode != 200 {
			t.Fatalf("a Developer, %s: %d %s", p, res.StatusCode, b)
		}
	}
	viewer := memberToken(t, srv, tok, "v@acme.uz", "viewer")
	for _, p := range []string{"data/query", "data/tables", "data/rows", "data/write"} {
		if res, _ := call(t, srv, viewer, "POST", dq+"db/"+p, `{"sql":"select 1","table":"t"}`); res.StatusCode != 403 {
			t.Fatalf("a Viewer, %s: %d", p, res.StatusCode)
		}
	}
}

func TestAuditDataSQLAdminOnly(t *testing.T) {
	srv, tok, gw := dataPanel(t)
	gw.res = answer(proto.QueryData{Changed: new(int64(1))})
	if res, b := call(t, srv, tok, "POST", dq+"db/data/write", `{"sql":"alter user app password 'pin-secret-123'"}`); res.StatusCode != 200 {
		t.Fatalf("write %d %s", res.StatusCode, b)
	}
	if _, b := call(t, srv, tok, "GET", "/api/audit?q=pin-secret", ""); len(auditRows(t, b)) != 1 {
		t.Fatalf("the Admin lost the line: %s", b)
	}
	for _, role := range []string{"viewer", "developer"} {
		other := memberToken(t, srv, tok, role+"@acme.uz", role)
		for _, path := range []string{"/api/audit?q=pin-secret", "/api/audit"} {
			res, b := call(t, srv, other, "GET", path, "")
			if res.StatusCode != 200 || strings.Contains(string(b), "pin-secret") || strings.Contains(string(b), "ran SQL") {
				t.Fatalf("a %s, %s: %d %s", role, path, res.StatusCode, b)
			}
		}
	}
}
