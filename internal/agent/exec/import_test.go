package exec

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func gz(s string) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write([]byte(s))
	w.Close()
	return b.Bytes()
}

func importer(t *testing.T) (*Dispatcher, *runtime.Fake, func(string) string) {
	rt := &runtime.Fake{}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	kr, seal := keyring(t)
	d.Keys = kr
	d.ImportDir = t.TempDir()
	return d, rt, seal
}

func TestImportPostgres(t *testing.T) {
	d, rt, seal := importer(t)
	os.WriteFile(filepath.Join(d.ImportDir, "old.sql.gz"), gz("CREATE TABLE t (id int);\n"), 0o600)
	a := proto.ImportArgs{Kind: "postgres", Project: "acme", Env: "production", Name: "db", Version: 17, Password: seal("pw"), Path: "old.sql.gz"}
	if res := d.Handle(context.Background(), cmd(t, proto.CmdImport, a)); !res.OK {
		t.Fatalf("%+v", res)
	}
	r := rt.Ran[0]
	if string(r.Stdin) != "CREATE TABLE t (id int);\n" || !strings.Contains(strings.Join(r.Spec.Cmd, " "), "psql -v ON_ERROR_STOP=1") ||
		r.Spec.Image != "pgvector/pgvector:pg17-trixie" || r.Spec.Network != "yawl-acme-production" || !slicesHas(r.Spec.Env, "PGPASSWORD=pw") {
		t.Fatalf("%+v", r)
	}
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("PGDMP\x01\x0e\x00rest")) }))
	defer src.Close()
	a.Path, a.URL = "", src.URL+"/dump?X-Amz-Signature=secret"
	if res := d.Handle(context.Background(), cmd(t, proto.CmdImport, a)); !res.OK {
		t.Fatalf("%+v", res)
	}
	if r := rt.Ran[1]; !strings.HasPrefix(strings.Join(r.Spec.Cmd, " "), "pg_restore --no-owner --no-privileges") || !bytes.HasPrefix(r.Stdin, []byte("PGDMP")) {
		t.Fatalf("%+v", r.Spec.Cmd)
	}
}

func TestImportOtherEngines(t *testing.T) {
	d, rt, seal := importer(t)
	os.WriteFile(filepath.Join(d.ImportDir, "x"), []byte("data"), 0o600)
	for kind, want := range map[string]string{"mysql": "mysql -h db -u app app", "mongodb": `mongorestore --archive --drop --nsExclude 'admin.*' --nsExclude 'config.*' --nsFrom '$db$.$coll$' --nsTo 'app.$coll$' --config`} {
		res := d.Handle(context.Background(), cmd(t, proto.CmdImport, proto.ImportArgs{Kind: kind, Project: "acme", Env: "production", Name: "db", Password: seal("pw"), Path: "x"}))
		if !res.OK || !strings.Contains(strings.Join(rt.Ran[len(rt.Ran)-1].Spec.Cmd, " "), want) {
			t.Fatalf("%s: %+v", kind, res)
		}
	}
	if res := d.Handle(context.Background(), cmd(t, proto.CmdImport, proto.ImportArgs{Kind: "valkey", Name: "cache", Password: seal("pw"), Path: "x"})); res.OK || !strings.Contains(res.Cause, "Valkey") {
		t.Fatalf("%+v", res)
	}
}

func TestImportRefusals(t *testing.T) {
	d, rt, seal := importer(t)
	base := proto.ImportArgs{Kind: "postgres", Project: "acme", Env: "production", Name: "db", Version: 17, Password: seal("pw")}
	for _, p := range []string{"../etc/passwd", "/etc/passwd", "a/b", ""} {
		a := base
		a.Path = p
		if res := d.Handle(context.Background(), cmd(t, proto.CmdImport, a)); res.OK || len(rt.Ran) != 0 {
			t.Fatalf("%q: %+v", p, res)
		}
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	defer gone.Close()
	a := base
	a.URL = gone.URL + "/d?token=hush"
	if res := d.Handle(context.Background(), cmd(t, proto.CmdImport, a)); res.OK || !strings.Contains(res.Cause, "404") || strings.Contains(res.Cause, "hush") {
		t.Fatalf("%+v", res)
	}
	os.WriteFile(filepath.Join(d.ImportDir, "bad.sql"), []byte("NOT SQL"), 0o600)
	rt.OnRun = func(runtime.ContainerSpec, []byte) (string, int, string, error) {
		return "", 3, "ERROR:  syntax error at or near \"NOT\"", nil
	}
	a = base
	a.Path = "bad.sql"
	if res := d.Handle(context.Background(), cmd(t, proto.CmdImport, a)); res.OK || !strings.Contains(res.Cause, "syntax error") {
		t.Fatalf("%+v", res)
	}
}

func slicesHas(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestImportCutEmptyAndHints(t *testing.T) {
	d, rt, seal := importer(t)
	cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.Write([]byte("CREATE TABLE t (id int);\n"))
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	defer cut.Close()
	base := proto.ImportArgs{Kind: "postgres", Project: "acme", Env: "production", Name: "db", Version: 17, Password: seal("pw")}
	a := base
	a.URL = cut.URL + "/d.sql"
	if res := d.Handle(context.Background(), cmd(t, proto.CmdImport, a)); res.OK || !strings.Contains(res.Cause, "cut off") {
		t.Fatalf("%+v", res)
	}
	if c := strings.Join(rt.Ran[0].Spec.Cmd, " "); !strings.Contains(c, "--single-transaction") {
		t.Fatalf("%s", c)
	}
	os.WriteFile(filepath.Join(d.ImportDir, "empty.sql"), nil, 0o600)
	a = base
	a.Path = "empty.sql"
	if res := d.Handle(context.Background(), cmd(t, proto.CmdImport, a)); res.OK || !strings.Contains(res.Cause, "empty") {
		t.Fatalf("%+v", res)
	}
	os.WriteFile(filepath.Join(d.ImportDir, "x.sql"), []byte("ALTER TABLE t OWNER TO u8abc;"), 0o600)
	for kind, stderr := range map[string]string{"postgres": `ERROR:  role "u8abc" does not exist`, "mysql": "ERROR 1227 (42000) at line 18: Access denied; you need (at least one of) the SUPER privilege(s)"} {
		rt.OnRun = func(runtime.ContainerSpec, []byte) (string, int, string, error) { return "", 1, stderr, nil }
		a = base
		a.Kind, a.Path = kind, "x.sql"
		res := d.Handle(context.Background(), cmd(t, proto.CmdImport, a))
		want := map[string]string{"postgres": "pg_dump --no-owner --no-acl", "mysql": "--set-gtid-purged=OFF"}[kind]
		if res.OK || !strings.Contains(res.Cause, want) {
			t.Fatalf("%s: %+v", kind, res)
		}
	}
}

func TestImportRedirectAndOracle(t *testing.T) {
	d, rt, seal := importer(t)
	noLoopback(t)
	for _, u := range []string{"http://169.254.169.254/latest/user-data", "http://127.0.0.1:1/d.sql"} {
		a := proto.ImportArgs{Kind: "postgres", Project: "acme", Env: "production", Name: "db", Version: 17, Password: seal("pw"), URL: u}
		if res := d.Handle(context.Background(), cmd(t, proto.CmdImport, a)); res.OK || !strings.Contains(res.Cause, "Yawl does not fetch from it") || len(rt.Ran) != 0 {
			t.Fatalf("%s dialed: %+v", u, res)
		}
	}
	loopbackOK = true
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("internal-only-line;\n"))
	}))
	defer target.Close()
	hop := httptest.NewServer(http.RedirectHandler(target.URL+"/secret", http.StatusFound))
	defer hop.Close()
	a := proto.ImportArgs{Kind: "postgres", Project: "acme", Env: "production", Name: "db", Version: 17, Password: seal("pw"), URL: hop.URL + "/d.sql"}
	res := d.Handle(context.Background(), cmd(t, proto.CmdImport, a))
	if !res.OK || string(rt.Ran[0].Stdin) != "internal-only-line;\n" || strings.Contains(res.Output, "internal-only") {
		t.Fatalf("redirect not followed blind: %+v stdin=%q", res, rt.Ran[0].Stdin)
	}
	rt.OnRun = func(_ runtime.ContainerSpec, in []byte) (string, int, string, error) {
		return "", 3, "ERROR:  syntax error at or near \"internal\"\nLINE 1: " + strings.TrimSpace(string(in)), nil
	}
	if res := d.Handle(context.Background(), cmd(t, proto.CmdImport, a)); res.OK || !strings.Contains(res.Cause, "LINE 1: internal-only-line;") {
		t.Fatalf("stderr not passed through: %+v", res)
	}
}
