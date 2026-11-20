package workers

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
)

func TestCleanupWorker(t *testing.T) {
	st, _ := seed(t, "web")
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 13, 0, 0, 0, time.UTC)
	gw := &fakeSender{}
	w := &Cleanup{St: st, GW: gw, Now: func() time.Time { return now }}
	cleanups := func() []proto.CleanupArgs {
		var out []proto.CleanupArgs
		for _, c := range gw.sent {
			if c.Name == proto.CmdCleanup {
				var a proto.CleanupArgs
				json.Unmarshal(c.Args, &a)
				out = append(out, a)
			}
		}
		return out
	}
	disk := func(pct int) {
		st.DB.Exec(`UPDATE servers SET disk_used_pct = ?, last_heartbeat = ? WHERE id = 'srv_1'`, pct, now.UnixMilli())
	}
	disk(50)
	w.Tick(ctx)
	if len(cleanups()) != 0 {
		t.Fatal("a half-empty disk was cleaned at 13:00")
	}
	disk(85)
	w.Tick(ctx)
	now = now.Add(30 * time.Minute)
	w.Tick(ctx)
	if c := cleanups(); len(c) != 1 || c[0].AllCache {
		t.Fatalf("%+v", c)
	}
	now = now.Add(31 * time.Minute)
	disk(92)
	w.Tick(ctx)
	if c := cleanups(); len(c) != 2 || !c[1].AllCache {
		t.Fatalf("%+v", c)
	}
	now = time.Date(2026, 10, 31, 4, 5, 0, 0, time.UTC)
	disk(10)
	w.Tick(ctx)
	if c := cleanups(); len(c) != 3 {
		t.Fatalf("the night's cleanup: %+v", c)
	}
	last, err := LastCleanup(ctx, st.Q, "srv_1")
	if err != nil || last.Freed != 0 && last.At != now.UnixMilli() || last.Why != "nightly" {
		t.Fatalf("%+v %v", last, err)
	}
}

type barrier struct {
	mu   sync.Mutex
	out  int
	n    int
	held chan struct{}
}

func (b *barrier) Send(ctx context.Context, _ string, c proto.Command) (proto.Result, error) {
	b.mu.Lock()
	if b.out++; b.out == b.n {
		close(b.held)
	}
	b.mu.Unlock()
	select {
	case <-b.held:
		return proto.Result{Key: c.Key, OK: true, Data: json.RawMessage(`{}`)}, nil
	case <-time.After(time.Second):
		return proto.Result{Key: c.Key, Cause: "alone"}, nil
	}
}

func TestCleanupServersAtOnce(t *testing.T) {
	st, _ := seed(t, "web")
	ctx := context.Background()
	now := time.Date(2026, 10, 31, 4, 5, 0, 0, time.UTC)
	st.DB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, last_heartbeat, created_at, updated_at) VALUES ('srv_2', 'hk1', '5.6.7.8', '', '', '', 1, 1, 1)`)
	w := &Cleanup{St: st, GW: &barrier{n: 2, held: make(chan struct{})}, Now: func() time.Time { return now }}
	w.Tick(ctx)
	for _, id := range []string{"srv_1", "srv_2"} {
		if run, _ := LastCleanup(ctx, st.Q, id); run.At == 0 || run.Cause != "" {
			t.Fatalf("%s: %+v", id, run)
		}
	}
}

func TestCleanupOneAtATime(t *testing.T) {
	st, _ := seed(t, "web")
	ctx := context.Background()
	b := &barrier{n: 99, held: make(chan struct{})}
	done := make(chan struct{})
	go func() { RunCleanup(ctx, st.Q, b, "srv_1", false, "nightly", time.Now()); close(done) }()
	time.Sleep(100 * time.Millisecond)
	if _, err := RunCleanup(ctx, st.Q, &fakeSender{}, "srv_1", false, "by hand", time.Now()); !errors.Is(err, ErrCleaning) {
		t.Fatalf("%v", err)
	}
	<-done
}
