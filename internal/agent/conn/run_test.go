package conn

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/state"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestUnrecordedNotStored(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	calls := 0
	r := &runner{cfg: Config{}, st: st, inflight: map[string]chan struct{}{},
		h: func(context.Context, proto.Command) proto.Result { calls++; return proto.Result{OK: true} }}
	for _, name := range []string{proto.CmdLogs, proto.CmdLogs, proto.CmdStart, proto.CmdStart} {
		r.run(context.Background(), proto.Command{Key: "k:" + name, Name: name})
	}
	if calls != 3 {
		t.Fatalf("handler ran %d times, want 3 (Logs twice, Start once)", calls)
	}
	if _, ok, _ := st.Result("k:" + proto.CmdLogs); ok {
		t.Fatal("a Logs result was stored")
	}
}

func TestRunnerIdleWaitsForSavedResult(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	started, release := make(chan struct{}), make(chan struct{})
	r := &runner{cfg: Config{}, st: st, inflight: map[string]chan struct{}{},
		h: func(context.Context, proto.Command) proto.Result {
			close(started)
			<-release
			return proto.Result{OK: true}
		}}
	ctx := context.Background()
	if err := r.idle(ctx); err != nil {
		t.Fatalf("idle with nothing running: %v", err)
	}
	go r.run(ctx, proto.Command{Key: "dep_1:Start", Name: proto.CmdStart})
	<-started
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := r.idle(short); err != context.DeadlineExceeded {
		t.Fatalf("idle while a Start runs: %v", err)
	}
	close(release)
	if err := r.idle(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.Result("dep_1:Start"); !ok {
		t.Fatal("idle returned before the Start's result was saved")
	}
}
