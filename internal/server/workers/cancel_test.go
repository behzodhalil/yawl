package workers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type slowBuild struct{ fakeSender }

func (s *slowBuild) Send(ctx context.Context, server string, c proto.Command) (proto.Result, error) {
	if c.Name == proto.CmdBuild {
		s.sent = append(s.sent, c)
		select {
		case <-ctx.Done():
			return proto.Result{}, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return s.fakeSender.Send(ctx, server, c)
}

func TestCancelDuringBuild(t *testing.T) {
	st, svc := seed(t, "web")
	id, _ := app.New(st).Deploy(context.Background(), svc, "", "owner")
	gw := &slowBuild{}
	go func() {
		time.Sleep(300 * time.Millisecond)
		st.DB.Exec(`UPDATE deploys SET cancel_requested = 1 WHERE id = ?`, id)
	}()
	start := time.Now()
	if err := NewRunner(st, gw, nil).Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	d, _ := st.Q.GetDeploy(context.Background(), id)
	if d.State != "superseded" || d.Cause != "Canceled." || time.Since(start) > 4*time.Second {
		t.Fatalf("%s %q after %s", d.State, d.Cause, time.Since(start))
	}
	for _, c := range gw.sent {
		if c.Name == proto.CmdStart {
			t.Fatal("started after a cancel")
		}
	}
}

func TestNoCacheSent(t *testing.T) {
	st, svc := seed(t, "web")
	id, _ := app.New(st).Deploy(context.Background(), svc, "", "owner")
	st.DB.Exec(`UPDATE deploys SET no_cache = 1 WHERE id = ?`, id)
	gw := &fakeSender{}
	NewRunner(st, gw, nil).Run(context.Background(), id)
	var a proto.BuildArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	if !a.NoCache {
		t.Fatalf("%+v", a)
	}
}

func TestCancelRaceWithStart(t *testing.T) {
	st, svc := seed(t, "web")
	id, _ := app.New(st).Deploy(context.Background(), svc, "", "owner")
	st.DB.Exec(`UPDATE deploys SET state = 'building' WHERE id = ?`, id)
	n, _ := st.Q.CancelQueued(context.Background(), db.CancelQueuedParams{UpdatedAt: 1, ID: id})
	if n != 0 {
		t.Fatal("ended a deploy that had started")
	}
	st.DB.Exec(`UPDATE deploys SET state = 'superseded', cause = 'Canceled.' WHERE id = ?`, id)
	if n, _ := st.Q.StartDeploy(context.Background(), db.StartDeployParams{State: "building", UpdatedAt: 1, ID: id}); n != 0 {
		t.Fatal("started a canceled deploy")
	}
}

func TestCancelEndsStepRow(t *testing.T) {
	st, svc := seed(t, "web")
	id, _ := app.New(st).Deploy(context.Background(), svc, "", "owner")
	go func() {
		time.Sleep(300 * time.Millisecond)
		st.DB.Exec(`UPDATE deploys SET cancel_requested = 1 WHERE id = ?`, id)
	}()
	if err := NewRunner(st, &slowBuild{}, nil).Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	steps, _ := st.Q.DeploySteps(context.Background(), id)
	if len(steps) != 1 || steps[0].State == "running" || !steps[0].FinishedAt.Valid {
		t.Fatalf("%+v", steps)
	}
}

func TestCancelBetweenSteps(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	id, _ := app.New(st).Deploy(ctx, svc, "", "owner")
	st.DB.Exec(`UPDATE deploys SET state = 'building', image = 'yawl/acme-production-api:x', cancel_requested = 1 WHERE id = ?`, id)
	st.DB.Exec(`INSERT INTO deploy_steps (id, deploy_id, step, state, started_at, created_at, updated_at) VALUES ('stp_b', ?, 'build', 'ok', 1, 1, 1)`, id)
	gw := &fakeSender{}
	if err := NewRunner(st, gw, nil).Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	if d, _ := st.Q.GetDeploy(ctx, id); d.State != "superseded" || len(gw.sent) != 0 {
		t.Fatalf("%s, sent %v", d.State, gw.names())
	}
}
