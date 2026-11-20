package app

import (
	"context"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestFreePrivatePort(t *testing.T) {
	st, _ := seed(t)
	ctx := context.Background()
	for _, s := range []db.UpsertServiceParams{
		{ID: "svc_a", EnvironmentID: "env_1", Name: "a-db", Kind: "postgres", OutsidePort: 15432, CreatedAt: 1, UpdatedAt: 1},
		{ID: "svc_b", EnvironmentID: "env_1", Name: "b-db", Kind: "postgres", CreatedAt: 1, UpdatedAt: 1},
	} {
		if _, err := st.Q.UpsertService(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	st.DB.Exec(`UPDATE services SET private_port = 15433 WHERE id = 'svc_b'`)
	if p, err := FreePrivatePort(ctx, st.Q, "srv_1", 0); err != nil || p != 15434 {
		t.Fatal(p, err)
	}
	if p, _ := FreePrivatePort(ctx, st.Q, "srv_1", 15440); p != 15440 {
		t.Fatalf("a free port it already has is kept: %d", p)
	}
	if p, _ := FreePrivatePort(ctx, st.Q, "srv_1", 15432); p != 15434 {
		t.Fatalf("an outside port is not kept: %d", p)
	}
}

func TestOutsidePortNotAPrivatePort(t *testing.T) {
	st, _ := seed(t)
	a := New(st)
	st.DB.Exec(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, private_port, version, backup_at, created_at, updated_at)
		VALUES ('svc_p', 'env_1', 'p-db', 'postgres', '', '', 0, '', 0, 0, 15432, 17, '03:00', 1, 1)`)
	y := "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n  - {name: p-db, kind: postgres}\n  - {name: q-db, kind: postgres, outside_port: 15432, allow: [1.2.3.4/32]}\n"
	if _, err := a.Apply(context.Background(), parse(t, y), "owner", false); err == nil || !strings.Contains(err.Error(), "q-db: outside_port 15432 is the private network port of acme-p-db on hel1.") {
		t.Fatal(err)
	}
}
