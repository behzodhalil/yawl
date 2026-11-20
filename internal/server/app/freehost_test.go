package app

import (
	"context"
	"fmt"
	"testing"
)

func TestApplyRefusesTakenFreeHost(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	zeta := "project: zeta\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: %s, repo: \"https://x/a\"}\n"
	want := "api-production.1-2-3-4.sslip.io is already acme/production/api's; name this service or environment differently."
	for _, dry := range []bool{true, false} {
		if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(zeta, "web")), "owner", dry); err == nil || err.Error() != want {
			t.Fatalf("dry %v: %v", dry, err)
		}
	}
	if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(zeta, "worker")), "owner", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(ctx, parse(t, file), "owner", false); err != nil {
		t.Fatal(err)
	}
}

func TestCopyRefusesTakenFreeHost(t *testing.T) {
	st, a := seedMove(t, false)
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('prj_z', 'zeta', 1, 1)`)
	st.DB.Exec(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_z', 'prj_z', 'staging', 'srv_1', 1, 1)`)
	want := "api-staging.1-2-3-4.sslip.io is already acme/staging/api's; name this service or environment differently."
	if _, err := a.PlanCopy(ctx, "svc_api", "zeta", "staging"); err == nil || err.Error() != want {
		t.Fatal(err)
	}
	for _, move := range []bool{false, true} {
		if _, err := a.CopyService(ctx, "svc_api", "zeta", "staging", nil, "", move, "owner"); err == nil || err.Error() != want {
			t.Fatalf("move %v: %v", move, err)
		}
	}
}

func TestRestoreRefusesTakenFreeHost(t *testing.T) {
	st, a := trashWorld(t)
	ctx := context.Background()
	zeta := "project: zeta\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/a\"}\n"
	want := "api-production.1-2-3-4.sslip.io is already zeta/production/api's; name this service or environment differently."
	for _, kind := range []string{"service", "environment", "project"} {
		id := map[string]string{"service": "svc_1", "environment": "env_1", "project": "prj_1"}[kind]
		if err := a.Delete(ctx, kind, id, "owner"); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Apply(ctx, parse(t, zeta), "owner", false); err != nil {
			t.Fatal(err)
		}
		if err := a.RestoreFromTrash(ctx, kind, id, "owner"); err == nil || err.Error() != want {
			t.Fatalf("%s: %v", kind, err)
		}
		if s, _ := st.Q.GetService(ctx, "svc_1"); !s.DeletedAt.Valid {
			t.Fatalf("%s: restored anyway", kind)
		}
		zid, _ := st.Q.GetLiveProjectByName(ctx, "zeta")
		if err := a.Delete(ctx, "project", zid, "owner"); err != nil {
			t.Fatal(err)
		}
		st.DB.Exec(`DELETE FROM services WHERE environment_id IN (SELECT id FROM environments WHERE project_id = ?)`, zid)
		st.DB.Exec(`DELETE FROM environments WHERE project_id = ?`, zid)
		st.DB.Exec(`DELETE FROM projects WHERE id = ?`, zid)
		if err := a.RestoreFromTrash(ctx, kind, id, "owner"); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
}

func TestMoveRefusesTakenFreeHost(t *testing.T) {
	st, a := seedMove(t, false)
	st.DB.Exec(`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('prj_z', 'zeta', 1, 1)`)
	st.DB.Exec(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_z', 'prj_z', 'staging', 'srv_2', 1, 1)`)
	st.DB.Exec(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, created_at, updated_at)
		VALUES ('svc_zw', 'env_z', 'web', 'web', 'https://x/y', 'main', 10000, '/', 60000, 30000, 1, 1)`)
	want := "web-staging.5-6-7-8.sslip.io is already zeta/staging/web's; name this service or environment differently."
	if _, err := a.PlanMove(context.Background(), "acme", "staging", "hk1"); err == nil || err.Error() != want {
		t.Fatal(err)
	}
}
