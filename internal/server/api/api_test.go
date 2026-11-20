package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func setup(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv, tok, _ := setupApp(t)
	return srv, tok
}

func setupApp(t *testing.T) (*httptest.Server, string, *app.App) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	testDB = st.DB
	st.Q.UpsertServer(context.Background(), db.UpsertServerParams{ID: "srv_1", Name: "hel1", PublicIp: "1.2.3.4", CreatedAt: 1, UpdatedAt: 1})
	a := app.New(st)
	tok, _ := a.CreateToken(context.Background(), "test")
	v, err := vault.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", Vault: v}))
	t.Cleanup(srv.Close)
	return srv, tok, a
}

func call(t *testing.T, srv *httptest.Server, tok, method, path, body string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b []byte
	b, _ = readAll(res)
	return res, b
}

const yaml = "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n"

func TestAPIRequiresToken(t *testing.T) {
	srv, _ := setup(t)
	for _, tok := range []string{"", "yawl_wrong"} {
		if res, _ := call(t, srv, tok, "GET", "/api/status", ""); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token %q: status %d", tok, res.StatusCode)
		}
	}
}

func TestAPIDeployFlow(t *testing.T) {
	srv, tok := setup(t)
	if res, b := call(t, srv, tok, "POST", "/api/apply", yaml); res.StatusCode != 200 {
		t.Fatalf("apply %d %s", res.StatusCode, b)
	}
	res, b := call(t, srv, tok, "POST", "/api/services/acme/production/api/deploys", `{"commit":""}`)
	if res.StatusCode != 200 {
		t.Fatalf("deploy %d %s", res.StatusCode, b)
	}
	var out struct {
		DeployID string `json:"deploy_id"`
	}
	json.Unmarshal(b, &out)
	res, b = call(t, srv, tok, "GET", "/api/deploys/"+out.DeployID, "")
	var d DeployView
	json.Unmarshal(b, &d)
	if res.StatusCode != 200 || d.State != "queued" || d.Service != "api" {
		t.Fatalf("get %d %s", res.StatusCode, b)
	}
	_, b = call(t, srv, tok, "GET", "/api/status", "")
	var rows []StatusRow
	json.Unmarshal(b, &rows)
	if len(rows) != 1 || rows[0].URL != "https://api-production.1-2-3-4.sslip.io" || rows[0].State != "queued" {
		t.Fatalf("status %s", b)
	}
}

func TestAPIPlanShowsChanges(t *testing.T) {
	srv, tok := setup(t)
	_, b := call(t, srv, tok, "POST", "/api/plan", yaml)
	if !strings.Contains(string(b), `"op":"create"`) {
		t.Fatalf("plan %s", b)
	}
	_, b = call(t, srv, tok, "GET", "/api/status", "")
	if strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("plan wrote something: %s", b)
	}
}

func TestAPIBadYAML(t *testing.T) {
	srv, tok := setup(t)
	res, b := call(t, srv, tok, "POST", "/api/apply", "project: Bad Name\n")
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "project") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestAPIUnknownService(t *testing.T) {
	srv, tok := setup(t)
	if res, _ := call(t, srv, tok, "POST", "/api/services/acme/production/nope/deploys", `{}`); res.StatusCode != http.StatusNotFound {
		t.Fatalf("%d", res.StatusCode)
	}
}

func readAll(res *http.Response) ([]byte, error) { return io.ReadAll(res.Body) }

var testDB *sql.DB
