package workers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/notify"
)

func TestOutboxEveryChannel(t *testing.T) {
	st, _ := seed(t, "web")
	t0 := time.UnixMilli(1_000_000_000)
	addOutbox(t, st, t0)
	tg, slack := &fakeNotifier{}, &fakeNotifier{err: errors.New("slack answered 500")}
	o := &Outbox{St: st, Notifier: func(context.Context) (Notifier, error) { return tg, nil },
		Extras: func(context.Context) (map[string]Notifier, error) { return map[string]Notifier{"slack": slack}, nil }}
	o.Tick(context.Background(), t0)
	rows, _ := st.Q.ListOutbox(context.Background())
	if len(tg.sent) != 1 || rows[0].SentAt.Valid || rows[0].NextAttemptAt <= t0.UnixMilli() {
		t.Fatalf("first: telegram %v, row %+v", tg.sent, rows[0])
	}
	slack.err = nil
	o.Tick(context.Background(), t0.Add(time.Hour))
	rows, _ = st.Q.ListOutbox(context.Background())
	if len(tg.sent) != 1 || len(slack.sent) != 1 || !rows[0].SentAt.Valid {
		t.Fatalf("retry: telegram %d, slack %d, row %+v", len(tg.sent), len(slack.sent), rows[0])
	}
}

func TestOutboxSlackOnly(t *testing.T) {
	st, _ := seed(t, "web")
	t0 := time.UnixMilli(1_000_000_000)
	addOutbox(t, st, t0)
	slack := &fakeNotifier{}
	o := &Outbox{St: st, Notifier: func(context.Context) (Notifier, error) { return nil, nil },
		Extras: func(context.Context) (map[string]Notifier, error) { return map[string]Notifier{"slack": slack}, nil }}
	o.Tick(context.Background(), t0)
	rows, _ := st.Q.ListOutbox(context.Background())
	if len(slack.sent) != 1 || !rows[0].SentAt.Valid {
		t.Fatalf("slack %v row %+v", slack.sent, rows[0])
	}
}

func TestOutboxChannelRemoved(t *testing.T) {
	st, _ := seed(t, "web")
	t0 := time.UnixMilli(1_000_000_000)
	addOutbox(t, st, t0)
	tg, slack := &fakeNotifier{}, &fakeNotifier{err: errors.New("down")}
	extras := map[string]Notifier{"slack": slack}
	o := &Outbox{St: st, Notifier: func(context.Context) (Notifier, error) { return tg, nil },
		Extras: func(context.Context) (map[string]Notifier, error) { return extras, nil }}
	o.Tick(context.Background(), t0)
	delete(extras, "slack")
	o.Tick(context.Background(), t0.Add(time.Hour))
	rows, _ := st.Q.ListOutbox(context.Background())
	if len(tg.sent) != 1 || !rows[0].SentAt.Valid {
		t.Fatalf("telegram %d row %+v", len(tg.sent), rows[0])
	}
}

func TestOutboxWatcherAndSlack(t *testing.T) {
	st, o, n, wk, now := outboxRig(t)
	slack := &fakeNotifier{}
	o.Extras = func(context.Context) (map[string]Notifier, error) { return map[string]Notifier{"slack": slack}, nil }
	put(t, st, now, "disk:srv_1:85", "open", false, "hel1 disk is 85% full.")
	put(t, st, now, "disk:srv_1:85", "close", true, "quiet close")
	o.Tick(ctx0, now)
	if _, _, inc := wk.Count(); inc < 1 {
		t.Fatal("the watcher got nothing")
	}
	if len(n.sent) != 0 || len(slack.sent) != 1 || slack.sent[0] != "hel1 disk is 85% full." {
		t.Fatalf("telegram %v slack %v", n.sent, slack.sent)
	}
}

type countingNotifier struct {
	calls int
	err   error
}

func (c *countingNotifier) Send(context.Context, notify.Message) error { c.calls++; return c.err }

func TestOutboxDownChannelOncePerTick(t *testing.T) {
	st, _ := seed(t, "web")
	t0 := time.UnixMilli(1_000_000_000)
	for range 3 {
		addOutbox(t, st, t0)
	}
	tg, mail := &fakeNotifier{}, &countingNotifier{err: errors.New("dial tcp: i/o timeout")}
	o := &Outbox{St: st, Notifier: func(context.Context) (Notifier, error) { return tg, nil },
		Extras: func(context.Context) (map[string]Notifier, error) { return map[string]Notifier{"email": mail}, nil }}
	o.Tick(context.Background(), t0)
	if mail.calls != 1 || len(tg.sent) != 3 {
		t.Fatalf("email tried %d times, telegram sent %d", mail.calls, len(tg.sent))
	}
}

func TestOutboxSaveAndGiveUp(t *testing.T) {
	st, _ := seed(t, "web")
	ctx := context.Background()
	t0 := time.UnixMilli(1_000_000_000)
	addOutbox(t, st, t0)
	st.DB.Exec(`CREATE TABLE writes (n INTEGER)`)
	st.DB.Exec(`CREATE TRIGGER count_writes AFTER UPDATE OF sent_channels ON outbox BEGIN INSERT INTO writes VALUES (1); END`)
	writes := func() (n int) { st.DB.QueryRow(`SELECT COUNT(*) FROM writes`).Scan(&n); return }
	tg, slack := &fakeNotifier{}, &fakeNotifier{err: errors.New("slack answered 500")}
	o := &Outbox{St: st, Notifier: func(context.Context) (Notifier, error) { return tg, nil },
		Extras: func(context.Context) (map[string]Notifier, error) { return map[string]Notifier{"slack": slack}, nil }}
	o.Tick(ctx, t0)
	o.Tick(ctx, t0.Add(time.Hour))
	if w := writes(); w != 1 {
		t.Fatalf("%d writes for one change", w)
	}
	o.Tick(ctx, t0.Add(24*time.Hour+time.Minute))
	rows, _ := st.Q.ListOutbox(ctx)
	if len(tg.sent) != 1 || !rows[0].FailedAt.Valid || rows[0].LastError != "slack: slack answered 500" {
		t.Fatalf("telegram %d row %+v", len(tg.sent), rows[0])
	}
	addOutbox(t, st, t0)
	st.DB.Exec(`CREATE TRIGGER refuse BEFORE UPDATE OF sent_channels ON outbox BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
	if err := o.Tick(ctx, t0); err == nil {
		t.Fatal("a failed write was ignored")
	}
}
