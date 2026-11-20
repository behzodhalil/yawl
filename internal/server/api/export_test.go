package api

import (
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/spec"
)

func TestAPIExport(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, three)
	testDB.Exec(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, ref, created_at, updated_at)
		SELECT 'v1', environment_id, id, 'STRIPE_KEY', 'secret-box', 'r', '', 1, 1 FROM services WHERE name = 'api'`)
	res, b := call(t, srv, tok, "GET", "/api/environments/acme/production/export", "")
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Disposition"), `filename="yawl.yaml"`) {
		t.Fatalf("%d %v", res.StatusCode, res.Header)
	}
	if !strings.Contains(string(b), "# api: STRIPE_KEY") || strings.Contains(string(b), "secret-box") {
		t.Fatalf("%s", b)
	}
	f, err := spec.Parse(b)
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	ch, err := a.Apply(t.Context(), f, "owner", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range ch {
		if c.Op != "unchanged" {
			t.Fatalf("applying the export would %s:\n%s", c, b)
		}
	}
}

func TestAPIExportTrashAndPreview(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, three)
	testDB.Exec(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, ref, created_at, updated_at)
		SELECT 'v1', environment_id, id, 'GONE_KEY', 'b', 'r', '', 1, 1 FROM services WHERE name = 'api'`)
	testDB.Exec(`UPDATE services SET deleted_at = 5 WHERE name = 'api'`)
	if _, b := call(t, srv, tok, "GET", "/api/environments/acme/production/export", ""); strings.Contains(string(b), "GONE_KEY") {
		t.Fatalf("%s", b)
	}
	testDB.Exec(`UPDATE environments SET preview_of = 'env_x'`)
	if res, b := call(t, srv, tok, "GET", "/api/environments/acme/production/export", ""); res.StatusCode != 409 || !strings.Contains(string(b), "preview") {
		t.Fatalf("preview: %d %s", res.StatusCode, b)
	}
	testDB.Exec(`UPDATE environments SET preview_of = '', deleted_at = 5`)
	if res, _ := call(t, srv, tok, "GET", "/api/environments/acme/production/export", ""); res.StatusCode != 404 {
		t.Fatalf("trashed: %d", res.StatusCode)
	}
}
