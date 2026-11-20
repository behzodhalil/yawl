package workers

import (
	"context"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestServiceVarsShared(t *testing.T) {
	st, svc := seed(t, "web")
	ex := func(q string) {
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	st.Q.UpsertServer(context.Background(), db.UpsertServerParams{ID: "srv_2", Name: "hk1", PublicIp: "5.6.7.8", CreatedAt: 1, UpdatedAt: 1})
	ex(`INSERT INTO shared_vars (id, scope, server_id, key, agent_box, recovery_box, created_at, updated_at) VALUES
		('sh1', 'team', 'srv_1', 'A', 'team-a', 'r', 1, 1), ('sh2', 'team', 'srv_1', 'T', 'team-t', 'r', 1, 1),
		('sh3', 'server', 'srv_1', 'A', 'server-a', 'r', 1, 1), ('sh4', 'server', 'srv_1', 'B', 'server-b', 'r', 1, 1),
		('sh5', 'server', 'srv_2', 'C', 'other-server', 'r', 1, 1), ('sh6', 'team', 'srv_2', 'T', 'team-t-hk1', 'r', 1, 1)`)
	ex(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, created_at, updated_at) VALUES
		('e1', 'env_1', '', 'B', 'env-b', 'r', 1, 1), ('e2', 'env_1', '` + svc + `', 'A', '', 'r', 1, 1)`)
	st.DB.Exec(`UPDATE env_vars SET agent_box = 'svc-a' WHERE id = 'e2'`)
	vars, err := ServiceVars(context.Background(), st.Q, "env_1", svc)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range vars {
		got[v.Key] = v.Box
	}
	if got["A"] != "svc-a" || got["B"] != "env-b" || got["T"] != "team-t" || got["C"] != "" || len(got) != 3 {
		t.Fatalf("%v", got)
	}
}

func TestServiceVarsStaleShared(t *testing.T) {
	st, svc := seed(t, "web")
	st.DB.Exec(`UPDATE servers SET agent_pubkey = 'new-key' WHERE id = 'srv_1'`)
	st.DB.Exec(`INSERT INTO shared_vars (id, scope, server_id, key, agent_box, recovery_box, sealed_to, created_at, updated_at) VALUES ('sh1', 'team', 'srv_1', 'SENTRY_DSN', 'b', 'r', 'old-key', 1, 1)`)
	_, err := ServiceVars(context.Background(), st.Q, "env_1", svc)
	if c, ok := err.(errCause); !ok || !strings.Contains(string(c), "SENTRY_DSN") || !strings.Contains(string(c), "set it again in Shared variables") {
		t.Fatalf("%v", err)
	}
}

func TestServiceVarsSource(t *testing.T) {
	st, svc := seed(t, "web")
	st.DB.Exec(`INSERT INTO shared_vars (id, scope, server_id, key, agent_box, recovery_box, created_at, updated_at) VALUES ('sh1', 'team', 'srv_1', 'TEAM', 'tb', 'r', 1, 1)`)
	st.DB.Exec(`INSERT INTO secret_sources (id, environment_id, provider, url, path, workspace, env, agent_box, recovery_box, created_at, updated_at)
		VALUES ('src1', 'env_1', 'vault', 'https://vault.acme', 'secret/data/shop', '', '', 'token-box', 'r', 1, 1)`)
	st.DB.Exec(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, created_at, updated_at) VALUES ('e1', 'env_1', '', 'OWN', 'ob', 'r', 1, 1)`)
	vars, err := ServiceVars(context.Background(), st.Q, "env_1", svc)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, v := range vars {
		keys = append(keys, v.Key)
	}
	if strings.Join(keys, ",") != "TEAM,vault,OWN" || vars[1].Source == nil || vars[1].Source.Path != "secret/data/shop" || vars[1].Box != "token-box" {
		t.Fatalf("%v %+v", keys, vars)
	}
}
