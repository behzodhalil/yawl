package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

type readerShell struct {
	io.Reader
	typed strings.Builder
}

func (s *readerShell) Write(b []byte) (int, error)              { return s.typed.Write(b) }
func (s *readerShell) Close() error                             { return nil }
func (s *readerShell) Resize(context.Context, uint, uint) error { return nil }

func querier(t *testing.T, on func(s runtime.ContainerSpec, stdin []byte) (string, int, string, error)) (*Dispatcher, *runtime.Fake, func(string) string) {
	rt := &runtime.Fake{OnRun: on, OnExec: func(string, []string) (runtime.Shell, error) {
		return &readerShell{Reader: strings.NewReader("yawl-read-ok\r\n")}, nil
	}}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	kr, seal := keyring(t)
	d.Keys = kr
	return d, rt, seal
}

func queryData(t *testing.T, res proto.Result) proto.QueryData {
	t.Helper()
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	var q proto.QueryData
	if err := json.Unmarshal(res.Data, &q); err != nil {
		t.Fatal(err)
	}
	return q
}

func cells(rows [][]*string) [][]string {
	out := [][]string{}
	for _, r := range rows {
		var line []string
		for _, v := range r {
			if v == nil {
				line = append(line, "<nil>")
			} else {
				line = append(line, *v)
			}
		}
		out = append(out, line)
	}
	return out
}

func TestQueryPostgres(t *testing.T) {
	d, rt, seal := querier(t, func(runtime.ContainerSpec, []byte) (string, int, string, error) {
		return "a,b\n,1\n" + pgNull + ",2\n\"x,\"\"y\"\"\nz\",3\n", 0, "3\n", nil
	})
	a := proto.QueryArgs{Kind: "postgres", Project: "acme", Env: "production", Name: "db", Version: 17, Password: seal("pw"), SQL: "select * from t"}
	q := queryData(t, d.Handle(context.Background(), cmd(t, proto.CmdQuery, a)))
	if !slices.Equal(q.Columns, []string{"a", "b"}) || fmt.Sprint(cells(q.Rows)) != fmt.Sprint([][]string{{"", "1"}, {"<nil>", "2"}, {"x,\"y\"\nz", "3"}}) || q.More || q.Changed != nil {
		t.Fatalf("%+v %q", q, cells(q.Rows))
	}
	read := readPassword([]byte("pw"))
	if len(rt.Ran) != 2 || len(read) != 64 || strings.Contains(read, "pw") {
		t.Fatalf("%d runs, %q", len(rt.Ran), read)
	}
	if r := rt.Ran[0]; !slices.Equal(r.Spec.Env, []string{"PGPASSWORD=pw", "READ=" + read}) || r.Stdin != nil || r.Spec.Network != "yawl-acme-production" ||
		!strings.Contains(r.Spec.Cmd[2], "ALTER ROLE yawl_read") || strings.Contains(r.Spec.Cmd[2], "$sql") {
		t.Fatalf("reader: %+v", r.Spec)
	}
	r := rt.Ran[1]
	if string(r.Stdin) != "select * from t" || r.Spec.Image != "pgvector/pgvector:pg17-trixie" || r.Spec.Network != "yawl-acme-production" ||
		r.Spec.Memory != 256<<20 || !slices.Contains(r.Spec.Env, "PGPASSWORD="+read) || strings.Contains(fmt.Sprint(r.Spec.Env), "=pw") ||
		!slices.Contains(r.Spec.Env, "PGOPTIONS=-c statement_timeout=30000 -c default_transaction_read_only=on") {
		t.Fatalf("%+v", r.Spec)
	}
	if line := strings.Join(r.Spec.Cmd, " "); !strings.Contains(line, "psql -X -q --csv") || strings.Contains(line, "pw") || strings.Contains(line, "ALTER ROLE") || !strings.HasSuffix(line, " yawl_read") {
		t.Fatalf("%s", line)
	}
}

func TestQueryPostgresWrite(t *testing.T) {
	d, rt, seal := querier(t, func(runtime.ContainerSpec, []byte) (string, int, string, error) {
		return "", 0, "NOTICE:  hi\n2\n", nil
	})
	a := proto.QueryArgs{Name: "db", Project: "acme", Env: "production", Version: 16, Password: seal("pw"), SQL: "update t set a = 1", Write: true}
	q := queryData(t, d.Handle(context.Background(), cmd(t, proto.CmdQuery, a)))
	if q.Changed == nil || *q.Changed != 2 || q.Columns != nil {
		t.Fatalf("%+v", q)
	}
	if c := rt.Ran[0].Spec.Cmd; len(rt.Ran) != 1 || !slices.Equal(rt.Ran[0].Spec.Env, []string{"PGPASSWORD=pw", "PGOPTIONS=-c statement_timeout=30000"}) || c[len(c)-1] != "app" || strings.Contains(c[2], "yawl_read") {
		t.Fatalf("%+v", rt.Ran[0].Spec)
	}
}

const myXML = `<?xml version="1.0"?>

<resultset statement="select * from t" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <row>
	<field name="a"></field>
	<field name="b">1</field>
  </row>

  <row>
	<field name="a" xsi:nil="true" />
	<field name="b">x&lt;y&gt;&amp;&quot;
z</field>
  </row>
</resultset>
`

func trailer(n int) string {
	return fmt.Sprintf(`<?xml version="1.0"?>

<resultset statement="SELECT ROW_COUNT() AS yawl_changed
" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <row>
	<field name="yawl_changed">%d</field>
  </row>
</resultset>
`, n)
}

func TestQueryMySQL(t *testing.T) {
	out := myXML + trailer(-1)
	d, rt, seal := querier(t, func(runtime.ContainerSpec, []byte) (string, int, string, error) { return out, 0, "", nil })
	read := readPassword([]byte("pw"))
	var sh *readerShell
	rt.OnExec = func(container string, c []string) (runtime.Shell, error) {
		if container != "yawl-acme-production-db" || !slices.Equal(c[3:], []string{"sh", "mysql", "MYSQL_ROOT_PASSWORD"}) || !strings.Contains(c[2], "GRANT SELECT, SHOW VIEW ON app.* TO yawl_read") {
			t.Fatalf("%s %q", container, c)
		}
		sh = &readerShell{Reader: strings.NewReader(read + "\r\nyawl-read-ok\r\n")}
		return sh, nil
	}
	a := proto.QueryArgs{Kind: "mysql", Project: "acme", Env: "production", Name: "db", Version: 8, Password: seal("pw"), SQL: "select * from t"}
	q := queryData(t, d.Handle(context.Background(), cmd(t, proto.CmdQuery, a)))
	if !slices.Equal(q.Columns, []string{"a", "b"}) || fmt.Sprint(cells(q.Rows)) != fmt.Sprint([][]string{{"", "1"}, {"<nil>", "x<y>&\"\nz"}}) || q.Changed != nil {
		t.Fatalf("%+v %q", q, cells(q.Rows))
	}
	if sh == nil || sh.typed.String() != read+"\n" {
		t.Fatalf("typed: %+v", sh)
	}
	r := rt.Ran[0]
	if string(r.Stdin) != "select * from t\n;\nSELECT ROW_COUNT() AS yawl_changed" || !slices.Equal(r.Spec.Env, []string{"MYSQL_PWD=" + read}) || r.Spec.Image != "mysql:8.4" ||
		!slices.Equal(r.Spec.Cmd, []string{"mysql", "--xml", "--quick", "--binary-as-hex", "--init-command=SET SESSION max_execution_time=30000, SESSION transaction_read_only=ON", "-h", "db", "-u", "yawl_read", "app"}) {
		t.Fatalf("%+v %q", r.Spec, r.Stdin)
	}
	rt.OnExec = func(string, []string) (runtime.Shell, error) {
		return &readerShell{Reader: strings.NewReader(read + "\r\nERROR 1045 (28000): Access denied for user 'root'@'localhost'\r\n")}, nil
	}
	if res := d.Handle(context.Background(), cmd(t, proto.CmdQuery, a)); res.OK || len(rt.Ran) != 1 ||
		res.Cause != "Yawl could not ready its read-only user: ERROR 1045 (28000): Access denied for user 'root'@'localhost'" {
		t.Fatalf("%+v", res)
	}
	out = myXML + trailer(4)
	a.Kind, a.Version, a.Write = "mariadb", 11, true
	q = queryData(t, d.Handle(context.Background(), cmd(t, proto.CmdQuery, a)))
	if q.Changed == nil || *q.Changed != 4 || q.Columns != nil {
		t.Fatalf("%+v", q)
	}
	if r := rt.Ran[1]; !slices.Equal(r.Spec.Cmd[:5], []string{"mariadb", "--xml", "--quick", "--binary-as-hex", "--init-command=SET SESSION max_statement_time=30"}) ||
		!slices.Equal(r.Spec.Cmd[7:], []string{"-u", "app", "app"}) || !slices.Equal(r.Spec.Env, []string{"MYSQL_PWD=pw"}) || len(rt.Execs) != 2 {
		t.Fatalf("%+v %v", r.Spec, rt.Execs)
	}
	out = `<?xml version="1.0"?>
<resultset statement="select * from t where false" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"></resultset>
` + trailer(-1)
	if q = queryData(t, d.Handle(context.Background(), cmd(t, proto.CmdQuery, a))); len(q.Rows) != 0 || q.Changed != nil {
		t.Fatalf("no rows: %+v", q)
	}
}

func TestQueryErrors(t *testing.T) {
	stderr := "ERROR:  syntax error at or near \"selec\"\nLINE 1: selec 1\n        ^\n"
	d, _, seal := querier(t, func(s runtime.ContainerSpec, _ []byte) (string, int, string, error) {
		if !slices.Contains(s.Cmd, "yawl_read") {
			return "", 0, "", nil
		}
		return "", 1, stderr, nil
	})
	a := proto.QueryArgs{Name: "db", Password: seal("pw"), SQL: "selec 1"}
	if res := d.Handle(context.Background(), cmd(t, proto.CmdQuery, a)); res.OK || res.Cause != strings.TrimSpace(stderr) {
		t.Fatalf("%+v", res)
	}
	stderr = "--------------\nselec 1\n--------------\n\nERROR 1064 (42000) at line 1: You have an error in your SQL syntax\n"
	a.Kind = "mariadb"
	if res := d.Handle(context.Background(), cmd(t, proto.CmdQuery, a)); res.OK || res.Cause != "ERROR 1064 (42000) at line 1: You have an error in your SQL syntax" {
		t.Fatalf("%+v", res)
	}
	for _, kind := range []string{"mongodb", "valkey", "clickhouse"} {
		a.Kind = kind
		if res := d.Handle(context.Background(), cmd(t, proto.CmdQuery, a)); res.OK || !strings.Contains(res.Cause, "does not browse") {
			t.Fatalf("%s: %+v", kind, res)
		}
	}
	if !proto.Unrecorded(proto.CmdQuery) {
		t.Fatal("Query is recorded")
	}
}

func TestQueryLimits(t *testing.T) {
	var b strings.Builder
	b.WriteString("n\n")
	for i := range 1500 {
		fmt.Fprintf(&b, "%d\n", i)
	}
	out := b.String()
	d, _, seal := querier(t, func(runtime.ContainerSpec, []byte) (string, int, string, error) { return out, 0, "1500\n", nil })
	a := proto.QueryArgs{Name: "db", Password: seal("pw"), SQL: "select n"}
	q := queryData(t, d.Handle(context.Background(), cmd(t, proto.CmdQuery, a)))
	if len(q.Rows) != 1000 || !q.More || *q.Rows[999][0] != "999" {
		t.Fatalf("%d %v", len(q.Rows), q.More)
	}
	was := queryBytes
	defer func() { queryBytes = was }()
	queryBytes = 100
	q = queryData(t, d.Handle(context.Background(), cmd(t, proto.CmdQuery, a)))
	if !q.More || len(q.Rows) != 35 {
		t.Fatalf("cut: %d %v", len(q.Rows), q.More)
	}
	for i, r := range q.Rows {
		if *r[0] != fmt.Sprint(i) {
			t.Fatalf("row %d is %s", i, *r[0])
		}
	}
	queryBytes = was
	wasJSON := queryJSON
	defer func() { queryJSON = wasJSON }()
	queryJSON = 1000
	out = "v\n" + strings.Repeat(strings.Repeat("\x01", 20)+"\n", 100)
	res := d.Handle(context.Background(), cmd(t, proto.CmdQuery, a))
	if q = queryData(t, res); len(res.Data) > 1000 || !q.More || len(q.Rows) == 0 {
		t.Fatalf("%d bytes, %d rows", len(res.Data), len(q.Rows))
	}
}
