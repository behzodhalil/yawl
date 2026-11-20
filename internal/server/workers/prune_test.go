package workers

import (
	"context"
	"testing"
	"time"
)

func TestPruneYear(t *testing.T) {
	st, _ := seed(t, "web")
	at := time.Date(2026, 10, 28, 4, 31, 0, 0, time.UTC)
	st.DB.Exec(`INSERT INTO events (id, kind, subject_id, actor, body_json, created_at, updated_at) VALUES ('old','x','s','a','{}',?,1), ('young','x','s','a','{}',?,1)`,
		at.Add(-366*24*time.Hour).UnixMilli(), at.Add(-364*24*time.Hour).UnixMilli())
	st.DB.Exec(`INSERT INTO outbox (id, event_id, channel, body, next_attempt_at, sent_at, created_at, updated_at) VALUES ('o_old','old','telegram','x',1,2,1,1)`)
	now := at.Add(-2 * time.Minute)
	p := &Prune{St: st, Now: func() time.Time { return now }}
	p.Tick(context.Background())
	count := func() (n int) {
		st.DB.QueryRow(`SELECT count(*) FROM events WHERE id IN ('old','young')`).Scan(&n)
		return
	}
	if count() != 2 {
		t.Fatal("pruned before 04:30")
	}
	now = at
	p.Tick(context.Background())
	if count() != 1 {
		t.Fatalf("%d left", count())
	}
	st.DB.Exec(`INSERT INTO events (id, kind, subject_id, actor, body_json, created_at, updated_at) VALUES ('old2','x','s','a','{}',?,1)`, at.Add(-400*24*time.Hour).UnixMilli())
	now = at.Add(time.Hour)
	p.Tick(context.Background())
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM events WHERE id = 'old2'`).Scan(&n)
	if n != 1 {
		t.Fatal("pruned twice in a day")
	}
}

func TestPruneSessions(t *testing.T) {
	st, _ := seed(t, "web")
	at := time.Date(2026, 10, 28, 4, 31, 0, 0, time.UTC)
	st.DB.Exec(`INSERT INTO owner (id, email, password_hash, role, created_at, updated_at) VALUES ('own_1','a@acme.uz','x','admin',1,1)`)
	st.DB.Exec(`INSERT INTO sessions (id, owner_id, hash, expires_at, created_at, updated_at) VALUES ('ses_old','own_1','h1',?,1,1), ('ses_live','own_1','h2',?,1,1)`,
		at.Add(-time.Hour).UnixMilli(), at.Add(time.Hour).UnixMilli())
	if err := (&Prune{St: st, Now: func() time.Time { return at }}).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	var ids []string
	rows, _ := st.DB.Query(`SELECT id FROM sessions`)
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 1 || ids[0] != "ses_live" {
		t.Fatalf("%v", ids)
	}
}
