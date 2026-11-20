package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type cronSender struct {
	mu   sync.Mutex
	away bool
	none bool
	nows int
	sent []string
}

func (c *cronSender) Send(_ context.Context, server string, cmd proto.Command) (proto.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, server+" "+cmd.Name)
	if c.away {
		return proto.Result{}, errors.New("agent not connected")
	}
	switch cmd.Name {
	case proto.CmdCronRuns:
		b, _ := json.Marshal(proto.CronRunsData{Runs: []proto.CronRun{{ID: "r1", ScheduledAt: 1, StartedAt: 2, FinishedAt: 3, Exit: 1, Output: "boom"}}})
		return proto.Result{OK: true, Data: b}, nil
	case proto.CmdCronRunNow:
		if c.none {
			return proto.Result{Cause: "this agent has no cron job svc_8f2k"}, nil
		}
		c.nows++
		if c.nows > 1 {
			return proto.Result{Cause: "A run of report is still going."}, nil
		}
		return proto.Result{OK: true}, nil
	}
	return proto.Result{OK: true}, nil
}

const cronYAML = `project: acme
environment: production
server: hel1
services:
  - name: report
    kind: cron
    repo: https://github.com/acme/api
    schedule: "0 3 * * *"
    timezone: Asia/Tashkent
    command: [node, report.js]
`

func cronAPI(t *testing.T) (*httptest.Server, string, *cronSender) {
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_1", Name: "hel1", PublicIp: "1.2.3.4", CreatedAt: 1, UpdatedAt: 1})
	a := app.New(st)
	a.Now = func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) }
	tok, _ := a.CreateToken(ctx, "laptop")
	mustApply(t, a, cronYAML)
	gw := &cronSender{}
	srv := httptest.NewServer(New(Deps{App: a, GW: gw}))
	t.Cleanup(srv.Close)
	return srv, tok, gw
}

func TestCronRunsAPI(t *testing.T) {
	srv, tok, gw := cronAPI(t)
	res, b := call(t, srv, tok, "GET", "/api/services/acme/production/report/runs", "")
	var v struct {
		Next     int64  `json:"next"`
		Timezone string `json:"timezone"`
		Running  bool   `json:"running"`
		Runs     []proto.CronRun
	}
	json.Unmarshal(b, &v)
	if res.StatusCode != 200 || v.Next != time.Date(2026, 10, 27, 22, 0, 0, 0, time.UTC).UnixMilli() || v.Timezone != "Asia/Tashkent" || len(v.Runs) != 1 || v.Runs[0].Output != "boom" {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if gw.sent[0] != "srv_1 CronRuns" {
		t.Fatalf("%v", gw.sent)
	}
	gw.away = true
	if res, b := call(t, srv, tok, "GET", "/api/services/acme/production/report/runs", ""); res.StatusCode != 502 || string(b) != "hel1 is offline; the runs of report cannot be read now.\n" {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/services/acme/production/report/runs", ""); res.StatusCode != 502 || string(b) != "hel1 is offline; report cannot be run now.\n" {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
}

func TestRunNowTwice(t *testing.T) {
	srv, tok, _ := cronAPI(t)
	if res, b := call(t, srv, tok, "POST", "/api/services/acme/production/report/runs", ""); res.StatusCode != 202 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/services/acme/production/report/runs", ""); res.StatusCode != 409 || string(b) != "A run of report is still going.\n" {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
}

func TestCronRunsNotACronJob(t *testing.T) {
	srv, tok, _ := cronAPI(t)
	call(t, srv, tok, "POST", "/api/apply", strings.Replace(cronYAML, "services:", "services:\n  - {name: api, kind: web, repo: https://github.com/acme/api}", 1))
	if res, b := call(t, srv, tok, "GET", "/api/services/acme/production/api/runs", ""); res.StatusCode != 404 || string(b) != "api is not a cron job.\n" {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
}

func TestRunNowNotDeployed(t *testing.T) {
	srv, tok, gw := cronAPI(t)
	gw.none = true
	if res, b := call(t, srv, tok, "POST", "/api/services/acme/production/report/runs", ""); res.StatusCode != 409 || string(b) != "Deploy report first: hel1 does not have it yet.\n" {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
}
