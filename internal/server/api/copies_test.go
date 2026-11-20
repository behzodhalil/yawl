package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/vault"
)

func TestAPIBackupCopies(t *testing.T) {
	_, tok, a := setupApp(t)
	v, _ := vault.LoadOrCreate(t.TempDir())
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", Vault: v}))
	defer srv.Close()
	body := func(ep string) string {
		return `{"endpoint":"` + ep + `","bucket":"copies","region":"eu-1","access_key":"AKCOPY1234","secret_key":"copy-secret"}`
	}
	if res, b := call(t, srv, tok, "PUT", "/api/settings/storage/copies/eu", body("https://eu.s3.example")); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	for _, n := range []string{"us", "asia"} {
		call(t, srv, tok, "PUT", "/api/settings/storage/copies/"+n, body("https://"+n+".s3.example"))
	}
	if res, b := call(t, srv, tok, "PUT", "/api/settings/storage/copies/four", body("https://4.example")); res.StatusCode != 400 || !strings.Contains(string(b), "at most three") {
		t.Fatalf("a fourth: %d %s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, tok, "PUT", "/api/settings/storage/copies/Bad%20Name", body("https://x.example")); res.StatusCode != 400 {
		t.Fatalf("a bad name: %d", res.StatusCode)
	}
	_, b := call(t, srv, tok, "GET", "/api/settings/storage", "")
	if !strings.Contains(string(b), `"name":"eu"`) || !strings.Contains(string(b), `"access_key_end":"1234"`) || strings.Contains(string(b), "copy-secret") {
		t.Fatalf("%s", b)
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/settings/storage/copies/asia", ""); res.StatusCode != 204 {
		t.Fatalf("%d", res.StatusCode)
	}
	call(t, srv, tok, "POST", "/api/apply", "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: db, kind: postgres}\n")
	testDB.Exec(`INSERT INTO backups (id, service_id, object_key, state, created_at, updated_at) SELECT 'bak_1', id, 'k', 'ok', 1, 1 FROM services WHERE name = 'db'`)
	testDB.Exec(`INSERT INTO backup_copies (backup_id, copy, state, cause, updated_at) VALUES ('bak_1', 'eu', 'ok', '', 1), ('bak_1', 'us', 'failed', 'no such host', 1)`)
	_, b = call(t, srv, tok, "GET", "/api/services/acme/production/db/backups", "")
	if !strings.Contains(string(b), `"copies":[{"copy":"eu","state":"ok"},{"copy":"us","state":"failed","cause":"no such host"}]`) {
		t.Fatalf("%s", b)
	}
	res, b := call(t, srv, tok, "POST", "/api/services/acme/production/db/restore", `{"backup":"bak_1","into":"db-2","recovery_box":"c2VhbGVk","from":"us"}`)
	if res.StatusCode != 400 || !strings.Contains(string(b), "no good copy in us") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestCopyChangeForgetsRows(t *testing.T) {
	_, tok, a := setupApp(t)
	v, _ := vault.LoadOrCreate(t.TempDir())
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", Vault: v}))
	defer srv.Close()
	put := func(bucket string) {
		call(t, srv, tok, "PUT", "/api/settings/storage/copies/eu", `{"endpoint":"https://eu.s3.example","bucket":"`+bucket+`","access_key":"AK","secret_key":"SK"}`)
	}
	rows := func() (n int) {
		testDB.QueryRow(`SELECT COUNT(*) FROM backup_copies WHERE copy = 'eu'`).Scan(&n)
		return
	}
	put("old")
	testDB.Exec(`INSERT INTO backup_copies (backup_id, copy, state, updated_at) VALUES ('bak_1', 'eu', 'ok', 1)`)
	put("old")
	if rows() != 1 {
		t.Fatal("an unchanged copy lost its rows")
	}
	put("new")
	if rows() != 0 {
		t.Fatal("a copy moved to another bucket kept its old rows")
	}
	testDB.Exec(`INSERT INTO backup_copies (backup_id, copy, state, updated_at) VALUES ('bak_1', 'eu', 'ok', 1)`)
	call(t, srv, tok, "DELETE", "/api/settings/storage/copies/eu", "")
	if rows() != 0 {
		t.Fatal("a removed copy kept its rows")
	}
	if _, err := vault.GetSecret(context.Background(), a.St.Q, v, "storage.copy.eu.secret_key"); err == nil {
		t.Fatal("a removed copy kept its secret")
	}
}

func TestCopyRemovedClosesIncidents(t *testing.T) {
	_, tok, a := setupApp(t)
	v, _ := vault.LoadOrCreate(t.TempDir())
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", Vault: v}))
	defer srv.Close()
	call(t, srv, tok, "PUT", "/api/settings/storage/copies/eu", `{"endpoint":"https://eu.s3.example","bucket":"b","access_key":"AK","secret_key":"SK"}`)
	call(t, srv, tok, "POST", "/api/apply", "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: db, kind: postgres}\n")
	var svc string
	testDB.QueryRow(`SELECT id FROM services WHERE name = 'db'`).Scan(&svc)
	testDB.Exec(`INSERT INTO backups (id, service_id, object_key, state, created_at, updated_at) VALUES ('bak_1', ?, 'k', 'ok', 1, 1)`, svc)
	testDB.Exec(`INSERT INTO backup_copies (backup_id, copy, state, cause, updated_at) VALUES ('bak_1', 'eu', 'failed', 'no such host', 1)`)
	call(t, srv, tok, "DELETE", "/api/settings/storage/copies/eu", "")
	var action string
	var quiet int
	testDB.QueryRow(`SELECT action, quiet FROM outbox WHERE incident = ? ORDER BY created_at DESC LIMIT 1`, "backupcopy:"+svc+":eu").Scan(&action, &quiet)
	if action != "close" || quiet != 1 {
		t.Fatalf("incident %q quiet %d", action, quiet)
	}
	res, b := call(t, srv, tok, "POST", "/api/services/acme/production/db/restore", `{"at":"2026-10-30T17:40:00Z","into":"db-2","recovery_box":"c2VhbGVk","from":"eu"}`)
	if res.StatusCode != 400 || !strings.Contains(string(b), "a restore to a time reads the main storage") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}
