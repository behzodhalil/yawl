package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/seal"
)

func dbCLI(t *testing.T, final string) *bytes.Buffer {
	t.Helper()
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/services/acme/production/acme-db/backups":
			io.WriteString(w, `{"id":"bak_1"}`)
		case r.Method == "GET" && r.URL.Path == "/api/services/acme/production/acme-db/backups":
			if polls.Add(1) < 3 {
				io.WriteString(w, `[{"id":"bak_1","state":"running"}]`)
				return
			}
			io.WriteString(w, final)
		case r.URL.Path == "/api/services/acme/production/acme-db/pitr":
			io.WriteString(w, `{"pitr":true,"from":1792724700000,"to":1793374680000,"shipped":1793374860000}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	t.Setenv("YAWL_CONFIG", filepath.Join(dir, "config.json"))
	out := &bytes.Buffer{}
	stdout, stdin = out, strings.NewReader("")
	Run(context.Background(), "login", []string{srv.URL, "--token", "tok"})
	out.Reset()
	os.WriteFile(filepath.Join(dir, "yawl.yaml"), []byte("project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: acme-db, kind: postgres}\n"), 0o644)
	t.Chdir(dir)
	pollEvery = 10 * time.Millisecond
	return out
}

func TestCLIDbBackup(t *testing.T) {
	out := dbCLI(t, `[{"id":"bak_1","state":"ok","size":1234,"created_at":1792900800000}]`)
	if err := Run(context.Background(), "db", []string{"backup", "acme-db"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), "Backup bak_1 ok, 1.2 KB.\n") {
		t.Fatalf("out %q", out)
	}
	out.Reset()
	if err := Run(context.Background(), "db", []string{"backups", "acme-db"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "bak_1") || !strings.Contains(out.String(), "ok") || !strings.Contains(out.String(), "1.2 KB") {
		t.Fatalf("list %q", out)
	}
	if !strings.HasSuffix(out.String(), "Restore to any second from 2026-10-23T03:05:00Z to 2026-10-30T15:38:00Z: yawl db restore acme-db --at <time> --into <new name>\n") {
		t.Fatalf("window %q", out)
	}
}

func TestCLIDbBackupFails(t *testing.T) {
	dbCLI(t, `[{"id":"bak_1","state":"failed","cause":"upload to b: 403"}]`)
	err := Run(context.Background(), "db", []string{"backup", "acme-db"})
	if err == nil || err.Error() != "backup bak_1 failed: upload to b: 403" {
		t.Fatalf("err %v", err)
	}
}

func TestCLIDbRestore(t *testing.T) {
	agentPub, agentPriv, _ := seal.GenerateKey()
	_, recPriv, _ := seal.GenerateKey()
	recovery := seal.EncodeRecovery(recPriv)
	var posted map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/services/acme/production/acme-db/keys":
			io.WriteString(w, `{"agent_pubkey":"`+agentPub.String()+`","server":"hel1"}`)
		case r.Method == "POST" && r.URL.Path == "/api/services/acme/production/acme-db/restore":
			json.NewDecoder(r.Body).Decode(&posted)
			io.WriteString(w, `{"deploy":"dep_r"}`)
		case r.URL.Path == "/api/deploys/dep_r/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"id\":\"dep_r\",\"service\":\"acme-db-2\",\"state\":\"live\",\"steps\":[{\"step\":\"ensure\",\"state\":\"ok\"},{\"step\":\"restore\",\"state\":\"ok\"}]}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	t.Setenv("YAWL_CONFIG", filepath.Join(dir, "config.json"))
	out := &bytes.Buffer{}
	stdout, stdin = out, strings.NewReader("")
	Run(context.Background(), "login", []string{srv.URL, "--token", "tok"})
	os.WriteFile(filepath.Join(dir, "yawl.yaml"), []byte("project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: acme-db, kind: postgres}\n"), 0o644)
	t.Chdir(dir)
	out.Reset()
	stdin = strings.NewReader(recovery + "\n")
	if err := Run(context.Background(), "db", []string{"restore", "acme-db", "bak_1", "--into", "acme-db-2"}); err != nil {
		t.Fatal(err)
	}
	if posted["backup"] != "bak_1" || posted["into"] != "acme-db-2" || strings.Contains(posted["recovery_box"], "yawl-recovery") {
		t.Fatalf("posted %v", posted)
	}
	opened, err := seal.Open(agentPub, agentPriv, posted["recovery_box"])
	if err != nil || string(opened) != recovery {
		t.Fatalf("the agent cannot open the key: %q %v", opened, err)
	}
	if !strings.Contains(out.String(), "Restored into acme-db-2. Point an app at it: yawl env set <app> DATABASE_URL --ref acme-db-2.url, then yawl deploy <app>.") {
		t.Fatalf("out %q", out)
	}
	stdin = strings.NewReader(recovery + "\n")
	if err := Run(context.Background(), "db", []string{"restore", "acme-db", "bak_1", "--into", "acme-db-3", "--from", "eu"}); err != nil {
		t.Fatal(err)
	}
	if posted["from"] != "eu" {
		t.Fatalf("posted %v", posted)
	}
	if err := Run(context.Background(), "db", []string{"restore", "acme-db", "bak_1"}); err == nil || err.Error() != "--into <new name> is required: a restore goes into a new database" {
		t.Fatalf("no --into: %v", err)
	}
	stdin, posted = strings.NewReader(recovery+"\n"), nil
	if err := Run(context.Background(), "db", []string{"restore", "acme-db", "--at", "2026-10-30T17:40:00Z", "--into", "acme-db-2"}); err != nil {
		t.Fatal(err)
	}
	if posted["at"] != "2026-10-30T17:40:00Z" || posted["backup"] != "" || posted["into"] != "acme-db-2" {
		t.Fatalf("posted %v", posted)
	}
	err = Run(context.Background(), "db", []string{"restore", "acme-db", "--at", "2026-10-30T17:40:00Z", "--into", "acme-db-2", "--from", "eu"})
	if err == nil || err.Error() != "--from reads a backup from a copy; --at reads the main storage only" {
		t.Fatalf("--at with --from: %v", err)
	}
}

func TestCLIDbImport(t *testing.T) {
	var got []string
	var length int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/services/acme/production/acme-db/import":
			b, _ := io.ReadAll(r.Body)
			got = append(got, r.Method+" "+r.URL.RawQuery+" "+string(b))
			if r.Method == "PUT" {
				length = r.ContentLength
			}
			io.WriteString(w, `{"deploy":"dep_7"}`)
		case r.URL.Path == "/api/deploys/dep_7/stream":
			io.WriteString(w, "data: {\"service\":\"acme-db\",\"state\":\"live\",\"steps\":[{\"step\":\"import\",\"state\":\"ok\"}]}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("YAWL_CONFIG", filepath.Join(dir, "config.json"))
	out := &bytes.Buffer{}
	stdout, stdin = out, strings.NewReader("")
	Run(context.Background(), "login", []string{srv.URL, "--token", "tok"})
	os.WriteFile(filepath.Join(dir, "yawl.yaml"), []byte("project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: acme-db, kind: postgres}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "heroku.sql"), []byte("CREATE TABLE t ();"), 0o644)
	t.Chdir(dir)
	for _, src := range []string{"heroku.sql", "https://bucket.example/d.sql", "server:old.sql.gz"} {
		if err := Run(context.Background(), "db", []string{"import", "acme-db", src}); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"PUT name=heroku.sql CREATE TABLE t ();", `POST  {"url":"https://bucket.example/d.sql"}`, `POST  {"path":"old.sql.gz"}`}
	if strings.Join(got, "|") != strings.Join(want, "|") || length != 18 || !strings.Contains(out.String(), "acme-db is live.") {
		t.Fatalf("%q length %d\n%s", got, length, out)
	}
}
