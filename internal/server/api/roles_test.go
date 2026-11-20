package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func adminOwner(t *testing.T) {
	t.Helper()
	testDB.Exec(`INSERT OR IGNORE INTO owner (id, email, password_hash, role, created_at, updated_at) VALUES ('own_admin', 'admin@acme.uz', 'x', 'admin', 1, 1)`)
	testDB.Exec(`UPDATE api_tokens SET owner_id = 'own_admin' WHERE owner_id = ''`)
}

func memberToken(t *testing.T, srv *httptest.Server, adminTok, email, role string) string {
	t.Helper()
	adminOwner(t)
	if _, err := testDB.Exec(`INSERT INTO owner (id, email, password_hash, role, created_at, updated_at) VALUES (?, ?, 'x', ?, 2, 2)`, "own_"+role, email, role); err != nil {
		t.Fatal(err)
	}
	res, b := call(t, srv, adminTok, "POST", "/api/tokens", `{"name":"`+role+`-ci"}`)
	m := regexp.MustCompile(`"token":"([^"]+)"`).FindStringSubmatch(string(b))
	if res.StatusCode != 200 || m == nil {
		t.Fatalf("token: %d %s", res.StatusCode, b)
	}
	testDB.Exec(`UPDATE api_tokens SET owner_id = ? WHERE name = ?`, "own_"+role, role+"-ci")
	return m[1]
}

func TestEveryRouteHasARole(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	re := regexp.MustCompile(`Handle(?:Func)?\("((?:GET|POST|PUT|DELETE) /api/[^"]+)"`)
	seen := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			seen++
			p := m[1]
			if strings.HasPrefix(p, "GET ") {
				_, admin := adminReads[p]
				if n := btoi(viewerReads[p]) + btoi(admin) + btoi(outsideAuth[p]); n != 1 {
					t.Errorf("%s is in %d read lists, want exactly 1", p, n)
				}
				continue
			}
			n := 0
			for _, set := range []map[string]bool{everyoneWrites, developerWrites, outsideAuth} {
				if set[p] {
					n++
				}
			}
			if _, ok := adminWrites[p]; ok {
				n++
			}
			if n != 1 {
				t.Errorf("%s is in %d role lists, want exactly 1", p, n)
			}
		}
	}
	if seen < 80 {
		t.Fatalf("found only %d routes; the walk is broken", seen)
	}
	for p := range adminReads {
		if !strings.HasPrefix(p, "GET ") {
			t.Errorf("adminReads holds a write: %s", p)
		}
	}
}

func TestDeveloperRoutes(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	dev := memberToken(t, srv, tok, "dev@acme.uz", "developer")
	if res, b := call(t, srv, dev, "POST", "/api/services/acme/production/api/deploys", "{}"); res.StatusCode == 403 {
		t.Fatalf("a Developer cannot deploy: %s", b)
	}
	for _, c := range []struct{ method, path, want string }{
		{"POST", "/api/services/acme/production/api/env/X/reveal", "Developers cannot reveal secrets."},
		{"DELETE", "/api/services/acme/production/api", "Developers cannot delete."},
		{"PUT", "/api/settings/storage", "Developers cannot change settings."},
		{"POST", "/api/join-token", "Developers cannot manage servers."},
		{"GET", "/api/services/acme/production/api/env/X/recovery_box", "Developers cannot read secrets."},
	} {
		if res, b := call(t, srv, dev, c.method, c.path, "{}"); res.StatusCode != 403 || strings.TrimSpace(string(b)) != c.want {
			t.Errorf("%s %s: %d %q", c.method, c.path, res.StatusCode, b)
		}
	}
}

func TestViewerReadsOnly(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	v := memberToken(t, srv, tok, "view@acme.uz", "viewer")
	if res, _ := call(t, srv, v, "GET", "/api/projects", ""); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	if res, b := call(t, srv, v, "POST", "/api/services/acme/production/api/deploys", "{}"); res.StatusCode != 403 || strings.TrimSpace(string(b)) != "Viewers cannot change anything." {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestViewerCannotReadSecrets(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	v := memberToken(t, srv, tok, "view@acme.uz", "viewer")
	if res, b := call(t, srv, v, "GET", "/api/services/acme/production/api/env/X/recovery_box", ""); res.StatusCode != 403 || strings.TrimSpace(string(b)) != "Viewers cannot read secrets." {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestAuditNamesMember(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	dev := memberToken(t, srv, tok, "dev@acme.uz", "developer")
	if res, b := call(t, srv, dev, "POST", "/api/apply", yaml); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var n int
	testDB.QueryRow(`SELECT count(*) FROM events WHERE kind = 'audit' AND subject_id = 'POST /api/apply' AND actor = 'API token developer-ci (dev@acme.uz)'`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d audit rows name the member", n)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestViewerCannotReadMovePlan(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	v := memberToken(t, srv, tok, "view@acme.uz", "viewer")
	if res, b := call(t, srv, v, "GET", "/api/environments/acme/production/move?to=hel1", ""); res.StatusCode != 403 || strings.TrimSpace(string(b)) != "Viewers cannot move environments." {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestRolesFailClosed(t *testing.T) {
	h := &handler{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", func(w http.ResponseWriter, r *http.Request) {})
	w := httptest.NewRecorder()
	h.roles(mux, mux).ServeHTTP(w, httptest.NewRequest("GET", "/api/projects", nil))
	if w.Code != 403 {
		t.Fatalf("%d", w.Code)
	}
}

func TestUnlistedReadIsAdmins(t *testing.T) {
	if ok, why := allowed("viewer", "GET /api/secret-thing-added-later"); ok || why != "Viewers cannot do this." {
		t.Fatal(ok, why)
	}
	if ok, _ := allowed("admin", "GET /api/secret-thing-added-later"); !ok {
		t.Fatal("an Admin is refused")
	}
}

func TestDeveloperCannotShortenKeep(t *testing.T) {
	srv, tok := setup(t)
	withDB := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n  - {name: db, kind: postgres, keep: {daily: KEEP, weekly: 0, monthly: 0}}\n"
	keep := func(n string) string { return strings.Replace(withDB, "KEEP", n, 1) }
	if res, b := call(t, srv, tok, "POST", "/api/apply", keep("30")); res.StatusCode != 200 {
		t.Fatalf("admin apply %d %s", res.StatusCode, b)
	}
	day := int64(24 * 3600 * 1000)
	now := time.Now().UnixMilli()
	for i := int64(1); i <= 5; i++ {
		testDB.Exec(`INSERT INTO backups (id, service_id, object_key, kind, state, created_at, finished_at, updated_at)
			SELECT ?, id, ?, 'dump', 'ok', ?, ?, ? FROM services WHERE name = 'db'`, fmt.Sprint("bak_", i), fmt.Sprint("obj/", i), now-i*day, now-i*day, now-i*day)
	}
	dev := memberToken(t, srv, tok, "dev@acme.uz", "developer")
	for _, dry := range []string{"/api/apply?dry=1", "/api/apply"} {
		if res, b := call(t, srv, dev, "POST", dry, keep("1")); res.StatusCode != 409 || !strings.Contains(string(b), "db: keep 1 daily deletes 5 of its 5 backups") || !strings.Contains(string(b), "only an Admin can apply that") {
			t.Fatalf("developer %s: %d %s", dry, res.StatusCode, b)
		}
	}
	var stored string
	testDB.QueryRow(`SELECT keep_json FROM services WHERE name = 'db'`).Scan(&stored)
	if !strings.Contains(stored, `"daily":30,`) {
		t.Fatalf("keep %s", stored)
	}
	if res, b := call(t, srv, dev, "POST", "/api/apply", keep("60")); res.StatusCode != 200 {
		t.Fatalf("developer lengthens %d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/apply", keep("1")); res.StatusCode != 200 {
		t.Fatalf("admin shortens %d %s", res.StatusCode, b)
	}
}
