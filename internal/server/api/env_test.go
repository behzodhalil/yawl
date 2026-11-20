package api

import (
	"context"
	"encoding/json"
	"path/filepath"

	"encoding/base64"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var box = base64.StdEncoding.EncodeToString([]byte("sealed-bytes-the-server-cannot-read"))

func ready(t *testing.T, _ bool) (*httptest.Server, string) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	if res, b := call(t, srv, tok, "PUT", "/api/recovery-key", `{"pubkey":"`+base64.StdEncoding.EncodeToString(make([]byte, 32))+`"}`); res.StatusCode != 200 {
		t.Fatalf("recovery key: %d %s", res.StatusCode, b)
	}
	return srv, tok
}

func setAgentKey(t *testing.T) {
	testDB.Exec(`UPDATE servers SET agent_pubkey = ? WHERE id = 'srv_1'`, base64.StdEncoding.EncodeToString(make([]byte, 32)))
}

func put(t *testing.T, srv *httptest.Server, tok, path, body string) (int, string) {
	res, b := call(t, srv, tok, "PUT", path, body)
	return res.StatusCode, string(b)
}

func sealed() string { return `{"agent_box":"` + box + `","recovery_box":"` + box + `"}` }

func TestSetRejectsPlaintext(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	code, b := put(t, srv, tok, "/api/services/acme/production/api/env/SECRET", `{"value":"hunter2","agent_box":"`+box+`","recovery_box":"`+box+`"}`)
	if code != http.StatusBadRequest || !strings.Contains(b, "send sealed values only") {
		t.Fatalf("%d %s", code, b)
	}
}

func TestSetRefusesWithoutAgentKey(t *testing.T) {
	srv, tok := ready(t, false)
	code, b := put(t, srv, tok, "/api/services/acme/production/api/env/SECRET", sealed())
	if code != http.StatusConflict || !strings.Contains(b, "server hel1 has no secrets key; re-join it") {
		t.Fatalf("%d %s", code, b)
	}
}

func TestSetRefusesPORT(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	for _, k := range []string{"PORT", "lower", "1BAD"} {
		if code, b := put(t, srv, tok, "/api/services/acme/production/api/env/"+k, sealed()); code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", k, code, b)
		}
	}
}

func TestRecoveryKeyOnce(t *testing.T) {
	srv, tok := ready(t, false)
	if code, _ := put(t, srv, tok, "/api/recovery-key", `{"pubkey":"`+base64.StdEncoding.EncodeToString(make([]byte, 32))+`"}`); code != http.StatusConflict {
		t.Fatalf("second recovery key: %d", code)
	}
}

func TestBackupKeyOnce(t *testing.T) {
	key := `{"key":"` + base64.StdEncoding.EncodeToString(make([]byte, 32)) + `"}`
	srv, tok := setup(t)
	if code, b := put(t, srv, tok, "/api/recovery-key/backup-key", key); code != http.StatusConflict || !strings.Contains(string(b), "no recovery key yet") {
		t.Fatalf("before the recovery key: %d %s", code, b)
	}
	pub := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if code, _ := put(t, srv, tok, "/api/recovery-key", `{"pubkey":"`+pub+`","backup_key":"short"}`); code != http.StatusBadRequest {
		t.Fatalf("bad backup_key: %d", code)
	}
	if code, b := put(t, srv, tok, "/api/recovery-key", `{"pubkey":"`+pub+`","backup_key":"`+pub+`"}`); code != 200 {
		t.Fatalf("init: %d %s", code, b)
	}
	if code, _ := put(t, srv, tok, "/api/recovery-key/backup-key", key); code != http.StatusConflict {
		t.Fatalf("after init: %d", code)
	}
	srv, tok = ready(t, false)
	for _, c := range []struct {
		body string
		code int
	}{{`{"key":"short"}`, http.StatusBadRequest}, {key, 200}, {key, http.StatusConflict}} {
		if code, b := put(t, srv, tok, "/api/recovery-key/backup-key", c.body); code != c.code {
			t.Fatalf("%s: %d %s", c.body, code, b)
		}
	}
}

func TestListHasNoBoxes(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	if code, b := put(t, srv, tok, "/api/services/acme/production/api/env/SECRET", sealed()); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	_, b := call(t, srv, tok, "GET", "/api/services/acme/production/api/env", "")
	if !strings.Contains(string(b), `"SECRET"`) || strings.Contains(string(b), "box") {
		t.Fatalf("%s", b)
	}
}

func TestServiceOverridesShared(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	put(t, srv, tok, "/api/environments/acme/production/env/KEY", sealed())
	put(t, srv, tok, "/api/services/acme/production/api/env/KEY", sealed())
	_, b := call(t, srv, tok, "GET", "/api/services/acme/production/api/env", "")
	if strings.Count(string(b), `"KEY"`) != 1 || !strings.Contains(string(b), `"scope":"service"`) {
		t.Fatalf("%s", b)
	}
}

func TestDeleteVar(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	put(t, srv, tok, "/api/services/acme/production/api/env/KEY", sealed())
	if res, _ := call(t, srv, tok, "DELETE", "/api/services/acme/production/api/env/KEY", ""); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/services/acme/production/api/env/KEY", ""); res.StatusCode != 404 {
		t.Fatal("deleting a missing key should be 404")
	}
}

type okSender struct{}

func (okSender) Send(_ context.Context, _ string, c proto.Command) (proto.Result, error) {
	b, _ := json.Marshal(proto.RevealData{Box: box})
	return proto.Result{Key: c.Key, OK: true, Data: b}, nil
}

func TestRevealIsRecordedAndAnnounced(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_1", Name: "hel1", PublicIp: "1.2.3.4", AgentPubkey: base64.StdEncoding.EncodeToString(make([]byte, 32)), CreatedAt: 1, UpdatedAt: 1})
	a := app.New(st)
	tok, _ := a.CreateToken(ctx, "laptop")
	srv := httptest.NewServer(New(Deps{App: a, GW: okSender{}}))
	defer srv.Close()
	call(t, srv, tok, "POST", "/api/apply", yaml)
	put(t, srv, tok, "/api/recovery-key", `{"pubkey":"`+base64.StdEncoding.EncodeToString(make([]byte, 32))+`"}`)
	put(t, srv, tok, "/api/services/acme/production/api/env/API_KEY", sealed())
	res, b := call(t, srv, tok, "POST", "/api/services/acme/production/api/env/API_KEY/reveal", `{"to":"`+base64.StdEncoding.EncodeToString(make([]byte, 32))+`"}`)
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	rows, _ := st.Q.ListOutbox(ctx)
	if len(rows) != 1 || rows[0].Body != "acme-api: API_KEY was revealed (API token laptop)." {
		t.Fatalf("outbox %+v", rows)
	}
}

func TestSetEnvBuildFlag(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	body := `{"agent_box":"` + box + `","recovery_box":"` + box + `","build":true}`
	if code, b := put(t, srv, tok, "/api/services/acme/production/api/env/NPM_TOKEN", body); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	put(t, srv, tok, "/api/services/acme/production/api/env/RUNTIME_ONLY", sealed())
	_, b := call(t, srv, tok, "GET", "/api/services/acme/production/api/env", "")
	if !strings.Contains(string(b), `"key":"NPM_TOKEN","scope":"service","build":true`) || strings.Count(string(b), `"build":true`) != 1 {
		t.Fatalf("%s", b)
	}
	_, b = call(t, srv, tok, "GET", "/api/environments/acme/production/env", "")
	if strings.Count(string(b), `"build":true`) != 1 {
		t.Fatalf("environment listing: %s", b)
	}
}

func TestSetEnvRef(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	withDB := yaml + "  - {name: acme-db, kind: postgres}\n"
	if res, b := call(t, srv, tok, "POST", "/api/apply", withDB); res.StatusCode != 200 {
		t.Fatalf("apply: %d %s", res.StatusCode, b)
	}
	path := "/api/services/acme/production/api/env/DATABASE_URL"
	if code, b := put(t, srv, tok, path, `{"ref":"acme-db.url"}`); code != 200 {
		t.Fatalf("good ref: %d %s", code, b)
	}
	_, b := call(t, srv, tok, "GET", "/api/services/acme/production/api/env", "")
	if !strings.Contains(string(b), `"ref":"acme-db.url"`) {
		t.Fatalf("listing: %s", b)
	}
	for body, want := range map[string]string{
		`{"ref":"acme-db.password"}`:                      "DATABASE_URL: a reference names a database in this project: <db>.url or <env>/<db>.url",
		`{"ref":"api.url"}`:                               "api is not a database in production",
		`{"ref":"nope.url"}`:                              "nope is not a database in production",
		`{"ref":"acme-db.url","agent_box":"` + box + `"}`: "send a ref or sealed boxes, not both",
	} {
		if code, b := put(t, srv, tok, path, body); code != 400 || !strings.Contains(b, want) {
			t.Errorf("%s: %d %s, want 400 %q", body, code, b, want)
		}
	}
}

func TestAPIBackupRoutes(t *testing.T) {
	srv, tok := ready(t, true)
	call(t, srv, tok, "POST", "/api/apply", yaml+"  - {name: acme-db, kind: postgres}\n")
	res, b := call(t, srv, tok, "POST", "/api/services/acme/production/acme-db/backups", "")
	if res.StatusCode != 400 || !strings.Contains(string(b), "acme-db has not been created yet; deploy it first") {
		t.Fatalf("never created: %d %s", res.StatusCode, b)
	}
	testDB.Exec(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, created_at, updated_at)
		SELECT 'v', environment_id, id, 'URL', 'u', 'r', 1, 1 FROM services WHERE name = 'acme-db'`)
	res, b = call(t, srv, tok, "POST", "/api/services/acme/production/acme-db/backups", "")
	if res.StatusCode != 200 || !strings.Contains(string(b), `"id":"bak_`) {
		t.Fatalf("backup: %d %s", res.StatusCode, b)
	}
	res, b = call(t, srv, tok, "GET", "/api/services/acme/production/acme-db/backups", "")
	if res.StatusCode != 200 || !strings.Contains(string(b), `"state":"queued"`) || strings.Contains(string(b), "object_key") {
		t.Fatalf("list: %d %s", res.StatusCode, b)
	}
}

func TestAPIRestoreRoute(t *testing.T) {
	srv, tok := ready(t, true)
	call(t, srv, tok, "POST", "/api/apply", yaml+"  - {name: acme-db, kind: postgres}\n")
	testDB.Exec(`INSERT INTO backups (id, service_id, object_key, state, created_at, updated_at)
		SELECT 'bak_1', id, 'k', 'ok', 1, 1 FROM services WHERE name = 'acme-db'`)
	path := "/api/services/acme/production/acme-db/restore"
	if res, b := call(t, srv, tok, "POST", path, `{"backup":"bak_1","into":"acme-db-2"}`); res.StatusCode != 400 || !strings.Contains(string(b), "recovery_box") {
		t.Fatalf("no key: %d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", path, `{"backup":"bak_1","into":"api","recovery_box":"`+box+`"}`); res.StatusCode != 400 || !strings.Contains(string(b), "api already exists") {
		t.Fatalf("into an existing service: %d %s", res.StatusCode, b)
	}
	res, b := call(t, srv, tok, "POST", path, `{"backup":"bak_1","into":"acme-db-2","recovery_box":"`+box+`"}`)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"deploy":"dep_`) {
		t.Fatalf("restore: %d %s", res.StatusCode, b)
	}
}

func TestEnvironmentKeys(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	res, b := call(t, srv, tok, "GET", "/api/environments/acme/production/keys", "")
	if res.StatusCode != 200 || !strings.Contains(string(b), `"server":"hel1"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

type echoHash struct{}

func (echoHash) Send(_ context.Context, _ string, c proto.Command) (proto.Result, error) {
	var a proto.HashArgs
	json.Unmarshal(c.Args, &a)
	var d proto.HashData
	for _, v := range a.Vars {
		d.Hashes = append(d.Hashes, v.Box)
	}
	b, _ := json.Marshal(d)
	return proto.Result{Key: c.Key, OK: true, Data: b}, nil
}

func TestCompareAPI(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_1", Name: "hel1", PublicIp: "1.2.3.4", AgentPubkey: base64.StdEncoding.EncodeToString(make([]byte, 32)), CreatedAt: 1, UpdatedAt: 1})
	a := app.New(st)
	tok, _ := a.CreateToken(ctx, "laptop")
	srv := httptest.NewServer(New(Deps{App: a, GW: echoHash{}}))
	defer srv.Close()
	call(t, srv, tok, "POST", "/api/apply", yaml)
	call(t, srv, tok, "POST", "/api/apply", strings.Replace(yaml, "production", "staging", 1))
	put(t, srv, tok, "/api/recovery-key", `{"pubkey":"`+base64.StdEncoding.EncodeToString(make([]byte, 32))+`"}`)
	put(t, srv, tok, "/api/services/acme/production/api/env/B_KEY", sealed())
	put(t, srv, tok, "/api/services/acme/staging/api/env/B_KEY", sealed())
	put(t, srv, tok, "/api/environments/acme/staging/env/A_KEY", sealed())
	res, b := call(t, srv, tok, "POST", "/api/projects/acme/compare", `{"left":"staging","right":"production","keys":{"staging":"k1","production":"k2"}}`)
	if res.StatusCode != 200 || string(b) != `[{"service":"","key":"A_KEY","state":"only_left"},{"service":"api","key":"B_KEY","state":"same"}]`+"\n" {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	res, b = call(t, srv, tok, "POST", "/api/projects/acme/compare", `{"left":"staging","right":"production","keys":{"staging":"k1"}}`)
	if res.StatusCode != 400 || strings.TrimSpace(string(b)) != "A comparison key is needed for production." {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestAPIRestoreAtAndWindow(t *testing.T) {
	srv, tok := ready(t, true)
	call(t, srv, tok, "POST", "/api/apply", yaml+"  - {name: acme-db, kind: postgres, pitr: true}\n")
	base := "/api/services/acme/production/acme-db/"
	if res, b := call(t, srv, tok, "GET", base+"pitr", ""); res.StatusCode != 200 || string(b) != `{"pitr":true,"from":0,"to":0,"shipped":0}`+"\n" {
		t.Fatalf("empty window: %d %s", res.StatusCode, b)
	}
	testDB.Exec(`INSERT INTO backups (id, service_id, object_key, kind, state, created_at, finished_at, updated_at)
		SELECT 'base_1', id, 'k', 'base', 'ok', 1000, 2000, 1 FROM services WHERE name = 'acme-db'`)
	testDB.Exec(`INSERT INTO wal_batches (id, service_id, object_key, first_wal, last_wal, size, created_at)
		SELECT 'w1', id, 'w', 'a', 'b', 1, 3600000 FROM services WHERE name = 'acme-db'`)
	if res, b := call(t, srv, tok, "GET", base+"pitr", ""); res.StatusCode != 200 || string(b) != `{"pitr":true,"from":2000,"to":3420000,"shipped":3600000}`+"\n" {
		t.Fatalf("window: %d %s", res.StatusCode, b)
	}
	if _, b := call(t, srv, tok, "GET", base+"backups", ""); strings.Contains(string(b), "base_1") {
		t.Fatalf("the list shows a base: %s", b)
	}
	if res, b := call(t, srv, tok, "POST", base+"restore", `{"at":"1970-01-01T00:30:00Z","into":"acme-db-2","recovery_box":"`+box+`"}`); res.StatusCode != 200 || !strings.Contains(string(b), `"deploy":"dep_`) {
		t.Fatalf("restore at: %d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", base+"restore", `{"at":"yesterday","into":"acme-db-3","recovery_box":"`+box+`"}`); res.StatusCode != 400 || !strings.Contains(string(b), "2026-10-30T17:40:00Z") {
		t.Fatalf("bad time: %d %s", res.StatusCode, b)
	}
}

func TestTrashedEnvironmentIsGone(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	call(t, srv, tok, "POST", "/api/apply", strings.Replace(yaml, "production", "staging", 1))
	if res, b := call(t, srv, tok, "DELETE", "/api/environments/acme/staging", `{"confirm":"staging"}`); res.StatusCode >= 300 {
		t.Fatalf("delete %d %s", res.StatusCode, b)
	}
	for _, c := range [][3]string{
		{"GET", "/api/environments/acme/staging/env", ""},
		{"PUT", "/api/environments/acme/staging/env/A_KEY", sealed()},
		{"POST", "/api/environments/acme/staging/services", `{"name":"api","kind":"web","repo":"https://x/y"}`},
	} {
		if res, b := call(t, srv, tok, c[0], c[1], c[2]); res.StatusCode != 404 {
			t.Errorf("%s %s: %d %s", c[0], c[1], res.StatusCode, b)
		}
	}
	if res, b := call(t, srv, tok, "POST", "/api/projects/acme/compare", `{"left":"staging","right":"production","keys":{"staging":"k1","production":"k2"}}`); res.StatusCode != 409 ||
		strings.TrimSpace(string(b)) != "There is no environment staging in acme." {
		t.Errorf("compare %d %s", res.StatusCode, b)
	}
}

func TestDatabaseOwnVarsAreKept(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	call(t, srv, tok, "POST", "/api/apply", yaml+"  - {name: db, kind: postgres}\n")
	for _, c := range [][3]string{
		{"PUT", "/api/services/acme/production/db/env/PASSWORD", sealed()},
		{"PUT", "/api/services/acme/production/db/env/URL", `{"ref":"api.url"}`},
		{"DELETE", "/api/services/acme/production/db/env/URL", ""},
	} {
		if res, b := call(t, srv, tok, c[0], c[1], c[2]); res.StatusCode != 409 ||
			strings.TrimSpace(string(b)) != "A database's PASSWORD and URL are its own; Yawl makes them." {
			t.Errorf("%s %s: %d %s", c[0], c[1], res.StatusCode, b)
		}
	}
	if code, b := put(t, srv, tok, "/api/services/acme/production/api/env/PASSWORD", sealed()); code != 200 {
		t.Fatalf("app: %d %s", code, b)
	}
}

func TestRecoveryKeyInitRace(t *testing.T) {
	srv, tok := setup(t)
	codes := make(chan int, 8)
	for i := range 8 {
		go func() {
			k := make([]byte, 32)
			k[0] = byte(i + 1)
			res, _ := call(t, srv, tok, "PUT", "/api/recovery-key", `{"pubkey":"`+base64.StdEncoding.EncodeToString(k)+`"}`)
			codes <- res.StatusCode
		}()
	}
	ok := 0
	for range 8 {
		if <-codes == 200 {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d inits were told ok", ok)
	}
}
