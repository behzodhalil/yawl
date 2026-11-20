package workers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/selfbackup"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

func selfSetup(t *testing.T, at time.Time) (*SelfBackup, *selfbackup.Mem, *store.Store, *time.Time) {
	t.Helper()
	data, etc := t.TempDir(), t.TempDir()
	st, err := store.Open(filepath.Join(data, "yawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, n := range selfbackup.EtcFiles {
		os.WriteFile(filepath.Join(etc, n), []byte(n), 0o600)
	}
	pub := "Fr31BeJWJGZomibtI2Z5olMFdha7KY6dASzm1sc77h4="
	st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "recovery.pubkey", Value: pub})
	mem := &selfbackup.Mem{}
	now := at
	w := &SelfBackup{St: st, Data: data, Etc: etc, Now: func() time.Time { return now },
		NewStore: func(context.Context) (selfbackup.Store, error) { return mem, nil }}
	return w, mem, st, &now
}

func outboxTexts(t *testing.T, st *store.Store) []string {
	rows, _ := st.Q.ListOutbox(context.Background())
	var out []string
	for _, r := range rows {
		out = append(out, r.Body)
	}
	return out
}

func TestSelfBackupWorker(t *testing.T) {
	day := time.Date(2026, 10, 25, 3, 59, 0, 0, time.UTC)
	w, mem, st, now := selfSetup(t, day)
	ctx := context.Background()
	w.Tick(ctx)
	if len(mem.Objects) != 0 {
		t.Fatal("backed up before 04:00")
	}
	*now = day.Add(time.Minute)
	w.Tick(ctx)
	if _, ok := mem.Objects["yawl-server/2026-10-25T040000Z.tar.age"]; !ok || len(mem.Objects) != 1 {
		t.Fatalf("%v", memKeys(mem))
	}
	*now = day.Add(5 * time.Hour)
	w.Tick(ctx)
	if len(mem.Objects) != 1 {
		t.Fatal("a second backup the same day")
	}
	for i := range 7 {
		mem.Objects[fmt.Sprintf("yawl-server/2026-10-%02dT040000Z.tar.age", 10+i)] = []byte("x")
	}
	*now = day.Add(24*time.Hour + time.Minute)
	w.Tick(ctx)
	ks := memKeys(mem)
	if len(ks) != 7 || ks[0] != "yawl-server/2026-10-12T040000Z.tar.age" {
		t.Fatalf("kept %v", ks)
	}
	if out := outboxTexts(t, st); len(out) != 0 {
		t.Fatalf("messages on success: %v", out)
	}
}

func memKeys(m *selfbackup.Mem) []string { k, _ := m.List(context.Background(), ""); return k }

func TestSelfBackupFailureAndMissed(t *testing.T) {
	day := time.Date(2026, 10, 25, 4, 0, 0, 0, time.UTC)
	w, mem, st, now := selfSetup(t, day)
	ctx := context.Background()
	mem.PutErr = errors.New("access denied")
	w.Tick(ctx)
	w.Tick(ctx)
	out := outboxTexts(t, st)
	if len(out) != 1 || out[0] != "The panel's own backup failed: access denied." {
		t.Fatalf("%v", out)
	}
	*now = day.Add(27 * time.Hour)
	mem.PutErr = errors.New("access denied")
	w.Tick(ctx)
	w.Tick(ctx)
	out = outboxTexts(t, st)
	if !strings.Contains(strings.Join(out, "|"), "The panel's own backup has not succeeded in 26 hours (last: never).") {
		t.Fatalf("%v", out)
	}
	n := 0
	for _, o := range out {
		if strings.Contains(o, "26 hours") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("missed said %d times", n)
	}
}

func TestSelfBackupNoStorage(t *testing.T) {
	day := time.Date(2026, 10, 25, 4, 0, 0, 0, time.UTC)
	w, _, st, now := selfSetup(t, day)
	w.NewStore = func(context.Context) (selfbackup.Store, error) { return nil, errNoStorage }
	w.Tick(context.Background())
	if out := outboxTexts(t, st); len(out) != 0 {
		t.Fatalf("no storage still messaged: %v", out)
	}
	*now = day.Add(27 * time.Hour)
	w.Tick(context.Background())
	if out := outboxTexts(t, st); len(out) != 1 || !strings.Contains(out[0], "26 hours (last: never)") {
		t.Fatalf("%v", out)
	}
}

func TestSelfBackupRunCloses(t *testing.T) {
	w, _, st, _ := selfSetup(t, time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))
	ctx := context.Background()
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "watch.url", Value: "https://watch.example"})
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM outbox WHERE incident = 'panel-backup' AND action = 'close'`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d closes", n)
	}
}

func TestSelfBackupSigns(t *testing.T) {
	w, mem, st, _ := selfSetup(t, time.Date(2026, 10, 25, 4, 0, 0, 0, time.UTC))
	w.V = mustVault(t, st)
	k := []byte(strings.Repeat("k", 32))
	if err := vault.SetSecret(context.Background(), st.Q, w.V, selfbackup.KeySetting, k); err != nil {
		t.Fatal(err)
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	ks := memKeys(mem)
	if len(ks) != 1 || !strings.HasPrefix(ks[0], "yawl-server/2026-10-25T040000Z.") || !selfbackup.Valid(k, ks[0], mem.Objects[ks[0]]) {
		t.Fatalf("%v", ks)
	}
	if last, _ := st.Q.GetSetting(context.Background(), "server_backup.last_key"); last != ks[0] {
		t.Fatalf("last_key %q", last)
	}
}
