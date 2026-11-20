package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestDeveloperCannotEndAdminOnlyDeploys(t *testing.T) {
	srv, tok, a := setupApp(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	dev := memberToken(t, srv, tok, "dev@acme.uz", "developer")
	var svc string
	testDB.QueryRow(`SELECT id FROM services WHERE name = 'api'`).Scan(&svc)
	ctx := context.Background()
	state := func(id string) (s string) {
		testDB.QueryRow(`SELECT state FROM deploys WHERE id = ?`, id).Scan(&s)
		return s
	}

	a.St.Q.CreateMoveDeploy(ctx, db.CreateMoveDeployParams{ID: "dep_move", ServiceID: svc, MoveID: "mov_1", CreatedAt: 1, UpdatedAt: 1})
	a.St.Q.CreateRestoreDeploy(ctx, db.CreateRestoreDeployParams{ID: "dep_restore", ServiceID: svc, RestoreFrom: "bak_1", CreatedAt: 2, UpdatedAt: 2})
	a.St.Q.CreateImportDeploy(ctx, db.CreateImportDeployParams{ID: "dep_import", ServiceID: svc, ImportID: "imp_1", CreatedAt: 3, UpdatedAt: 3})
	for _, id := range []string{"dep_move", "dep_restore", "dep_import"} {
		if res, b := call(t, srv, dev, "POST", "/api/deploys/"+id+"/cancel", ""); res.StatusCode != 403 || state(id) != "queued" {
			t.Fatalf("developer cancel of %s: %d %s, state %s", id, res.StatusCode, b, state(id))
		}
	}
	if res, b := call(t, srv, tok, "POST", "/api/deploys/dep_import/cancel", ""); res.StatusCode != 202 || state("dep_import") != "superseded" {
		t.Fatalf("admin cancel: %d %s, state %s", res.StatusCode, b, state("dep_import"))
	}

	a.St.Q.CreateMoveDeploy(ctx, db.CreateMoveDeployParams{ID: "dep_move2", ServiceID: svc, MoveID: "mov_1", CreatedAt: 4, UpdatedAt: 4})
	testDB.Exec(`UPDATE deploys SET state = 'building' WHERE id = 'dep_move2'`)
	var asked int
	if res, b := call(t, srv, dev, "POST", "/api/deploys/dep_move2/cancel", ""); res.StatusCode != 403 {
		t.Fatalf("developer cancel of a building move deploy: %d %s", res.StatusCode, b)
	}
	testDB.QueryRow(`SELECT cancel_requested FROM deploys WHERE id = 'dep_move2'`).Scan(&asked)
	if asked != 0 {
		t.Fatalf("cancel_requested %d", asked)
	}

	a.St.Q.CreateRestoreDeploy(ctx, db.CreateRestoreDeployParams{ID: "dep_restore2", ServiceID: svc, RestoreFrom: "bak_1", CreatedAt: 5, UpdatedAt: 5})
	a.St.Q.CreateImportDeploy(ctx, db.CreateImportDeployParams{ID: "dep_import2", ServiceID: svc, ImportID: "imp_1", CreatedAt: 6, UpdatedAt: 6})
	res, b := call(t, srv, dev, "POST", "/api/services/acme/production/api/deploys", `{"commit":""}`)
	var out struct {
		DeployID string `json:"deploy_id"`
	}
	json.Unmarshal(b, &out)
	if res.StatusCode != 200 || out.DeployID == "" {
		t.Fatalf("developer deploy: %d %s", res.StatusCode, b)
	}
	if s := state("dep_restore2"); s != "queued" {
		t.Fatalf("restore deploy %s after a developer deploy", s)
	}
	if s := state("dep_import2"); s != "queued" {
		t.Fatalf("import deploy %s after a developer deploy", s)
	}
}
