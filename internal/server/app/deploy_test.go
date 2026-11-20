package app

import (
	"context"
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func seed(t *testing.T) (*store.Store, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	srv, _ := st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_1", Name: "hel1", PublicIp: "1.2.3.4", CreatedAt: 1, UpdatedAt: 1})
	prj, _ := st.Q.UpsertProject(ctx, db.UpsertProjectParams{ID: "prj_1", Name: "acme", CreatedAt: 1, UpdatedAt: 1})
	env, _ := st.Q.UpsertEnvironment(ctx, db.UpsertEnvironmentParams{ID: "env_1", ProjectID: prj, Name: "production", ServerID: srv, CreatedAt: 1, UpdatedAt: 1})
	svc, err := st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_1", EnvironmentID: env, Name: "api", Kind: "web",
		Repo: "https://x/y", Branch: "main", Port: 10000, HealthPath: "/", HealthTimeoutMs: 60000, DrainTimeoutMs: 30000, CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	return st, svc
}

func TestQueueSupersedes(t *testing.T) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	first, _ := a.Deploy(ctx, svc, "", "owner")
	st.Q.StartDeploy(ctx, db.StartDeployParams{State: "building", ID: first})
	second, _ := a.Deploy(ctx, svc, "", "owner")
	third, err := a.Deploy(ctx, svc, "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{first: "building", second: "superseded", third: "queued"} {
		d, _ := st.Q.GetDeploy(ctx, id)
		if d.State != want {
			t.Errorf("%s: %s, want %s", id, d.State, want)
		}
	}
}

func TestDeployWritesEvent(t *testing.T) {
	st, svc := seed(t)
	if _, err := New(st).Deploy(context.Background(), svc, "abc", "owner"); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.Q.CountEvents(context.Background()); n != 1 {
		t.Fatalf("%d events", n)
	}
}

func TestFreeHost(t *testing.T) {
	if got := FreeHost("api", "production", netip.MustParseAddr("1.2.3.4")); got != "api-production.1-2-3-4.sslip.io" {
		t.Fatal(got)
	}
}
