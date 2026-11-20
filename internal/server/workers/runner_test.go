package workers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/behzodhalil/yawl/internal/cron"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/deploy"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type fakeSender struct {
	mu       sync.Mutex
	results  map[string]proto.Result
	sent     []proto.Command
	to       []string
	fail     map[string]string
	failKey  func(key string) bool
	n        int
	warnings []string
}

func (f *fakeSender) Send(_ context.Context, serverID string, c proto.Command) (proto.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if res, ok := f.results[c.Key]; ok && c.Key != "" && !proto.Unrecorded(c.Name) {
		return res, nil
	}
	res := f.answer(c)
	if f.results == nil {
		f.results = map[string]proto.Result{}
	}
	f.results[c.Key] = res
	f.sent = append(f.sent, c)
	f.to = append(f.to, serverID)
	return res, nil
}

func (f *fakeSender) answer(c proto.Command) proto.Result {
	if cause, ok := f.fail[c.Name]; ok {
		return proto.Result{Key: c.Key, Cause: cause}
	}
	if f.failKey != nil && f.failKey(c.Key) {
		return proto.Result{Key: c.Key, Cause: "permission denied"}
	}
	var data any
	switch c.Name {
	case proto.CmdBuild:
		var a proto.BuildArgs
		json.Unmarshal(c.Args, &a)
		data = proto.BuildData{Image: a.Image, Commit: "abc", Subject: "Fix invoice rounding", Warnings: f.warnings}
	case proto.CmdPull:
		data = proto.PullData{Digest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	case proto.CmdEnsureDatabase:
		var a proto.EnsureDatabaseArgs
		json.Unmarshal(c.Args, &a)
		var e proto.EnsureDatabaseData
		if a.Password == "" {
			e = proto.EnsureDatabaseData{PasswordBox: "pw-a", PasswordRecoveryBox: "pw-r", URLBox: "url-a", URLRecoveryBox: "url-r"}
		}
		if a.OutsidePort != 0 {
			e.OutsideURLBox, e.OutsideURLRecoveryBox = "out-a", "out-r"
		}
		if a.ShareHere {
			e.Here = "here-box"
		}
		for srv := range a.ShareWith {
			if e.Shares == nil {
				e.Shares = map[string]string{}
			}
			e.Shares[srv] = "share-" + srv
		}
		data = e
	case proto.CmdBackup:
		data = proto.BackupData{Size: 1234, SHA256: "abc123"}
	case proto.CmdRestore:
		data = struct{}{}
	case proto.CmdSnapshot:
		var a proto.SnapshotArgs
		json.Unmarshal(c.Args, &a)
		data = proto.SnapshotData{Path: "/var/lib/yawl/snapshots/yawl-acme-production-" + a.Name + "/" + strings.Split(c.Key, ":")[0] + ".dump", Size: 42}
	case proto.CmdDeleteBackups:
		var a proto.DeleteBackupsArgs
		json.Unmarshal(c.Args, &a)
		data = proto.DeleteBackupsData{Deleted: a.Keys}
	case proto.CmdStart:
		f.n++
		data = proto.StartData{Container: "new" + string(rune('0'+f.n))}
	}
	b, _ := json.Marshal(data)
	return proto.Result{Key: c.Key, OK: true, Data: b}
}

func (f *fakeSender) names() []string {
	var out []string
	for _, c := range f.sent {
		out = append(out, c.Name)
	}
	return out
}

func seed(t *testing.T, kind string) (*store.Store, string) {
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
	port := int64(10000)
	if kind == "worker" {
		port = 0
	}
	svc, err := st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_1", EnvironmentID: env, Name: "api", Kind: kind,
		Repo: "https://x/y", Branch: "main", Port: port, HealthPath: "/", HealthTimeoutMs: 60000, DrainTimeoutMs: 30000, CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	return st, svc
}

func deployOnce(t *testing.T, st *store.Store, svc string, gw Sender) (string, *Runner) {
	t.Helper()
	ctx := context.Background()
	id, err := app.New(st).Deploy(ctx, svc, "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	r := NewRunner(st, gw, nil)
	if err := r.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	return id, r
}

func TestRunHappyPath(t *testing.T) {
	st, svc := seed(t, "web")
	gw := &fakeSender{}
	id, _ := deployOnce(t, st, svc, gw)
	ctx := context.Background()
	d, _ := st.Q.GetDeploy(ctx, id)
	s, _ := st.Q.GetService(ctx, svc)
	if d.State != "live" || s.CurrentDeployID.String != id || d.Image != "yawl/acme-production-api:"+id {
		t.Fatalf("deploy %+v service current %v", d, s.CurrentDeployID)
	}
	want := []string{proto.CmdBuild, proto.CmdStart, proto.CmdHealthCheck, proto.CmdSwitchRoute, proto.CmdDrain, proto.CmdPrune}
	if !slices.Equal(gw.names(), want) {
		t.Fatalf("sent %v", gw.names())
	}
	var sw proto.SwitchArgs
	json.Unmarshal(gw.sent[3].Args, &sw)
	if !slices.Equal(sw.Hosts, []string{"api-production.1-2-3-4.sslip.io"}) || sw.Port != 10000 {
		t.Fatalf("switch %+v", sw)
	}
	if rows, _ := st.Q.ListOutbox(ctx); len(rows) != 0 {
		t.Fatalf("outbox has %d rows on success", len(rows))
	}
}

func TestRunCrashKeepsOld(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	first, _ := deployOnce(t, st, svc, &fakeSender{})
	gw := &fakeSender{fail: map[string]string{proto.CmdHealthCheck: "ModuleNotFoundError: No module named 'jinja2' (exit code 1)"}}
	id, _ := deployOnce(t, st, svc, gw)
	d, _ := st.Q.GetDeploy(ctx, id)
	s, _ := st.Q.GetService(ctx, svc)
	if d.State != "failed" || s.CurrentDeployID.String != first {
		t.Fatalf("state %s, current %s (want %s)", d.State, s.CurrentDeployID.String, first)
	}
	if slices.Contains(gw.names(), proto.CmdSwitchRoute) {
		t.Fatal("traffic was switched to a version that failed its check")
	}
	rows, _ := st.Q.ListOutbox(ctx)
	want := "acme-api deploy failed: ModuleNotFoundError: No module named 'jinja2' (exit code 1). The previous version is still live."
	if len(rows) != 1 || rows[0].Body != want {
		t.Fatalf("outbox %+v", rows)
	}
}

func TestFirstDeployFailureSaysNothingWasLive(t *testing.T) {
	st, svc := seed(t, "web")
	deployOnce(t, st, svc, &fakeSender{fail: map[string]string{proto.CmdHealthCheck: "Your app listens on 127.0.0.1:10000. Bind to 0.0.0.0."}})
	rows, _ := st.Q.ListOutbox(context.Background())
	want := "acme-api deploy failed: Your app listens on 127.0.0.1:10000. Bind to 0.0.0.0. Nothing was live before this deploy."
	if len(rows) != 1 || rows[0].Body != want {
		t.Fatalf("outbox %+v", rows)
	}
}

func TestDrainFailureRollsBack(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	first, _ := deployOnce(t, st, svc, &fakeSender{})
	gw := &fakeSender{fail: map[string]string{proto.CmdDrain: "container stop timed out"}}
	id, _ := deployOnce(t, st, svc, gw)
	d, _ := st.Q.GetDeploy(ctx, id)
	s, _ := st.Q.GetService(ctx, svc)
	if d.State != "rolled_back" || s.CurrentDeployID.String != first {
		t.Fatalf("state %s current %s", d.State, s.CurrentDeployID.String)
	}
	if n := slices.Index(gw.names(), proto.CmdDrain); gw.names()[n+1] != proto.CmdSwitchRoute {
		t.Fatalf("route not switched back: %v", gw.names())
	}
	rows, _ := st.Q.ListOutbox(ctx)
	if len(rows) != 1 || rows[0].Body != "acme-api deploy rolled back: container stop timed out." {
		t.Fatalf("outbox %+v", rows)
	}
}

func TestWorkerStopThenStart(t *testing.T) {
	st, svc := seed(t, "worker")
	deployOnce(t, st, svc, &fakeSender{})
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	want := []string{proto.CmdBuild, proto.CmdStop, proto.CmdStart, proto.CmdHealthCheck, proto.CmdPrune}
	if !slices.Equal(gw.names(), want) {
		t.Fatalf("sent %v", gw.names())
	}
}

func TestResumeAfterRestart(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	id, _ := app.New(st).Deploy(ctx, svc, "", "owner")
	now := time.Now().UnixMilli()
	st.Q.StartDeploy(ctx, db.StartDeployParams{State: "checking", StartedAt: sql.NullInt64{Int64: now, Valid: true}, ID: id})
	st.Q.SetDeployImage(ctx, db.SetDeployImageParams{Image: "yawl/acme-production-api:" + id, ID: id})
	st.Q.SetDeployContainer(ctx, db.SetDeployContainerParams{Container: "c7", ID: id})
	for _, step := range []string{"build", "start"} {
		st.Q.AddDeployStep(ctx, db.AddDeployStepParams{ID: store.NewID("stp_"), DeployID: id, Step: step, StartedAt: now})
		st.Q.FinishDeployStep(ctx, db.FinishDeployStepParams{State: "ok", DeployID: id, Step: step})
	}
	gw := &fakeSender{}
	if err := NewRunner(st, gw, nil).Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	want := []string{proto.CmdHealthCheck, proto.CmdSwitchRoute, proto.CmdDrain, proto.CmdPrune}
	if !slices.Equal(gw.names(), want) {
		t.Fatalf("sent %v", gw.names())
	}
	var hc proto.HealthArgs
	json.Unmarshal(gw.sent[0].Args, &hc)
	if hc.Container != "c7" {
		t.Fatalf("resumed check on %q", hc.Container)
	}
}

func TestTickRunsQueued(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	id, _ := app.New(st).Deploy(ctx, svc, "", "owner")
	r := NewRunner(st, &fakeSender{}, nil)
	r.Tick(ctx)
	r.Wait()
	if d, _ := st.Q.GetDeploy(ctx, id); d.State != "live" {
		t.Fatalf("state %s", d.State)
	}
}

func TestTickResumesAfterCrash(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	id, _ := app.New(st).Deploy(ctx, svc, "", "owner")
	crashed := NewRunner(st, &fakeSender{}, nil)
	if ok, _ := st.TakeLease(ctx, "deploy:"+svc, crashed.holder, time.Now(), leaseTTL); !ok {
		t.Fatal("setup: lease")
	}
	st.Q.StartDeploy(ctx, db.StartDeployParams{State: "building", ID: id})

	r := NewRunner(st, &fakeSender{}, nil)
	r.Tick(ctx)
	r.Wait()
	if d, _ := st.Q.GetDeploy(ctx, id); d.State != "live" {
		t.Fatalf("state %s: the restarted server did not resume", d.State)
	}
}

type cancelSender struct{ cancel context.CancelFunc }

func (c cancelSender) Send(ctx context.Context, _ string, cmd proto.Command) (proto.Result, error) {
	c.cancel()
	<-ctx.Done()
	return proto.Result{}, ctx.Err()
}

func TestShutdownDoesNotFailDeploy(t *testing.T) {
	st, svc := seed(t, "web")
	id, _ := app.New(st).Deploy(context.Background(), svc, "", "owner")
	ctx, cancel := context.WithCancel(context.Background())
	NewRunner(st, cancelSender{cancel}, nil).Run(ctx, id)
	d, _ := st.Q.GetDeploy(context.Background(), id)
	if d.State == "failed" {
		t.Fatalf("shutdown marked the deploy failed: %s", d.Cause)
	}
	if rows, _ := st.Q.ListOutbox(context.Background()); len(rows) != 0 {
		t.Fatal("shutdown sent a failure message")
	}
}

// Review #2: each worker rollback must start a container under a new name, or
// Docker refuses the second one and the worker stays down.
func TestWorkerRollbackNamesAreUnique(t *testing.T) {
	st, svc := seed(t, "worker")
	deployOnce(t, st, svc, &fakeSender{})
	var names []string
	for i := 0; i < 2; i++ {
		gw := &fakeSender{fail: map[string]string{proto.CmdHealthCheck: "boom"}}
		deployOnce(t, st, svc, gw)
		for _, c := range gw.sent {
			if strings.HasSuffix(c.Key, ":rollback-start") {
				var a proto.StartArgs
				json.Unmarshal(c.Args, &a)
				names = append(names, a.Deploy)
			}
		}
	}
	if len(names) != 2 || names[0] == names[1] {
		t.Fatalf("rollback container ids %v", names)
	}
}

func TestResumeAfterSwitchDrainsPrevious(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	first, _ := deployOnce(t, st, svc, &fakeSender{})
	prev, _ := st.Q.GetDeploy(ctx, first)

	id, _ := app.New(st).Deploy(ctx, svc, "", "owner")
	gw := &fakeSender{}
	stop := &stopAfter{inner: gw, after: proto.CmdDrain}
	cctx, cancel := context.WithCancel(ctx)
	stop.cancel = cancel
	NewRunner(st, stop, nil).Run(cctx, id)

	gw2 := &fakeSender{}
	if err := NewRunner(st, gw2, nil).Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	var drain proto.DrainArgs
	for _, c := range gw2.sent {
		if c.Name == proto.CmdDrain {
			json.Unmarshal(c.Args, &drain)
		}
	}
	if drain.Container != prev.Container || drain.Container == "" {
		t.Fatalf("drained %q, want the previous container %q", drain.Container, prev.Container)
	}
}

type stopAfter struct {
	inner  *fakeSender
	after  string
	cancel context.CancelFunc
}

func (s *stopAfter) Send(ctx context.Context, id string, c proto.Command) (proto.Result, error) {
	if ctx.Err() != nil {
		return proto.Result{}, ctx.Err()
	}
	if c.Name == s.after {
		s.cancel()
		return proto.Result{}, context.Canceled
	}
	return s.inner.Send(ctx, id, c)
}

func TestRunnerSendsSealedEnv(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	for _, v := range []db.UpsertEnvVarParams{
		{ID: "v1", EnvironmentID: "env_1", ServiceID: "", Key: "KEY", AgentBox: "shared", RecoveryBox: "r"},
		{ID: "v2", EnvironmentID: "env_1", ServiceID: svc, Key: "KEY", AgentBox: "own", RecoveryBox: "r"},
		{ID: "v3", EnvironmentID: "env_1", ServiceID: "", Key: "ONLY_SHARED", AgentBox: "s2", RecoveryBox: "r"},
	} {
		if err := st.Q.UpsertEnvVar(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	var start proto.StartArgs
	json.Unmarshal(gw.sent[1].Args, &start)
	want := []proto.SealedVar{{Key: "KEY", Box: "own"}, {Key: "ONLY_SHARED", Box: "s2"}}
	if !slices.Equal(start.Vars, want) {
		t.Fatalf("vars %+v", start.Vars)
	}
}

func TestRunnerSendsCommand(t *testing.T) {
	st, svc := seed(t, "worker")
	if _, err := st.DB.Exec(`UPDATE services SET command_json = '["python","-m","app.worker"]' WHERE id = ?`, svc); err != nil {
		t.Fatal(err)
	}
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	i := slices.IndexFunc(gw.sent, func(c proto.Command) bool { return c.Name == proto.CmdStart })
	if i < 0 {
		t.Fatal("no Start sent")
	}
	var start proto.StartArgs
	json.Unmarshal(gw.sent[i].Args, &start)
	if !slices.Equal(start.Cmd, []string{"python", "-m", "app.worker"}) {
		t.Fatalf("cmd %q", start.Cmd)
	}
}

func TestRunnerBuildVarsOnlyFlagged(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	for _, v := range []db.UpsertEnvVarParams{
		{ID: "v1", EnvironmentID: "env_1", ServiceID: svc, Key: "NPM_TOKEN", AgentBox: "b1", RecoveryBox: "r", Build: 1},
		{ID: "v2", EnvironmentID: "env_1", ServiceID: svc, Key: "DATABASE_URL", AgentBox: "b2", RecoveryBox: "r"},
	} {
		if err := st.Q.UpsertEnvVar(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	var b proto.BuildArgs
	json.Unmarshal(gw.sent[0].Args, &b)
	if want := []proto.SealedVar{{Key: "NPM_TOKEN", Box: "b1", Build: true}}; !slices.Equal(b.Vars, want) {
		t.Fatalf("build vars %+v", b.Vars)
	}
	var start proto.StartArgs
	json.Unmarshal(gw.sent[1].Args, &start)
	if len(start.Vars) != 2 {
		t.Fatalf("start vars %+v; want both", start.Vars)
	}
}

func TestBuildSendsRoot(t *testing.T) {
	st, svc := seed(t, "web")
	if _, err := st.DB.Exec("UPDATE services SET root = 'landing' WHERE id = ?", svc); err != nil {
		t.Fatal(err)
	}
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	var b proto.BuildArgs
	json.Unmarshal(gw.sent[0].Args, &b)
	if b.Root != "landing" {
		t.Fatalf("build root %q", b.Root)
	}
}

func TestBuildRootNeedsNewAgent(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	st.DB.Exec("UPDATE services SET root = 'landing' WHERE id = ?", svc)
	st.DB.Exec("UPDATE servers SET agent_version = '0.1.12'")
	gw := &fakeSender{}
	id, _ := deployOnce(t, st, svc, gw)
	d, _ := st.Q.GetDeploy(ctx, id)
	if len(gw.sent) != 0 || d.State != "failed" || !strings.Contains(d.Cause, "runs agent 0.1.12, which cannot build the folder landing") {
		t.Fatalf("sent %v, deploy %s %q", gw.names(), d.State, d.Cause)
	}
	st.DB.Exec("UPDATE servers SET agent_version = '0.1.13'")
	gw = &fakeSender{}
	deployOnce(t, st, svc, gw)
	if len(gw.sent) == 0 || gw.sent[0].Name != proto.CmdBuild {
		t.Fatalf("a 0.1.13 agent did not get the build: %v", gw.names())
	}
}

func TestOlderThan(t *testing.T) {
	for v, want := range map[string]bool{"0.1.12": true, "0.1.13": false, "0.1.14": false, "0.2.0": false, "0.0.9": true, "1.0": false, "0.1": true, "dev": false, "": false, "0.1.13-rc1": false} {
		if olderThan(v, "0.1.13") != want {
			t.Errorf("%q: %v", v, !want)
		}
	}
}

func TestServiceOverridesSharedBuildFlag(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "v1", EnvironmentID: "env_1", Key: "TOKEN", AgentBox: "shared", RecoveryBox: "r", Build: 1})
	st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "v2", EnvironmentID: "env_1", ServiceID: svc, Key: "TOKEN", AgentBox: "own", RecoveryBox: "r"})
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	var b proto.BuildArgs
	json.Unmarshal(gw.sent[0].Args, &b)
	if len(b.Vars) != 0 {
		t.Fatalf("the service's own run-time TOKEN should override the shared build one; build vars %+v", b.Vars)
	}
}

type fakeTokens struct {
	tok   string
	err   error
	repos []string
}

func (f *fakeTokens) Token(_ context.Context, repo string) (string, error) {
	f.repos = append(f.repos, repo)
	return f.tok, f.err
}

func runWith(t *testing.T, st *store.Store, svc string, gw Sender, tokens TokenSource) string {
	t.Helper()
	ctx := context.Background()
	id, err := app.New(st).Deploy(ctx, svc, "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	r := NewRunner(st, gw, nil)
	r.Tokens = tokens
	if err := r.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRunnerPassesToken(t *testing.T) {
	st, svc := seed(t, "web")
	gw, tokens := &fakeSender{}, &fakeTokens{tok: "ghs_abc"}
	runWith(t, st, svc, gw, tokens)
	var b proto.BuildArgs
	json.Unmarshal(gw.sent[0].Args, &b)
	if b.Token != "ghs_abc" || len(tokens.repos) != 1 || tokens.repos[0] != "https://x/y" {
		t.Fatalf("build token %q, asked for %v", b.Token, tokens.repos)
	}
	for _, q := range []string{`SELECT output FROM deploy_steps`, `SELECT body_json FROM events`, `SELECT body FROM outbox`, `SELECT cause FROM deploys`} {
		rows, err := st.DB.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			rows.Scan(&s)
			if strings.Contains(s, "ghs_abc") {
				t.Fatalf("%s holds the token: %s", q, s)
			}
		}
		rows.Close()
	}
}

func TestRunnerTokenErrorFailsBuild(t *testing.T) {
	st, svc := seed(t, "web")
	gw := &fakeSender{}
	cause := "The Yawl GitHub App is not installed on acme/api. Install it: https://github.com/apps/yawl-ab12/installations/new"
	id := runWith(t, st, svc, gw, &fakeTokens{err: errors.New(cause)})
	d, _ := st.Q.GetDeploy(context.Background(), id)
	if d.State != string(deploy.Failed) || d.Cause != cause || len(gw.sent) != 0 {
		t.Fatalf("state %s cause %q, %d commands sent", d.State, d.Cause, len(gw.sent))
	}
}

func addDB(t *testing.T, st *store.Store) string {
	ctx := context.Background()
	id, err := st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_db", EnvironmentID: "env_1", Name: "acme-db", Kind: "postgres",
		Version: 17, BackupAt: "03:00", CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "recovery.pubkey", Value: "cmVjb3Zlcnk="})
	return id
}

func TestRunnerEnsureStoresBoxes(t *testing.T) {
	st, _ := seed(t, "web")
	dbID := addDB(t, st)
	gw := &fakeSender{}
	id, _ := deployOnce(t, st, dbID, gw)
	if d, _ := st.Q.GetDeploy(context.Background(), id); d.State != "live" || len(gw.sent) != 1 || gw.sent[0].Name != proto.CmdEnsureDatabase {
		t.Fatalf("state %s, sent %v", d.State, gw.names())
	}
	var a proto.EnsureDatabaseArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	if a.Name != "acme-db" || a.Version != 17 || a.Password != "" || a.RecoveryPubkey != "cmVjb3Zlcnk=" {
		t.Fatalf("args %+v", a)
	}
	rows, _ := st.Q.EnvVarsFor(context.Background(), db.EnvVarsForParams{EnvironmentID: "env_1", ServiceID: dbID})
	got := map[string][2]string{}
	for _, r := range rows {
		got[r.Key] = [2]string{r.AgentBox, r.RecoveryBox}
	}
	if got["PASSWORD"] != [2]string{"pw-a", "pw-r"} || got["URL"] != [2]string{"url-a", "url-r"} {
		t.Fatalf("db vars %v", got)
	}
	deployOnce(t, st, dbID, gw)
	json.Unmarshal(gw.sent[1].Args, &a)
	if a.Password != "pw-a" {
		t.Fatalf("second ensure sent password %q", a.Password)
	}
}

func TestRunnerEnsureNeedsRecoveryKey(t *testing.T) {
	st, _ := seed(t, "web")
	dbID := addDB(t, st)
	st.DB.Exec(`DELETE FROM settings WHERE key = 'recovery.pubkey'`)
	gw := &fakeSender{}
	id, _ := deployOnce(t, st, dbID, gw)
	d, _ := st.Q.GetDeploy(context.Background(), id)
	if d.State != "failed" || d.Cause != "no recovery key yet; run yawl recovery-key init first" || len(gw.sent) != 0 {
		t.Fatalf("state %s cause %q sent %v", d.State, d.Cause, gw.names())
	}
}

func TestRunnerResolvesReference(t *testing.T) {
	st, svc := seed(t, "web")
	dbID := addDB(t, st)
	deployOnce(t, st, dbID, &fakeSender{})
	st.Q.UpsertEnvVar(context.Background(), db.UpsertEnvVarParams{ID: "v1", EnvironmentID: "env_1", ServiceID: svc, Key: "DATABASE_URL", Ref: "acme-db.url"})
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	var start proto.StartArgs
	json.Unmarshal(gw.sent[1].Args, &start)
	if want := []proto.SealedVar{{Key: "DATABASE_URL", Box: "url-a"}}; !slices.Equal(start.Vars, want) {
		t.Fatalf("vars %+v", start.Vars)
	}
}

func TestRunnerReferenceMissing(t *testing.T) {
	st, svc := seed(t, "web")
	addDB(t, st)
	st.Q.UpsertEnvVar(context.Background(), db.UpsertEnvVarParams{ID: "v1", EnvironmentID: "env_1", ServiceID: svc, Key: "DATABASE_URL", Ref: "acme-db.url"})
	id, _ := deployOnce(t, st, svc, &fakeSender{})
	d, _ := st.Q.GetDeploy(context.Background(), id)
	if want := "DATABASE_URL refers to acme-db, which has not been created yet. Deploy acme-db first."; d.State != "failed" || d.Cause != want {
		t.Fatalf("state %s cause %q", d.State, d.Cause)
	}
}

func sqlNull(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }

type keys map[string]string

func (k keys) TakeRestoreKey(id string) (string, bool) { v, ok := k[id]; delete(k, id); return v, ok }

func restoreDeploy(t *testing.T) (*store.Store, string) {
	b, st, _, _ := backupWorld(t, true)
	_ = b
	ctx := context.Background()
	st.Q.InsertBackup(ctx, db.InsertBackupParams{ID: "bak_1", ServiceID: "svc_db", ObjectKey: "yawl/acme/production/acme-db/x-bak_1.dump.age", CreatedAt: 1, UpdatedAt: 1})
	st.Q.FinishBackup(ctx, db.FinishBackupParams{State: "ok", UpdatedAt: 1, ID: "bak_1"})
	a := app.New(st)
	id, err := a.Restore(ctx, "svc_db", "bak_1", "acme-db-2", "unused", "owner")
	if err != nil {
		t.Fatal(err)
	}
	return st, id
}

func TestRunnerRestoreSendsKey(t *testing.T) {
	st, id := restoreDeploy(t)
	gw := &fakeSender{}
	r := NewRunner(st, gw, nil)
	r.RestoreKeys = keys{id: "sealed-recovery"}
	r.V = mustVault(t, st)
	if err := r.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if names := gw.names(); len(names) != 2 || names[0] != proto.CmdEnsureDatabase || names[1] != proto.CmdRestore {
		t.Fatalf("sent %v", names)
	}
	var a proto.RestoreArgs
	json.Unmarshal(gw.sent[1].Args, &a)
	if a.Name != "acme-db-2" || a.Key != "yawl/acme/production/acme-db/x-bak_1.dump.age" || a.RecoveryKey != "sealed-recovery" ||
		a.Password != "pw-a" || a.Storage.Bucket != "acme-backups" || a.Version != 17 {
		t.Fatalf("restore args %+v", a)
	}
	if d, _ := st.Q.GetDeploy(context.Background(), id); d.State != "live" {
		t.Fatalf("state %s", d.State)
	}
}

var noKey = map[string]string{proto.CmdRestore: "Cannot open the recovery key: it was sealed for another server key"}

func TestRunnerRestoreKeyLost(t *testing.T) {
	st, id := restoreDeploy(t)
	gw := &fakeSender{fail: noKey}
	r := NewRunner(st, gw, nil)
	r.RestoreKeys = keys{}
	r.V = mustVault(t, st)
	r.Run(context.Background(), id)
	d, _ := st.Q.GetDeploy(context.Background(), id)
	if d.State != "failed" || d.Cause != "the recovery key was not kept across a server restart; run yawl db restore again" {
		t.Fatalf("state %s cause %q", d.State, d.Cause)
	}
	var a proto.RestoreArgs
	json.Unmarshal(gw.sent[len(gw.sent)-1].Args, &a)
	if gw.sent[len(gw.sent)-1].Key != id+":restore" || a.RecoveryKey != "" {
		t.Fatalf("sent %s %+v", gw.sent[len(gw.sent)-1].Key, a)
	}
}

func TestRunnerRestoreKeyLostAnsweredFromRecord(t *testing.T) {
	st, id := restoreDeploy(t)
	gw := &fakeSender{fail: noKey, results: map[string]proto.Result{id + ":restore": {Key: id + ":restore", OK: true, Data: []byte("{}")}}}
	r := NewRunner(st, gw, nil)
	r.RestoreKeys = keys{}
	r.V = mustVault(t, st)
	r.Run(context.Background(), id)
	if d, _ := st.Q.GetDeploy(context.Background(), id); d.State != "live" {
		t.Fatalf("state %s cause %q", d.State, d.Cause)
	}
}

func TestRunnerMoveRestoreKeyLost(t *testing.T) {
	st, id := restoreDeploy(t)
	st.DB.Exec(`UPDATE deploys SET move_id = 'mov_1' WHERE id = ?`, id)
	r := NewRunner(st, &fakeSender{fail: noKey}, nil)
	r.RestoreKeys = keys{}
	r.V = mustVault(t, st)
	r.Run(context.Background(), id)
	d, _ := st.Q.GetDeploy(context.Background(), id)
	if d.Cause != "the recovery key was not kept across a server restart; retry the move and enter it again" {
		t.Fatalf("state %s cause %q", d.State, d.Cause)
	}
	if got := outbox(t, st); len(got) == 0 || !strings.HasSuffix(got[len(got)-1], "It did not start on the new server.") {
		t.Fatalf("outbox %q", got)
	}
}

func TestSwitchFailurePointsBack(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	first, _ := deployOnce(t, st, svc, &fakeSender{})
	gw := &fakeSender{n: 1, failKey: func(k string) bool { return strings.HasSuffix(k, ":switch") }}
	id, _ := deployOnce(t, st, svc, gw)
	d, _ := st.Q.GetDeploy(ctx, id)
	prev, _ := st.Q.GetDeploy(ctx, first)
	s, _ := st.Q.GetService(ctx, svc)
	if d.State != "failed" || s.CurrentDeployID.String != first {
		t.Fatalf("state %s current %s", d.State, s.CurrentDeployID.String)
	}
	n := slices.Index(gw.names(), proto.CmdSwitchRoute)
	if got := gw.names()[n:]; !slices.Equal(got, []string{proto.CmdSwitchRoute, proto.CmdSwitchRoute, proto.CmdStop}) {
		t.Fatalf("sent %v", gw.names())
	}
	var back proto.SwitchArgs
	var stop proto.StopArgs
	json.Unmarshal(gw.sent[n+1].Args, &back)
	json.Unmarshal(gw.sent[n+2].Args, &stop)
	if back.Container != prev.Container || stop.Container != d.Container || d.Container == prev.Container {
		t.Fatalf("back to %q, stopped %q (new %q, prev %q)", back.Container, stop.Container, d.Container, prev.Container)
	}
	if got := outbox(t, st); len(got) != 1 || got[0] != "acme-api deploy failed: permission denied. The previous version is still live." {
		t.Fatalf("outbox %q", got)
	}
}

func TestFirstSwitchFailureStopsNew(t *testing.T) {
	st, svc := seed(t, "web")
	gw := &fakeSender{fail: map[string]string{proto.CmdSwitchRoute: "caddy: 500"}}
	deployOnce(t, st, svc, gw)
	if got := gw.names(); got[len(got)-1] != proto.CmdStop {
		t.Fatalf("sent %v", got)
	}
}

func TestFailedRollbackEndsFailed(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	deployOnce(t, st, svc, &fakeSender{})
	gw := &fakeSender{fail: map[string]string{proto.CmdDrain: "container stop timed out"}, failKey: func(k string) bool { return strings.HasSuffix(k, ":rollback") }}
	id, _ := deployOnce(t, st, svc, gw)
	if d, _ := st.Q.GetDeploy(ctx, id); d.State != "failed" {
		t.Fatalf("state %s", d.State)
	}
	want := "acme-api deploy failed: container stop timed out; rollback also failed: permission denied. The new version may still be serving."
	if got := outbox(t, st); len(got) != 1 || got[0] != want {
		t.Fatalf("outbox %q", got)
	}
}

func TestMoveDeployFailureSaysNewServer(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	deployOnce(t, st, svc, &fakeSender{})
	if err := st.Q.CreateMoveDeploy(ctx, db.CreateMoveDeployParams{ID: "dep_move", ServiceID: svc, MoveID: "mov_1", CreatedAt: 2, UpdatedAt: 2}); err != nil {
		t.Fatal(err)
	}
	NewRunner(st, &fakeSender{fail: map[string]string{proto.CmdHealthCheck: "boom"}}, nil).Run(ctx, "dep_move")
	if got := outbox(t, st); len(got) != 1 || got[0] != "acme-api deploy failed: boom. It did not start on the new server." {
		t.Fatalf("outbox %q", got)
	}
}

func TestRunnerRecordsWarnings(t *testing.T) {
	st, svc := seed(t, "web")
	st.Q.UpsertEnvVar(context.Background(), db.UpsertEnvVarParams{ID: "v1", EnvironmentID: "env_1", ServiceID: svc, Key: "DB", AgentBox: "b"})
	gw := &fakeSender{warnings: []string{"The code reads X, which is not set in production.", "go.mod asks for Go 1.23; the build uses Go 1.21."}}
	id, _ := deployOnce(t, st, svc, gw)
	ctx := context.Background()
	if d, _ := st.Q.GetDeploy(ctx, id); d.State != "live" {
		t.Fatal(d.State)
	}
	var a proto.BuildArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	if !slices.Equal(a.Keys, []string{"DB"}) || a.Env != "production" {
		t.Fatalf("build args keys %v env %q", a.Keys, a.Env)
	}
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'deploy.warning' AND subject_id = ?`, id).Scan(&n)
	steps, _ := st.Q.DeploySteps(ctx, id)
	if n != 2 || steps[0].Output != "warning: The code reads X, which is not set in production.\nwarning: go.mod asks for Go 1.23; the build uses Go 1.21." {
		t.Fatalf("%d warning events, build output %q", n, steps[0].Output)
	}
}

func TestRunnerOutsideAccess(t *testing.T) {
	st, _ := seed(t, "web")
	dbID := addDB(t, st)
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET outside_port = 15432, allow_json = '["203.0.113.4/32"]' WHERE id = ?`, dbID)
	gw := &fakeSender{}
	deployOnce(t, st, dbID, gw)
	var a proto.EnsureDatabaseArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	if a.OutsidePort != 15432 || !slices.Equal(a.Allow, []string{"203.0.113.4/32"}) || a.PublicIP != "1.2.3.4" {
		t.Fatalf("args %+v", a)
	}
	if box, err := app.DatabaseVar(ctx, st.Q, "env_1", dbID, "OUTSIDE_URL"); err != nil || box != "out-a" {
		t.Fatalf("OUTSIDE_URL %q %v", box, err)
	}
	st.DB.Exec(`UPDATE services SET outside_port = 0, allow_json = '[]' WHERE id = ?`, dbID)
	deployOnce(t, st, dbID, gw)
	if _, err := app.DatabaseVar(ctx, st.Q, "env_1", dbID, "OUTSIDE_URL"); err == nil {
		t.Fatal("OUTSIDE_URL kept after outside access was turned off")
	}
}

func TestBuildRecordsCommit(t *testing.T) {
	st, svc := seed(t, "web")
	id, _ := deployOnce(t, st, svc, &fakeSender{})
	if d, _ := st.Q.GetDeploy(context.Background(), id); d.CommitSha != "abc" {
		t.Fatalf("commit %q", d.CommitSha)
	}
}

func TestCronDeploySchedules(t *testing.T) {
	st, svc := seed(t, "cron")
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET port = 0, health_path = '', schedule = '0 3 * * *', timezone = 'Europe/Berlin', command_json = '["node","report.js"]', timeout_ms = 3600000 WHERE id = ?`, svc)
	st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "v1", EnvironmentID: "env_1", ServiceID: "", Key: "KEY", AgentBox: "box", RecoveryBox: "r"})
	gw := &fakeSender{}
	id, _ := deployOnce(t, st, svc, gw)
	if got := strings.Join(gw.names(), ","); got != "Build,CronSchedule,Prune" {
		t.Fatalf("sent %s", got)
	}
	var job proto.CronJob
	json.Unmarshal(gw.sent[1].Args, &job)
	want := proto.CronJob{ID: svc, Project: "acme", Env: "production", Service: "api", Image: "yawl/acme-production-api:" + id,
		Schedule: "0 3 * * *", Timezone: "Europe/Berlin", Cmd: []string{"node", "report.js"}, Vars: []proto.SealedVar{{Key: "KEY", Box: "box"}},
		Timeout: time.Hour, Label: "acme/production/api"}
	if !reflect.DeepEqual(job, want) {
		t.Fatalf("job\n%+v\nwant\n%+v", job, want)
	}
	if d, _ := st.Q.GetDeploy(ctx, id); d.State != "live" {
		t.Fatalf("state %s", d.State)
	}
	st.DB.Exec(`UPDATE services SET timezone = '' WHERE id = ?`, svc)
	gw2 := &fakeSender{}
	deployOnce(t, st, svc, gw2)
	json.Unmarshal(gw2.sent[1].Args, &job)
	if job.Timezone != cron.PanelZone() {
		t.Fatalf("zone %q", job.Timezone)
	}
}

func TestVolumeServiceStopsFirst(t *testing.T) {
	st, svc := seed(t, "web")
	st.DB.Exec(`UPDATE services SET volumes_json = '[{"name":"uploads","path":"/app/uploads"}]' WHERE id = ?`, svc)
	deployOnce(t, st, svc, &fakeSender{})
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	if got := strings.Join(gw.names(), ","); got != "Build,Stop,Start,HealthCheck,SwitchRoute,Prune" {
		t.Fatalf("%s", got)
	}
	var a proto.StartArgs
	json.Unmarshal(gw.sent[2].Args, &a)
	if len(a.Volumes) != 1 || a.Volumes[0] != (proto.Volume{Name: "uploads", Path: "/app/uploads"}) {
		t.Fatalf("%+v", a)
	}
}

func TestVolumeServiceRollsBackOnFailedCheck(t *testing.T) {
	st, svc := seed(t, "web")
	st.DB.Exec(`UPDATE services SET volumes_json = '[{"name":"uploads","path":"/app/uploads"}]' WHERE id = ?`, svc)
	deployOnce(t, st, svc, &fakeSender{})
	gw := &fakeSender{fail: map[string]string{proto.CmdHealthCheck: "boom"}}
	id, _ := deployOnce(t, st, svc, gw)
	if d, _ := st.Q.GetDeploy(context.Background(), id); d.State != "rolled_back" {
		t.Fatalf("state %s", d.State)
	}
	if got := strings.Join(gw.names(), ","); !strings.HasSuffix(got, "Start,SwitchRoute") {
		t.Fatalf("the old version was not started again and routed: %s", got)
	}
}

func TestRunnerStoresSubject(t *testing.T) {
	st, svc := seed(t, "web")
	id, _ := deployOnce(t, st, svc, &fakeSender{})
	if d, _ := st.Q.GetDeploy(context.Background(), id); d.Subject != "Fix invoice rounding" || d.CommitSha != "abc" {
		t.Fatalf("subject %q commit %q", d.Subject, d.CommitSha)
	}
}

func TestRunnerSendsLimits(t *testing.T) {
	limits := func(c proto.Command) (float64, int64) {
		var a struct {
			CPU    float64
			Memory int64
		}
		json.Unmarshal(c.Args, &a)
		return a.CPU, a.Memory
	}
	sent := func(gw *fakeSender, name string) proto.Command {
		for _, c := range gw.sent {
			if c.Name == name {
				return c
			}
		}
		t.Fatalf("no %s in %v", name, gw.names())
		return proto.Command{}
	}
	st, svc := seed(t, "web")
	st.DB.Exec(`UPDATE services SET cpu = 0.5, memory = 512000000, release_json = '["migrate"]' WHERE id = ?`, svc)
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	for _, name := range []string{proto.CmdStart, proto.CmdRelease} {
		if c, m := limits(sent(gw, name)); c != 0.5 || m != 512e6 {
			t.Fatalf("%s: %v %v", name, c, m)
		}
	}
	dbID := addDB(t, st)
	st.DB.Exec(`UPDATE services SET cpu = 1, memory = 1000000000 WHERE id = ?`, dbID)
	gw = &fakeSender{}
	deployOnce(t, st, dbID, gw)
	if c, m := limits(sent(gw, proto.CmdEnsureDatabase)); c != 1 || m != 1e9 {
		t.Fatalf("ensure: %v %v", c, m)
	}
	st2, cron := seed(t, "cron")
	st2.DB.Exec(`UPDATE services SET port = 0, health_path = '', schedule = '0 3 * * *', timezone = 'UTC', command_json = '["x"]', timeout_ms = 3600000, memory = 256000000 WHERE id = ?`, cron)
	gw = &fakeSender{}
	deployOnce(t, st2, cron, gw)
	if _, m := limits(sent(gw, proto.CmdCronSchedule)); m != 256e6 {
		t.Fatalf("cron: %v", m)
	}
}

func TestRollbackStartKeepsOldLimits(t *testing.T) {
	st, svc := seed(t, "worker")
	st.DB.Exec(`UPDATE services SET memory = 512000000 WHERE id = ?`, svc)
	deployOnce(t, st, svc, &fakeSender{})
	st.DB.Exec(`UPDATE services SET cpu = 8, memory = 64000000 WHERE id = ?`, svc)
	gw := &fakeSender{fail: map[string]string{proto.CmdHealthCheck: "boom"}}
	deployOnce(t, st, svc, gw)
	for _, c := range gw.sent {
		if strings.HasSuffix(c.Key, ":rollback-start") {
			var a proto.StartArgs
			json.Unmarshal(c.Args, &a)
			if a.CPU != 0 || a.Memory != 512e6 {
				t.Fatalf("rollback-start with %v CPUs and %d bytes", a.CPU, a.Memory)
			}
			return
		}
	}
	t.Fatalf("no rollback-start in %v", gw.names())
}

func TestRunnerPullsImage(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET repo = '', branch = '', image = 'ghcr.io/acme/api:1' WHERE id = ?`, svc)
	v, err := vault.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.SaveRegistry(ctx, st.Q, v, "ghcr.io", "acme", "ghp_secret")
	id, _ := app.New(st).Deploy(ctx, svc, "", "owner")
	gw := &fakeSender{}
	r := NewRunner(st, gw, nil)
	r.V = v
	if err := r.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(gw.names(), proto.CmdBuild) || gw.names()[0] != proto.CmdPull {
		t.Fatalf("sent %v", gw.names())
	}
	var a proto.PullArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	if a.Ref != "ghcr.io/acme/api:1" || a.Tag != "yawl/acme-production-api:"+id || a.Auth == nil || a.Auth.Username != "acme" || a.Auth.Token != "ghp_secret" {
		t.Fatalf("%+v", a)
	}
	d, _ := st.Q.GetDeploy(ctx, id)
	if d.State != "live" || d.Image != a.Tag || d.Subject != "ghcr.io/acme/api:1" || d.Digest != "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("%+v", d)
	}
}

func TestMovePullsRunningDigest(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET repo = '', branch = '', image = 'ghcr.io/acme/api:1' WHERE id = ?`, svc)
	deployOnce(t, st, svc, &fakeSender{})
	if err := st.Q.CreateMoveDeploy(ctx, db.CreateMoveDeployParams{ID: "dep_move", ServiceID: svc, MoveID: "mov_1", CreatedAt: time.Now().UnixMilli(), UpdatedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	gw := &fakeSender{}
	NewRunner(st, gw, nil).Run(ctx, "dep_move")
	if len(gw.sent) == 0 || gw.sent[0].Name != proto.CmdPull {
		t.Fatalf("sent %v", gw.names())
	}
	var a proto.PullArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	if a.Ref != "ghcr.io/acme/api@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("pulled %q", a.Ref)
	}
}

func TestRunnerRecoverSendsBaseAndWAL(t *testing.T) {
	_, st, _, _ := backupWorld(t, true)
	ctx := context.Background()
	st.DB.Exec(`UPDATE services SET pitr = 1 WHERE id = 'svc_db'`)
	st.Q.InsertBaseBackup(ctx, db.InsertBaseBackupParams{ID: "base_1", ServiceID: "svc_db", ObjectKey: "k/base_1", CreatedAt: utc("2026-10-21 03:00").UnixMilli(), UpdatedAt: 1})
	st.Q.FinishBackup(ctx, db.FinishBackupParams{State: "ok", Sha256: "sum-base", UpdatedAt: 1, ID: "base_1", FinishedAt: sqlNull(utc("2026-10-21 03:05").UnixMilli())})
	for i, at := range []string{"2026-10-21 04:00", "2026-10-21 05:00", "2026-10-21 06:00"} {
		st.Q.InsertWALBatch(ctx, db.InsertWALBatchParams{ID: fmt.Sprint("w", i), ServiceID: "svc_db", ObjectKey: fmt.Sprint("k/w", i), FirstWal: "a", LastWal: "b", Size: 1, Sha256: fmt.Sprint("sum-w", i), CreatedAt: utc(at).UnixMilli()})
	}
	a := app.New(st)
	at := utc("2026-10-21 04:30")
	id, err := a.RestoreAt(ctx, "svc_db", at, "acme-db-2", "unused", "owner")
	if err != nil {
		t.Fatal(err)
	}
	gw := &fakeSender{}
	r := NewRunner(st, gw, nil)
	r.RestoreKeys = keys{id: "sealed-recovery"}
	r.V = mustVault(t, st)
	if err := r.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	var ra proto.RestoreArgs
	json.Unmarshal(gw.sent[1].Args, &ra)
	if ra.Key != "k/base_1" || ra.SHA256 != "sum-base" || ra.PITR == nil || !slices.Equal(ra.PITR.WAL, []string{"k/w0", "k/w1"}) ||
		!slices.Equal(ra.PITR.WALSHA256, []string{"sum-w0", "sum-w1"}) || ra.PITR.At != "2026-10-21 04:30:00+00" ||
		ra.PITR.SourcePassword != "password-box" || ra.Ensure == nil || ra.Ensure.Password != "pw-a" || ra.Ensure.PITR {
		t.Fatalf("restore args %+v pitr %+v ensure %+v", ra, ra.PITR, ra.Ensure)
	}
}

func TestRunnerSendsReplicas(t *testing.T) {
	st, svc := seed(t, "worker")
	if _, err := st.DB.Exec(`UPDATE services SET replicas = 3 WHERE id = ?`, svc); err != nil {
		t.Fatal(err)
	}
	deployOnce(t, st, svc, &fakeSender{})
	gw := &fakeSender{fail: map[string]string{proto.CmdHealthCheck: "boom"}}
	deployOnce(t, st, svc, gw)
	seen := map[string]int{}
	for _, c := range gw.sent {
		if c.Name == proto.CmdStart {
			var a proto.StartArgs
			json.Unmarshal(c.Args, &a)
			seen[c.Key[strings.Index(c.Key, ":")+1:]] = a.Replicas
		}
	}
	if seen["start"] != 3 || seen["rollback-start"] != 3 {
		t.Fatalf("%v", seen)
	}
}
