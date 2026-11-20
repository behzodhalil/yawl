package app

import (
	"context"
	"strings"
	"testing"
)

func TestCopyService(t *testing.T) {
	st, a := seedMove(t, true)
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_qa', 'prj_1', 'qa', 'srv_1', 1, 1), ('env_eu', 'prj_1', 'eu', 'srv_2', 1, 1)`)
	st.DB.Exec(`UPDATE services SET replicas = 2, tags_json = '["front"]', port = 8080 WHERE id = 'svc_api'`)

	p, err := a.PlanCopy(ctx, "svc_api", "acme", "qa")
	if err != nil || len(p.Vars) != 0 || !strings.Contains(strings.Join(p.Lines, "\n"), "DATABASE_URL refers to staging-db, which acme/qa does not have") {
		t.Fatalf("%+v %v", p, err)
	}
	id, err := a.CopyService(ctx, "svc_api", "acme", "qa", nil, "", false, "owner")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := st.Q.GetService(ctx, id)
	if s.Name != "api" || s.Port != 8080 || s.Replicas != 2 || s.TagsJson != `["front"]` || s.EnvironmentID != "env_qa" || s.CurrentDeployID.Valid {
		t.Fatalf("%+v", s)
	}
	var token, ref string
	st.DB.QueryRow(`SELECT agent_box || '/' || recovery_box FROM env_vars WHERE service_id = ? AND key = 'TOKEN'`, id).Scan(&token)
	st.DB.QueryRow(`SELECT ref FROM env_vars WHERE service_id = ? AND key = 'DATABASE_URL'`, id).Scan(&ref)
	var shared int
	st.DB.QueryRow(`SELECT COUNT(*) FROM env_vars WHERE environment_id = 'env_qa' AND key = 'SHARED'`).Scan(&shared)
	if token != "a2/r2" || ref != "staging-db.url" || shared != 0 {
		t.Fatalf("token %q ref %q shared %d", token, ref, shared)
	}
	if _, err := a.CopyService(ctx, "svc_api", "acme", "qa", nil, "", false, "owner"); err == nil || !strings.Contains(err.Error(), "acme/qa has a service api already") {
		t.Fatalf("%v", err)
	}

	p, _ = a.PlanCopy(ctx, "svc_api", "acme", "eu")
	if p.ToPubkey != "PUBK" || len(p.Vars) != 1 || p.Vars[0].ID != "var_a" || p.Vars[0].RecoveryBox != "r2" {
		t.Fatalf("%+v", p)
	}
	if _, err := a.CopyService(ctx, "svc_api", "acme", "eu", nil, "", false, "owner"); err == nil || !strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("%v", err)
	}
	id, err = a.CopyService(ctx, "svc_api", "acme", "eu", map[string]Reseal{"var_a": {AgentBox: "sealed-for-hk1", UpdatedAt: 12}}, "PUBK", false, "owner")
	if err != nil {
		t.Fatal(err)
	}
	st.DB.QueryRow(`SELECT agent_box FROM env_vars WHERE service_id = ? AND key = 'TOKEN'`, id).Scan(&token)
	if token != "sealed-for-hk1" {
		t.Fatalf("%q", token)
	}

	id, err = a.CopyService(ctx, "svc_staging-db", "acme", "qa", nil, "", false, "owner")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM env_vars WHERE service_id = ?`, id).Scan(&n)
	if n != 0 {
		t.Fatalf("a copied database took %d variables", n)
	}

	if _, err := a.CopyService(ctx, "svc_web", "acme", "qa", nil, "", true, "owner"); err != nil {
		t.Fatal(err)
	}
	if o, _ := st.Q.GetService(ctx, "svc_web"); !o.DeletedAt.Valid {
		t.Fatal("a moved service stayed")
	}
}

func TestCopyServiceReviewFixes(t *testing.T) {
	st, a := seedMove(t, true)
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_qa', 'prj_1', 'qa', 'srv_1', 1, 1)`)
	st.DB.Exec(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, ref, created_at, updated_at) VALUES ('var_x', 'env_s', 'svc_api', 'OTHER_DB', '', '', 'production/main-db.url', 1, 1)`)
	st.DB.Exec(`UPDATE services SET basic_auth = 'ops:aGFzaA==' WHERE id = 'svc_api'`)
	p, err := a.PlanCopy(ctx, "svc_api", "acme", "qa")
	if err != nil || !strings.Contains(strings.Join(p.Lines, "\n"), "OTHER_DB refers to another environment's database and is not copied; set it again") {
		t.Fatalf("%+v %v", p, err)
	}
	id, err := a.CopyService(ctx, "svc_api", "acme", "qa", nil, "", false, "owner")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM env_vars WHERE service_id = ? AND key = 'OTHER_DB'`, id).Scan(&n)
	var auth string
	st.DB.QueryRow(`SELECT basic_auth FROM services WHERE id = ?`, id).Scan(&auth)
	if n != 0 || auth != "ops:aGFzaA==" {
		t.Fatalf("other-env ref copied %d; auth %q", n, auth)
	}
	st.DB.Exec(`INSERT INTO domains (id, service_id, host, dns_state, created_at, updated_at) VALUES ('dom_1', 'svc_web', 'web.acme.uz', 'ok', 1, 1)`)
	st.DB.Exec(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_q2', 'prj_1', 'q2', 'srv_1', 1, 1)`)
	if _, err := a.CopyService(ctx, "svc_web", "acme", "q2", nil, "", true, "owner"); err == nil || !strings.Contains(err.Error(), "web.acme.uz") {
		t.Fatalf("a move with a domain: %v", err)
	}
	st.DB.Exec(`INSERT INTO env_moves (id, environment_id, from_server_id, to_server_id, state, step, created_at, updated_at) VALUES ('mv_1', 'env_q2', 'srv_1', 'srv_2', 'running', 'stop', 1, 1)`)
	if _, err := a.PlanCopy(ctx, "svc_web", "acme", "q2"); err == nil || !strings.Contains(err.Error(), "moving") {
		t.Fatalf("a moving environment: %v", err)
	}
}

func TestCopyServiceLimit(t *testing.T) {
	st, a := seedMove(t, true)
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_qa', 'prj_1', 'qa', 'srv_1', 1, 1)`)
	n, _ := st.Q.CountLiveServices(ctx)
	a.Limits = Limits{Services: int(n)}
	if _, err := a.CopyService(ctx, "svc_api", "acme", "qa", nil, "", false, "owner"); err == nil || !strings.Contains(err.Error(), "services in the beta") {
		t.Fatalf("%v", err)
	}
	if _, err := a.CopyService(ctx, "svc_api", "acme", "qa", nil, "", true, "owner"); err != nil {
		t.Fatalf("a move: %v", err)
	}
}

func TestCopyServiceMinors(t *testing.T) {
	st, a := seedMove(t, true)
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_qa', 'prj_1', 'qa', 'srv_1', 1, 1), ('env_eu', 'prj_1', 'eu', 'srv_2', 1, 1)`)
	st.DB.Exec(`INSERT INTO environments (id, project_id, name, server_id, preview_of, created_at, updated_at) VALUES ('env_pr', 'prj_1', 'pr-7', 'srv_1', 'env_s', 1, 1)`)
	st.DB.Exec(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, deleted_at, created_at, updated_at)
		VALUES ('svc_old', 'env_qa', 'api', 'web', 'https://x/y', 'main', 8080, '/', 1, 1, 5, 1, 1)`)
	if _, err := a.PlanCopy(ctx, "svc_api", "acme", "qa"); err == nil || !strings.Contains(err.Error(), "trash") {
		t.Fatalf("a trashed name: %v", err)
	}
	if _, err := a.PlanCopy(ctx, "svc_api", "acme", "pr-7"); err == nil || !strings.Contains(err.Error(), "preview") {
		t.Fatalf("a preview environment: %v", err)
	}
	if _, err := a.CopyService(ctx, "svc_api", "acme", "eu", map[string]Reseal{"var_a": {AgentBox: "b", UpdatedAt: 11}}, "PUBK", false, "owner"); err == nil || !strings.Contains(err.Error(), "reload") {
		t.Fatalf("a variable changed since the plan: %v", err)
	}
	if _, err := a.CopyService(ctx, "svc_api", "acme", "eu", map[string]Reseal{"var_a": {AgentBox: "b", UpdatedAt: 12}}, "OLDKEY", false, "owner"); err == nil || !strings.Contains(err.Error(), "reload") {
		t.Fatalf("sealed to a key the server no longer has: %v", err)
	}
}
