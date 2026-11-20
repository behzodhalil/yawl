package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type inspectSender struct {
	server string
	cmd    proto.Command
	block  bool
	cause  string
}

func (s *inspectSender) Send(ctx context.Context, serverID string, cmd proto.Command) (proto.Result, error) {
	s.server, s.cmd = serverID, cmd
	if s.block {
		<-ctx.Done()
		return proto.Result{}, ctx.Err()
	}
	if s.cause != "" {
		return proto.Result{Cause: s.cause}, nil
	}
	data, _ := json.Marshal(proto.InspectData{Commit: "abc", ReadsPort: true, HealthPath: "/health", Missing: []string{"SECRET_KEY"}, Warnings: []string{}})
	return proto.Result{Data: data}, nil
}

type fixedTokens string

func (f fixedTokens) Token(context.Context, string) (string, error) { return string(f), nil }

func setupInspect(t *testing.T, s *inspectSender, timeout time.Duration) (*httptest.Server, string, *app.App) {
	t.Helper()
	_, _, a := setupApp(t)
	tok, _ := a.CreateToken(context.Background(), "inspect")
	srv := httptest.NewServer(New(Deps{App: a, GW: s, Tokens: fixedTokens("ghs_tok"), InspectTimeout: timeout}))
	t.Cleanup(srv.Close)
	mustApply(t, a, "project: acme\nenvironment: production\nserver: hel1\nservices: []\n")
	env, _ := a.St.Q.GetEnvironmentByName(context.Background(), db.GetEnvironmentByNameParams{Project: "acme", Env: "production"})
	a.St.Q.UpsertEnvVar(context.Background(), db.UpsertEnvVarParams{ID: "var_1", EnvironmentID: env.ID, ServiceID: "", Key: "SENTRY_DSN",
		AgentBox: "x", RecoveryBox: "y", CreatedAt: 1, UpdatedAt: 1})
	return srv, tok, a
}

func TestInspectRoute(t *testing.T) {
	s := &inspectSender{}
	srv, tok, _ := setupInspect(t, s, time.Minute)
	res, b := call(t, srv, tok, "POST", "/api/inspect", `{"project":"acme","env":"production","repo":"https://github.com/acme/api","branch":"main"}`)
	var out proto.InspectData
	if json.Unmarshal(b, &out); res.StatusCode != 200 || out.HealthPath != "/health" || !out.ReadsPort {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var args proto.InspectArgs
	json.Unmarshal(s.cmd.Args, &args)
	if s.server != "srv_1" || s.cmd.Name != proto.CmdInspect || args.Token != "ghs_tok" || args.Env != "production" ||
		!reflect.DeepEqual(args.Keys, []string{"SENTRY_DSN"}) || args.Repo != "https://github.com/acme/api" {
		t.Fatalf("%s %+v", s.server, args)
	}
	if strings.Contains(string(b), "ghs_tok") {
		t.Fatal("the clone token reached the browser")
	}
}

func TestInspectCause(t *testing.T) {
	s := &inspectSender{cause: "clone https://github.com/acme/nope: repository not found"}
	srv, tok, _ := setupInspect(t, s, time.Minute)
	res, b := call(t, srv, tok, "POST", "/api/inspect", `{"project":"acme","env":"production","repo":"https://github.com/acme/nope"}`)
	if res.StatusCode != 422 || !strings.Contains(string(b), "repository not found") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, tok, "POST", "/api/inspect", `{"project":"acme","env":"nowhere","repo":"https://x/y"}`); res.StatusCode != 404 {
		t.Fatalf("unknown env: %d", res.StatusCode)
	}
	if res, _ := call(t, srv, tok, "POST", "/api/inspect", `{"project":"acme","env":"production","repo":""}`); res.StatusCode != 400 {
		t.Fatalf("empty repo: %d", res.StatusCode)
	}
}

func TestInspectTimeout(t *testing.T) {
	s := &inspectSender{block: true}
	srv, tok, _ := setupInspect(t, s, 50*time.Millisecond)
	res, b := call(t, srv, tok, "POST", "/api/inspect", `{"project":"acme","env":"production","repo":"https://github.com/acme/api"}`)
	if res.StatusCode != 504 || strings.TrimSpace(string(b)) != "Reading https://github.com/acme/api took longer than 2 minutes." {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestInspectSharedKeys(t *testing.T) {
	s := &inspectSender{}
	srv, tok, _ := setupInspect(t, s, time.Minute)
	testDB.Exec(`INSERT INTO shared_vars (id, scope, server_id, key, agent_box, recovery_box, created_at, updated_at) VALUES ('shv_1', 'team', 'srv_1', 'TEAM_KEY', 'b', 'r', 1, 1)`)
	call(t, srv, tok, "POST", "/api/inspect", `{"project":"acme","env":"production","repo":"https://github.com/acme/api"}`)
	var args proto.InspectArgs
	json.Unmarshal(s.cmd.Args, &args)
	if !slices.Contains(args.Keys, "TEAM_KEY") {
		t.Fatalf("%+v", args.Keys)
	}
}
