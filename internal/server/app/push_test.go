package app

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func pushSeed(t *testing.T) (*App, *db.Queries) {
	st, _ := seed(t)
	ctx := context.Background()
	for _, s := range []struct{ id, name, repo, branch string }{
		{"svc_api", "gh-api", "https://github.com/Acme/API", "main"},
		{"svc_web", "gh-web", "git@github.com:acme/api.git", "main"},
		{"svc_stg", "gh-stg", "https://github.com/acme/api", "staging"},
		{"svc_off", "gh-off", "https://github.com/acme/api", "main"},
	} {
		auto := int64(1)
		if s.id == "svc_off" {
			auto = 0
		}
		if _, err := st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: s.id, EnvironmentID: "env_1", Name: s.name, Kind: "web",
			Repo: s.repo, Branch: s.branch, Port: 10000, HealthPath: "/", HealthTimeoutMs: 60000, DrainTimeoutMs: 30000,
			AutoDeploy: auto, CreatedAt: 1, UpdatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	a := New(st)
	tick := time.Unix(1_800_000_000, 0)
	a.Now = func() time.Time { tick = tick.Add(time.Second); return tick }
	return a, st.Q
}

func serviceOf(t *testing.T, q *db.Queries, ids []string) []string {
	var out []string
	for _, id := range ids {
		d, err := q.GetDeploy(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if d.CommitSha != "c0ffee" {
			t.Fatalf("deploy %s commit %q", id, d.CommitSha)
		}
		out = append(out, d.ServiceID)
	}
	slices.Sort(out)
	return out
}

func TestPushQueuesMatching(t *testing.T) {
	a, q := pushSeed(t)
	ids, err := a.Push(context.Background(), "d-1", "acme/api", "main", "c0ffee", "behzod")
	if err != nil {
		t.Fatal(err)
	}
	if got := serviceOf(t, q, ids); !slices.Equal(got, []string{"svc_api", "svc_web"}) {
		t.Fatalf("queued for %v", got)
	}
	evs := testEvents(t, a)
	if !slices.Contains(evs, "deploy.queued by GitHub push by behzod") {
		t.Fatalf("events %v", evs)
	}
}

func TestPushDuplicateQueuesOnce(t *testing.T) {
	a, _ := pushSeed(t)
	ctx := context.Background()
	first, _ := a.Push(ctx, "d-1", "Acme/API", "main", "c0ffee", "behzod")
	again, err := a.Push(ctx, "d-1", "acme/api", "main", "c0ffee", "behzod")
	if err != nil || len(first) != 2 || len(again) != 0 {
		t.Fatalf("first %v, redelivery %v (%v)", first, again, err)
	}
	same, _ := a.Push(ctx, "d-2", "acme/api", "main", "c0ffee", "behzod")
	if len(same) != 0 {
		t.Fatalf("a second push of the same head queued %v", same)
	}
}

func TestPushIgnoredBranchAndManual(t *testing.T) {
	a, _ := pushSeed(t)
	ids, err := a.Push(context.Background(), "d-1", "acme/api", "feature-x", "c0ffee", "behzod")
	if err != nil || len(ids) != 0 {
		t.Fatalf("a push to another branch queued %v (%v)", ids, err)
	}
	ids, _ = a.Push(context.Background(), "d-2", "acme/other", "main", "c0ffee", "behzod")
	if len(ids) != 0 {
		t.Fatalf("a push to another repo queued %v", ids)
	}
}

func testEvents(t *testing.T, a *App) []string {
	rows, err := a.St.DB.Query(`SELECT kind || ' by ' || actor FROM events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func TestPushForcePushBackDeploys(t *testing.T) {
	a, _ := pushSeed(t)
	ctx := context.Background()
	a.Push(ctx, "d-1", "acme/api", "main", "aaaa", "behzod")
	a.St.DB.Exec(`UPDATE deploys SET state = 'live'`)
	a.Push(ctx, "d-2", "acme/api", "main", "bbbb", "behzod")
	a.St.DB.Exec(`UPDATE deploys SET state = 'live' WHERE commit_sha = 'bbbb'`)
	ids, err := a.Push(ctx, "d-3", "acme/api", "main", "aaaa", "behzod")
	if err != nil || len(ids) != 2 {
		t.Fatalf("force-push back to aaaa queued %v (%v), want 2", ids, err)
	}
}

func TestPushRedeliveryOfSupersededPush(t *testing.T) {
	a, q := pushSeed(t)
	ctx := context.Background()
	a.Push(ctx, "d-1", "acme/api", "main", "aaaa", "behzod")
	newer, _ := a.Push(ctx, "d-2", "acme/api", "main", "bbbb", "behzod")
	if ids, _ := a.Push(ctx, "d-1", "acme/api", "main", "aaaa", "behzod"); len(ids) != 0 {
		t.Fatalf("redelivery of d-1 queued %v", ids)
	}
	for _, id := range newer {
		if d, _ := q.GetDeploy(ctx, id); d.State != "queued" {
			t.Fatalf("newer deploy %s is %s", id, d.State)
		}
	}
}

func TestPushRecordsPusher(t *testing.T) {
	a, q := pushSeed(t)
	ids, err := a.Push(context.Background(), "d-9", "acme/api", "main", "c0ffee", "behzod")
	if err != nil || len(ids) == 0 {
		t.Fatal(ids, err)
	}
	if d, _ := q.GetDeploy(context.Background(), ids[0]); d.Actor != "behzod" {
		t.Fatalf("actor %q", d.Actor)
	}
}

func TestPushSkipWords(t *testing.T) {
	a, _ := pushSeed(t)
	for i, msg := range []string{"Fix README [skip ci]", "docs [ci skip]", "wip [skip deploy]", "x [skip yawl]"} {
		ids, err := a.PushVia(context.Background(), "GitHub", fmt.Sprint("d-", i), "acme/api", "main", fmt.Sprint("c", i), "behzod", PushInfo{Message: msg})
		if err != nil || len(ids) != 0 {
			t.Fatalf("%q: %v %v", msg, ids, err)
		}
	}
}

func TestPushWatchPaths(t *testing.T) {
	a, q := pushSeed(t)
	ctx := context.Background()
	a.St.DB.Exec(`UPDATE services SET watch_json = '["api/**","go.mod"]' WHERE id = 'svc_api'`)
	a.St.DB.Exec(`UPDATE services SET watch_json = '["web/*.ts"]' WHERE id = 'svc_web'`)
	ids, _ := a.PushVia(ctx, "GitHub", "w-1", "acme/api", "main", "c0ffee", "b", PushInfo{Files: []string{"api/internal/x.go", "README.md"}})
	if got := serviceOf(t, q, ids); !slices.Equal(got, []string{"svc_api"}) {
		t.Fatalf("%v", got)
	}
	ids, _ = a.PushVia(ctx, "GitHub", "w-2", "acme/api", "main", "beef", "b", PushInfo{Files: []string{"docs/a.md"}, Partial: true})
	if len(ids) != 2 {
		t.Fatalf("a cut-short list must deploy both: %v", ids)
	}
}

func TestWatchMatch(t *testing.T) {
	for _, c := range []struct {
		glob, file string
		want       bool
	}{{"api/**", "api/a/b.go", true}, {"api/**", "apix/a.go", false}, {"go.mod", "go.mod", true}, {"web/*.ts", "web/a.ts", true}, {"web/*.ts", "web/x/a.ts", false}, {"**/*.md", "docs/a/b.md", true}} {
		if got := watchMatch(c.glob, c.file); got != c.want {
			t.Errorf("%s %s: %v", c.glob, c.file, got)
		}
	}
}

func TestWatchMatchForms(t *testing.T) {
	for _, c := range []struct {
		glob, file string
		want       bool
	}{{"./api/**", "api/a.go", true}, {"api/", "api/x/y.go", true}, {"api/**/*.go", "api/a/b/c.go", true}, {"api/**/*.go", "api/a.md", false}, {"**", "x/y", true}} {
		if got := watchMatch(c.glob, c.file); got != c.want {
			t.Errorf("%s %s: %v", c.glob, c.file, got)
		}
	}
}

func TestPushSkippedRecorded(t *testing.T) {
	a, _ := pushSeed(t)
	ctx := context.Background()
	a.PushVia(ctx, "GitHub", "s-1", "acme/api", "main", "c1", "behzod", PushInfo{Message: "docs [skip ci]"})
	a.St.DB.Exec(`UPDATE services SET watch_json = '["web/**"]' WHERE id = 'svc_api'`)
	a.PushVia(ctx, "GitHub", "s-2", "acme/api", "main", "c2", "behzod", PushInfo{Files: []string{"docs/a.md"}})
	var words, watch int
	a.St.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'push.skipped' AND json_extract(body_json, '$.delivery') = 's-1'`).Scan(&words)
	a.St.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'push.skipped' AND subject_id = 'svc_api' AND json_extract(body_json, '$.delivery') = 's-2'`).Scan(&watch)
	if words != 1 || watch != 1 {
		t.Fatalf("skip words %d, watch %d", words, watch)
	}
}

func TestNoCacheCarriesOver(t *testing.T) {
	a, q := pushSeed(t)
	ctx := context.Background()
	id, err := a.DeployNoCache(ctx, "svc_api", "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := q.GetDeploy(ctx, id); d.NoCache != 1 {
		t.Fatal("no_cache not set")
	}
	ids, _ := a.PushVia(ctx, "GitHub", "n-1", "acme/api", "main", "c0ffee", "b", PushInfo{})
	for _, id := range ids {
		if d, _ := q.GetDeploy(ctx, id); d.ServiceID == "svc_api" && d.NoCache != 1 {
			t.Fatal("the newer push dropped the no-cache ask")
		}
	}
}
