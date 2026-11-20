package workers

import (
	"context"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/selfbackup"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestAdoptPausesWorkers(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	_, r := deployOnce(t, st, svc, &fakeSender{})
	st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_db", EnvironmentID: "env_1", Name: "db", Kind: "postgres", Version: 17, BackupAt: "00:00", CreatedAt: 1, UpdatedAt: 1})
	addDomain(t, st.Q, "d1", svc, "a.uz")
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "adopt.pending", Value: "1"})
	app.New(st).Deploy(ctx, svc, "", "github")

	gw := &fakeSender{fail: map[string]string{"Probe": "connection refused"}}
	noon := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	var deploysBefore, outboxBefore int
	st.DB.QueryRow(`SELECT COUNT(*) FROM deploys`).Scan(&deploysBefore)
	st.DB.QueryRow(`SELECT COUNT(*) FROM outbox`).Scan(&outboxBefore)
	r.GW = gw
	r.Tick(ctx)
	r.Wait()
	w := &Watch{St: st, GW: gw, App: app.New(st), Now: func() time.Time { return noon }}
	for range 4 {
		w.Tick(ctx)
	}
	(&Domains{St: st, GW: gw}).Tick(ctx)
	(&Backups{St: st, GW: gw, Now: func() time.Time { return noon }}).Tick(ctx)
	mem := &selfbackup.Mem{}
	(&SelfBackup{St: st, Data: t.TempDir(), Etc: t.TempDir(), Now: func() time.Time { return noon },
		NewStore: func(context.Context) (selfbackup.Store, error) { return mem, nil }}).Tick(ctx)

	if len(gw.sent) != 0 {
		t.Fatalf("sent %v while adopting", gw.names())
	}
	var deploys, outbox, backups int
	st.DB.QueryRow(`SELECT COUNT(*) FROM deploys`).Scan(&deploys)
	st.DB.QueryRow(`SELECT COUNT(*) FROM outbox`).Scan(&outbox)
	st.DB.QueryRow(`SELECT COUNT(*) FROM backups`).Scan(&backups)
	var queued string
	st.DB.QueryRow(`SELECT state FROM deploys ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&queued)
	if deploys != deploysBefore || outbox != outboxBefore || backups != 0 || len(mem.Objects) != 0 || queued != "queued" {
		t.Fatalf("deploys %d→%d outbox %d→%d backups %d self %d last %s", deploysBefore, deploys, outboxBefore, outbox, backups, len(mem.Objects), queued)
	}
}
