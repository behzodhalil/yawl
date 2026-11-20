package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "yawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrateFresh(t *testing.T) {
	s, ctx := open(t), context.Background()
	a, err := s.Q.UpsertProject(ctx, db.UpsertProjectParams{ID: NewID("prj_"), Name: "acme", CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Q.UpsertProject(ctx, db.UpsertProjectParams{ID: NewID("prj_"), Name: "acme", CreatedAt: 2, UpdatedAt: 2})
	if err != nil || a != b {
		t.Fatalf("upsert gave %q then %q (%v)", a, b, err)
	}
}

func TestTxRollsBack(t *testing.T) {
	s, ctx := open(t), context.Background()
	boom := errors.New("boom")
	err := s.Tx(ctx, func(q *db.Queries) error {
		if err := q.InsertEvent(ctx, db.InsertEventParams{ID: NewID("evt_"), Kind: "x", SubjectID: "s", Actor: "a", BodyJson: "{}"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if n, _ := s.Q.CountEvents(ctx); n != 0 {
		t.Fatalf("want 0 events, got %d", n)
	}
}

func TestLease(t *testing.T) {
	s, ctx := open(t), context.Background()
	now := time.UnixMilli(1_000_000)
	take := func(holder string, at time.Time) bool {
		ok, err := s.TakeLease(ctx, "deploy:svc_1", holder, at, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !take("a", now) {
		t.Fatal("a should take a free lease")
	}
	if take("b", now.Add(30*time.Second)) {
		t.Fatal("b must not take a held lease")
	}
	if !take("a", now.Add(30*time.Second)) {
		t.Fatal("a renews its own lease")
	}
	if !take("b", now.Add(2*time.Minute)) {
		t.Fatal("b takes an expired lease")
	}
}

func TestNewID(t *testing.T) {
	id := NewID("svc_")
	if len(id) != 16 || id[:4] != "svc_" || id == NewID("svc_") {
		t.Fatalf("bad id %q", id)
	}
}

func TestStatusDeploysUsesIndex(t *testing.T) {
	s := open(t)
	rows, err := s.DB.Query(`EXPLAIN QUERY PLAN SELECT id FROM deploys WHERE created_at > ?`, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		rows.Scan(&id, &parent, &unused, &detail)
		plan += detail + "\n"
	}
	if !strings.Contains(plan, "USING INDEX deploys_created_at") {
		t.Fatalf("plan:\n%s", plan)
	}
}
