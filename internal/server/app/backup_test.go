package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func at(s string) int64 {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t.UnixMilli()
}

func TestRetention(t *testing.T) {
	now := time.UnixMilli(at("2026-10-24 04:00"))
	var ok []db.Backup
	for d := range 40 {
		day := now.AddDate(0, 0, -d)
		ok = append(ok, db.Backup{ID: day.Format("01-02"), State: "ok", CreatedAt: at(day.Format("2006-01-02") + " 03:00")})
	}
	ok = append(ok, db.Backup{ID: "10-24-ondemand", State: "ok", CreatedAt: at("2026-10-24 01:00")})
	drop := Keep(ok)
	var kept []string
	for _, b := range ok {
		if !slices.ContainsFunc(drop, func(d db.Backup) bool { return d.ID == b.ID }) {
			kept = append(kept, b.ID)
		}
	}
	slices.Sort(kept)
	want := []string{"10-04", "10-11", "10-18", "10-19", "10-20", "10-21", "10-22", "10-23", "10-24"}
	if !slices.Equal(kept, want) {
		t.Fatalf("kept %v\nwant %v", kept, want)
	}
	old := []db.Backup{{ID: "only", State: "ok", CreatedAt: at("2026-01-01 03:00")}}
	if drop := Keep(old); len(drop) != 0 {
		t.Fatalf("dropped the newest (and only) success: %v", drop)
	}
	mixed := []db.Backup{{ID: "f", State: "failed", CreatedAt: at("2025-01-01 03:00")}, {ID: "new", State: "ok", CreatedAt: at("2026-10-24 03:00")}}
	for _, d := range Keep(mixed) {
		if d.State != "ok" {
			t.Fatalf("dropped a %s row", d.State)
		}
	}
}

func TestBackupRefuses(t *testing.T) {
	st, svc := seed(t)
	a, ctx := New(st), context.Background()
	if _, err := a.Backup(ctx, svc, "owner"); err == nil || err.Error() != "api is not a database" {
		t.Fatalf("web service: %v", err)
	}
	st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_db", EnvironmentID: "env_1", Name: "acme-db", Kind: "postgres", Version: 17, BackupAt: "03:00", CreatedAt: 1, UpdatedAt: 1})
	if _, err := a.Backup(ctx, "svc_db", "owner"); err == nil || err.Error() != "acme-db has not been created yet; deploy it first" {
		t.Fatalf("never created: %v", err)
	}
	st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "v", EnvironmentID: "env_1", ServiceID: "svc_db", Key: "URL", AgentBox: "u", RecoveryBox: "r"})
	a.Now = func() time.Time { return time.UnixMilli(at("2026-10-24 03:00")) }
	id, err := a.Backup(ctx, "svc_db", "owner")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := st.Q.GetBackup(ctx, id)
	if want := fmt.Sprintf("yawl/acme/production/acme-db/20261024T030000Z-%s.dump.age", id); b.State != "queued" || b.ObjectKey != want {
		t.Fatalf("backup %+v, key want %s", b, want)
	}
	if !strings.HasPrefix(id, "bak_") {
		t.Fatalf("id %s", id)
	}
}

func TestRetentionRule(t *testing.T) {
	now := time.UnixMilli(at("2026-10-24 04:00"))
	var ok []db.Backup
	for d := range 120 {
		day := now.AddDate(0, 0, -d)
		ok = append(ok, db.Backup{ID: day.Format("01-02"), State: "ok", CreatedAt: at(day.Format("2006-01-02") + " 03:00")})
	}
	drop := KeepWith(ok, KeepRule{Daily: 3, Monthly: 3})
	var kept []string
	for _, b := range ok {
		if !slices.ContainsFunc(drop, func(d db.Backup) bool { return d.ID == b.ID }) {
			kept = append(kept, b.ID)
		}
	}
	slices.Sort(kept)
	if strings.Join(kept, ",") != "08-31,09-30,10-22,10-23,10-24" {
		t.Fatalf("%v", kept)
	}
}

func TestRetentionAfterFailures(t *testing.T) {
	now := time.UnixMilli(at("2026-10-24 04:00"))
	ok := []db.Backup{{ID: "10-24", State: "ok", CreatedAt: at("2026-10-24 03:00")}}
	for d := 40; d < 60; d++ {
		day := now.AddDate(0, 0, -d)
		ok = append(ok, db.Backup{ID: day.Format("01-02"), State: "ok", CreatedAt: at(day.Format("2006-01-02") + " 03:00")})
	}
	drop := Keep(ok)
	if len(ok)-len(drop) != 8 {
		var ids []string
		for _, d := range drop {
			ids = append(ids, d.ID)
		}
		t.Fatalf("kept %d of %d; dropped %v", len(ok)-len(drop), len(ok), ids)
	}
}
