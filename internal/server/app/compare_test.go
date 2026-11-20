package app

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type echoHasher struct {
	calls   []string
	args    []proto.HashArgs
	offline string
}

func (e *echoHasher) Send(ctx context.Context, serverID string, c proto.Command) (proto.Result, error) {
	if serverID == e.offline {
		<-ctx.Done()
		return proto.Result{}, ctx.Err()
	}
	var a proto.HashArgs
	json.Unmarshal(c.Args, &a)
	e.calls, e.args = append(e.calls, serverID), append(e.args, a)
	var d proto.HashData
	for _, v := range a.Vars {
		d.Hashes = append(d.Hashes, "h:"+v.Box)
	}
	b, _ := json.Marshal(d)
	return proto.Result{Key: c.Key, OK: true, Data: b}, nil
}

func seedTwoEnvs(t *testing.T) (*store.Store, *App) {
	st, _ := seed(t)
	ctx := context.Background()
	st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_2", Name: "hk1", PublicIp: "5.6.7.8", CreatedAt: 1, UpdatedAt: 1})
	st.Q.UpsertEnvironment(ctx, db.UpsertEnvironmentParams{ID: "env_2", ProjectID: "prj_1", Name: "staging", ServerID: "srv_2", CreatedAt: 1, UpdatedAt: 1})
	st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_2", EnvironmentID: "env_2", Name: "api", Kind: "web",
		Repo: "https://x/y", Branch: "main", Port: 10000, HealthPath: "/", HealthTimeoutMs: 60000, DrainTimeoutMs: 30000, CreatedAt: 1, UpdatedAt: 1})
	return st, New(st)
}

func setVar(t *testing.T, st *store.Store, env, svc, key, box, ref string) {
	t.Helper()
	if err := st.Q.UpsertEnvVar(context.Background(), db.UpsertEnvVarParams{ID: store.NewID("var_"), EnvironmentID: env, ServiceID: svc,
		Key: key, AgentBox: box, RecoveryBox: "r", Ref: ref, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestCompareStates(t *testing.T) {
	st, a := seedTwoEnvs(t)
	setVar(t, st, "env_2", "svc_2", "SAME", "x", "")
	setVar(t, st, "env_1", "svc_1", "SAME", "x", "")
	setVar(t, st, "env_2", "", "DIFF", "a", "")
	setVar(t, st, "env_1", "", "DIFF", "b", "")
	setVar(t, st, "env_2", "", "SENTRY_DSN", "s", "")
	setVar(t, st, "env_1", "", "ONLY_PROD", "p", "")
	setVar(t, st, "env_2", "svc_2", "DATABASE_URL", "", "acme-db.url")
	setVar(t, st, "env_1", "svc_1", "DATABASE_URL", "", "acme-db.url")
	setVar(t, st, "env_2", "", "MIXED", "", "acme-db.url")
	setVar(t, st, "env_1", "", "MIXED", "m", "")
	rows, err := a.Compare(context.Background(), &echoHasher{}, "acme", "staging", "production",
		map[string]string{"staging": "ks", "production": "kp"}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	want := []DiffRow{{"", "DIFF", "different"}, {"", "MIXED", "different"}, {"", "ONLY_PROD", "only_right"}, {"", "SENTRY_DSN", "only_left"},
		{"api", "DATABASE_URL", "same"}, {"api", "SAME", "same"}}
	if !slices.Equal(rows, want) {
		t.Fatalf("%+v", rows)
	}
}

func TestCompareSendsEachEnvToItsServer(t *testing.T) {
	st, a := seedTwoEnvs(t)
	setVar(t, st, "env_2", "", "A", "sa", "")
	setVar(t, st, "env_1", "", "B", "pb", "")
	setVar(t, st, "env_1", "", "R", "", "acme-db.url")
	h := &echoHasher{}
	a.Compare(context.Background(), h, "acme", "staging", "production", map[string]string{"staging": "ks", "production": "kp"}, "owner")
	if !slices.Equal(h.calls, []string{"srv_2", "srv_1"}) || h.args[0].Key != "ks" || h.args[1].Key != "kp" ||
		len(h.args[0].Vars) != 1 || h.args[0].Vars[0].Box != "sa" || len(h.args[1].Vars) != 1 || h.args[1].Vars[0].Box != "pb" {
		t.Fatalf("%v %+v", h.calls, h.args)
	}
}

func TestCompareRecordsNoHashes(t *testing.T) {
	st, a := seedTwoEnvs(t)
	setVar(t, st, "env_2", "", "A", "secretbox", "")
	if _, err := a.Compare(context.Background(), &echoHasher{}, "acme", "staging", "production", map[string]string{"staging": "ks", "production": "kp"}, "owner"); err != nil {
		t.Fatal(err)
	}
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'env.compared'`).Scan(&n)
	var leak int
	st.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM events WHERE body_json LIKE '%h:%') + (SELECT COUNT(*) FROM outbox WHERE body LIKE '%h:%')`).Scan(&leak)
	if n != 1 || leak != 0 {
		t.Fatalf("events %d, leaks %d", n, leak)
	}
}

func TestCompareOffline(t *testing.T) {
	st, a := seedTwoEnvs(t)
	setVar(t, st, "env_2", "", "A", "x", "")
	a.CompareTimeout = 50e6
	_, err := a.Compare(context.Background(), &echoHasher{offline: "srv_2"}, "acme", "staging", "production", map[string]string{"staging": "ks", "production": "kp"}, "owner")
	if err == nil || err.Error() != "hk1 is offline; values cannot be compared now." {
		t.Fatal(err)
	}
}

func TestCompareNeedsKeys(t *testing.T) {
	_, a := seedTwoEnvs(t)
	_, err := a.Compare(context.Background(), &echoHasher{}, "acme", "staging", "production", map[string]string{"staging": "ks"}, "owner")
	if err == nil || err.Error() != "A comparison key is needed for production." {
		t.Fatal(err)
	}
}

func TestCompareSharedVars(t *testing.T) {
	st, a := seedTwoEnvs(t)
	for _, v := range [][4]string{{"team", "srv_1", "TEAM", "t"}, {"team", "srv_2", "TEAM", "t"}, {"server", "srv_1", "SMTP_HOST", "m"},
		{"team", "srv_1", "OVER", "x"}, {"team", "srv_2", "OVER", "x"}} {
		st.DB.Exec(`INSERT INTO shared_vars (id, scope, server_id, key, agent_box, recovery_box, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 'r', 1, 1)`, store.NewID("shv_"), v[0], v[1], v[2], v[3])
	}
	setVar(t, st, "env_1", "", "OVER", "", "acme-db.url")
	rows, err := a.Compare(context.Background(), &echoHasher{}, "acme", "staging", "production", map[string]string{"staging": "ks", "production": "kp"}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	want := []DiffRow{{"", "OVER", "different"}, {"", "SMTP_HOST", "only_right"}, {"", "TEAM", "same"}}
	if !slices.Equal(rows, want) {
		t.Fatalf("%+v", rows)
	}
}
