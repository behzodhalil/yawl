package workers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeStatuser struct {
	calls       []string
	refuse      bool
	unconnected bool
	fail        bool
}

type refused struct{ unconnected bool }

func (refused) Error() string       { return "no" }
func (refused) Refused() bool       { return true }
func (r refused) Unconnected() bool { return r.unconnected }

func (f *fakeStatuser) Status(_ context.Context, key, sha, state, ctx, desc, target string) error {
	f.calls = append(f.calls, strings.Join([]string{key, sha, state, ctx, desc, target}, " | "))
	if f.refuse || f.unconnected {
		return refused{unconnected: f.unconnected}
	}
	if f.fail {
		return errors.New("timeout")
	}
	return nil
}

func TestCommitStatus(t *testing.T) {
	st, svc := seed(t, "web")
	st.DB.Exec(`UPDATE services SET repo = 'https://github.com/Acme/API.git' WHERE id = ?`, svc)
	now := time.Now()
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_1', ?, 'c0ffee', 'building', ?, ?)`, svc, now.UnixMilli(), now.UnixMilli())
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_old', ?, 'beef', 'live', ?, ?)`, svc, now.Add(-48*time.Hour).UnixMilli(), 1)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_sup', ?, 'abc', 'superseded', ?, ?)`, svc, now.UnixMilli(), now.UnixMilli())
	f := &fakeStatuser{}
	w := &CommitStatus{St: st, S: f, PanelURL: "https://panel.acme.uz", Now: time.Now}
	ctx := context.Background()
	w.Tick(ctx)
	if len(f.calls) != 1 || f.calls[0] != "acme/api | c0ffee | pending | yawl/production/api | Deploying | https://panel.acme.uz/p/acme/production/s/api/deploys/dep_1" {
		t.Fatalf("%q", f.calls)
	}
	w.Tick(ctx)
	st.DB.Exec(`UPDATE deploys SET state = 'live' WHERE id = 'dep_1'`)
	w.Tick(ctx)
	w.Tick(ctx)
	if len(f.calls) != 2 || !strings.Contains(f.calls[1], "| success | yawl/production/api | Live |") {
		t.Fatalf("%q", f.calls)
	}
	st.DB.Exec(`UPDATE deploys SET state = 'failed', cause = 'health check failed: GET /health answered 500' WHERE id = 'dep_1'`)
	f.refuse = true
	w.Tick(ctx)
	w.Tick(ctx)
	if len(f.calls) != 3 || !strings.Contains(f.calls[2], "| failure | yawl/production/api | health check failed: GET /health answered 500 |") {
		t.Fatalf("refused, asked again: %q", f.calls)
	}
	if note, _ := st.Q.GetSetting(ctx, "commitstatus.refused.github"); note == "" {
		t.Fatal("no note for the settings page")
	}
	f.refuse = false
	st.DB.Exec(`UPDATE deploys SET state = 'live' WHERE id = 'dep_1'`)
	w.Tick(ctx)
	if note, err := st.Q.GetSetting(ctx, "commitstatus.refused.github"); err == nil {
		t.Fatalf("note kept after a success: %q", note)
	}
}

func TestCommitStatusUnconnected(t *testing.T) {
	st, svc := seed(t, "web")
	st.DB.Exec(`UPDATE services SET repo = 'https://codeberg.org/a/b' WHERE id = ?`, svc)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_1', ?, 'c0ffee', 'live', ?, 1)`, svc, time.Now().UnixMilli())
	f := &fakeStatuser{unconnected: true}
	w := &CommitStatus{St: st, S: f, PanelURL: "https://p", Now: time.Now}
	w.Tick(context.Background())
	w.Tick(context.Background())
	var notes int
	st.DB.QueryRow(`SELECT COUNT(*) FROM settings WHERE key LIKE 'commitstatus.%'`).Scan(&notes)
	if notes != 0 {
		t.Fatalf("%d notes for an unconnected forge", notes)
	}
	if len(f.calls) != 1 {
		t.Fatalf("asked %d times", len(f.calls))
	}
}

func TestCommitStatusRetries(t *testing.T) {
	st, svc := seed(t, "web")
	st.DB.Exec(`UPDATE services SET repo = 'https://gitlab.com/team/api' WHERE id = ?`, svc)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_1', ?, 'c0ffee', 'live', ?, 1)`, svc, time.Now().UnixMilli())
	f := &fakeStatuser{fail: true}
	at := time.Now()
	w := &CommitStatus{St: st, S: f, PanelURL: "https://p", Now: func() time.Time { return at }}
	w.Tick(context.Background())
	f.fail = false
	at = at.Add(time.Minute)
	w.Tick(context.Background())
	w.Tick(context.Background())
	if len(f.calls) != 2 || !strings.HasPrefix(f.calls[1], "gitlab.com/team/api | c0ffee | success") {
		t.Fatalf("%q", f.calls)
	}
}

func TestCommitStatusBacksOff(t *testing.T) {
	st, svc := seed(t, "web")
	st.DB.Exec(`UPDATE services SET repo = 'https://gitlab.com/team/api' WHERE id = ?`, svc)
	at := time.Now()
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_1', ?, 'c0ffee', 'live', ?, 1)`, svc, at.UnixMilli())
	f := &fakeStatuser{fail: true}
	w := &CommitStatus{St: st, S: f, PanelURL: "https://p", Now: func() time.Time { return at }}
	for i := 0; i < 5; i++ {
		at = at.Add(5 * time.Second)
		w.Tick(context.Background())
	}
	if len(f.calls) > 3 {
		t.Fatalf("asked %d times in 25 s", len(f.calls))
	}
}

func TestCommitStatusWords(t *testing.T) {
	st, svc := seed(t, "web")
	st.DB.Exec(`UPDATE services SET repo = 'https://github.com/acme/api' WHERE id = ?`, svc)
	now := time.Now().UnixMilli()
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, started_at, finished_at, created_at, updated_at) VALUES ('dep_1', ?, 'c0ffee', 'live', ?, ?, ?, 1)`, svc, now-72000, now, now)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, cause, status_sent, created_at, updated_at) VALUES ('dep_2', ?, 'beef', 'superseded', 'Canceled.', 'pending', ?, 1)`, svc, now+1)
	f := &fakeStatuser{}
	(&CommitStatus{St: st, S: f, PanelURL: "https://p", Now: time.Now}).Tick(context.Background())
	if len(f.calls) != 2 || !strings.Contains(f.calls[0], "| Live in 1m 12s |") || !strings.Contains(f.calls[1], "| Canceled |") {
		t.Fatalf("%q", f.calls)
	}
}
