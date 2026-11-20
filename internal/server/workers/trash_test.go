package workers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

var tDel = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func sentArgs(gw *fakeSender) []string {
	var out []string
	for _, c := range gw.sent {
		var m map[string]any
		json.Unmarshal(c.Args, &m)
		arg := ""
		for _, k := range []string{"Host", "Container", "Name", "ID"} {
			if v, ok := m[k].(string); ok {
				arg = v
				break
			}
		}
		if ks, ok := m["Keys"].([]any); ok {
			arg = strings.Trim(strings.ReplaceAll(strings.Trim(jsonOf(ks), "[]"), `"`, ""), " ")
		}
		out = append(out, c.Name+" "+arg)
	}
	return out
}

func jsonOf(v any) string { b, _ := json.Marshal(v); return string(b) }

func trashSetup(t *testing.T, kind string) (*store.Store, *Trash, *fakeSender, *app.App, *time.Time) {
	st, svc := seed(t, kind)
	deployOnce(t, st, svc, &fakeSender{})
	st.DB.Exec(`INSERT INTO domains (id, service_id, host, dns_state, cert_state, created_at, updated_at) VALUES ('dom_1', ?, 'api.acme.uz', 'ok', 'issued', 1, 1)`, svc)
	now := tDel
	a := app.New(st)
	a.Now = func() time.Time { return now }
	gw := &fakeSender{}
	return st, &Trash{St: st, GW: gw, App: a, Now: func() time.Time { return now }}, gw, a, &now
}

func TestTrashStops(t *testing.T) {
	st, w, gw, a, _ := trashSetup(t, "web")
	a.Delete(context.Background(), "service", "svc_1", "owner")
	w.Tick(context.Background())
	got := sentArgs(gw)
	want := []string{"RemoveRoute api-production.1-2-3-4.sslip.io", "RemoveRoute api.acme.uz"}
	if len(got) != 3 || !slices.Equal(got[:2], want) || !strings.HasPrefix(got[2], "Stop ") {
		t.Fatalf("%q", got)
	}
	var stopped int
	st.DB.QueryRow(`SELECT stopped FROM services WHERE id = 'svc_1'`).Scan(&stopped)
	w.Tick(context.Background())
	if stopped != 1 || len(gw.sent) != 3 {
		t.Fatalf("stopped %d, sent again %d", stopped, len(gw.sent))
	}
}

func TestTrashKeepsFreeHostAnotherClaims(t *testing.T) {
	st, w, gw, a, now := trashSetup(t, "web")
	a.Delete(context.Background(), "service", "svc_1", "owner")
	st.DB.Exec(`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('prj_z', 'zeta', 1, 1)`)
	st.DB.Exec(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_z', 'prj_z', 'production', 'srv_1', 1, 1)`)
	st.DB.Exec(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, created_at, updated_at)
		VALUES ('svc_z', 'env_z', 'api', 'web', 'https://x/y', 'main', 10000, '/', 60000, 30000, 1, 1)`)
	w.Tick(context.Background())
	*now = tDel.Add(49 * time.Hour)
	w.Tick(context.Background())
	got := sentArgs(gw)
	if slices.Contains(got, "RemoveRoute api-production.1-2-3-4.sslip.io") || !slices.Contains(got, "RemoveRoute api.acme.uz") {
		t.Fatalf("%q", got)
	}
}

func TestTrashStopsCron(t *testing.T) {
	_, w, gw, a, _ := trashSetup(t, "cron")
	a.Delete(context.Background(), "service", "svc_1", "owner")
	w.Tick(context.Background())
	if got := sentArgs(gw); !slices.Contains(got, "CronRemove svc_1") {
		t.Fatalf("%q", got)
	}
}

func TestTrashPurges(t *testing.T) {
	st, svc := seed(t, "web")
	dbID := addDB(t, st)
	deployOnce(t, st, dbID, &fakeSender{})
	st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, created_at, updated_at) VALUES ('bk_1', ?, 'yawl/acme/production/acme-db/k1', 'ok', 1, 1)`, dbID)
	st.DB.Exec(`INSERT INTO wal_batches (id, service_id, object_key, first_wal, last_wal, size, created_at) VALUES ('wal_1', ?, 'yawl/acme/production/acme-db/wal/w1', 'a', 'b', 1, 1)`, dbID)
	st.DB.Exec(`INSERT INTO settings (key, value) VALUES ('storage.endpoint','s3.example.com'), ('storage.bucket','b')`)
	upload := filepath.Join(t.TempDir(), "upl_1")
	os.WriteFile(upload, []byte("x"), 0o600)
	st.DB.Exec(`INSERT INTO imports (id, service_id, source, ref, file, token, created_at) VALUES ('imp_1', ?, 'upload', 'd.sql', ?, 't', 1)`, dbID, upload)
	st.DB.Exec(`INSERT INTO analytics_hours (service_id, at) VALUES (?, 1)`, dbID)
	st.DB.Exec(`INSERT INTO backup_copies (backup_id, copy, state, updated_at) VALUES ('bk_1', 'eu', 'ok', 1)`)
	st.DB.Exec(`INSERT INTO storage_copies (name, endpoint, bucket, created_at, updated_at) VALUES ('eu', 'eu.s3.example', 'eu-bucket', 1, 1)`)
	_ = svc
	v := mustVault(t, st)
	setStorageKeys(t, st, v)
	vault.SetSecret(context.Background(), st.Q, v, "storage.copy.eu.access_key", []byte("EAK"))
	vault.SetSecret(context.Background(), st.Q, v, "storage.copy.eu.secret_key", []byte("ESK"))
	now := tDel
	a := app.New(st)
	a.Now = func() time.Time { return now }
	gw := &fakeSender{}
	w := &Trash{St: st, GW: gw, App: a, V: v, Now: func() time.Time { return now }}
	a.Delete(context.Background(), "service", dbID, "owner")
	w.Tick(context.Background())
	now = tDel.Add(49 * time.Hour)
	w.Tick(context.Background())
	got := strings.Join(sentArgs(gw), "\n")
	for _, want := range []string{"Drain yawl-acme-production-acme-db", "RemoveVolume yawl-acme-production-acme-db-data", "DeleteBackups yawl/acme/production/acme-db/k1", "yawl/acme/production/acme-db/wal/w1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	copyDeleted := false
	for _, c := range gw.sent {
		var d proto.DeleteBackupsArgs
		if c.Name == proto.CmdDeleteBackups && json.Unmarshal(c.Args, &d) == nil && d.Storage.Bucket == "eu-bucket" && slices.Contains(d.Keys, "yawl/acme/production/acme-db/k1") {
			copyDeleted = true
		}
	}
	if !copyDeleted {
		t.Fatal("the purge left the copy's objects")
	}
	var rows, events int
	st.DB.QueryRow(`SELECT (SELECT count(*) FROM services WHERE id = ?) + (SELECT count(*) FROM deploys WHERE service_id = ?) + (SELECT count(*) FROM backups WHERE service_id = ?) + (SELECT count(*) FROM wal_batches WHERE service_id = ?)
		+ (SELECT count(*) FROM imports WHERE service_id = ?) + (SELECT count(*) FROM analytics_hours WHERE service_id = ?) + (SELECT count(*) FROM backup_copies)`, dbID, dbID, dbID, dbID, dbID, dbID).Scan(&rows)
	if _, err := os.Stat(upload); !os.IsNotExist(err) {
		t.Fatal("the purge left an upload")
	}
	st.DB.QueryRow(`SELECT count(*) FROM events WHERE kind = 'trash.deleted'`).Scan(&events)
	if rows != 0 || events != 1 {
		t.Fatalf("rows %d events %d", rows, events)
	}
	if texts := outbox(t, st); texts[len(texts)-1] != "acme/production/acme-db was purged." {
		t.Fatalf("%q", texts)
	}
}

func TestPurgeWaitsForServer(t *testing.T) {
	st, w, gw, a, now := trashSetup(t, "web")
	a.Delete(context.Background(), "service", "svc_1", "owner")
	st.DB.Exec(`UPDATE servers SET offline = 1`)
	*now = tDel.Add(49 * time.Hour)
	w.Tick(context.Background())
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM services WHERE id = 'svc_1'`).Scan(&n)
	if len(gw.sent) != 0 || n != 1 {
		t.Fatalf("sent %v, rows %d", gw.names(), n)
	}
	items, _ := a.Trash(context.Background())
	if items[0].Waiting != "waiting for hel1 to be online to remove its containers" {
		t.Fatalf("%+v", items)
	}
}

func TestTrashPurgesEmptyEnvironment(t *testing.T) {
	st, w, _, a, now := trashSetup(t, "web")
	st.DB.Exec(`INSERT INTO secret_sources (id, environment_id, provider, agent_box, recovery_box, created_at, updated_at) VALUES ('src_1', 'env_1', 'doppler', 'a', 'r', 1, 1)`)
	a.Delete(context.Background(), "environment", "env_1", "owner")
	w.Tick(context.Background())
	*now = tDel.Add(49 * time.Hour)
	w.Tick(context.Background())
	var n int
	st.DB.QueryRow(`SELECT (SELECT count(*) FROM environments) + (SELECT count(*) FROM services) + (SELECT count(*) FROM secret_sources)`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows left", n)
	}
}

var _ = proto.CmdStop

func setStorageKeys(t *testing.T, st *store.Store, v *vault.Vault) {
	vault.SetSecret(context.Background(), st.Q, v, "storage.access_key", []byte("AK"))
	vault.SetSecret(context.Background(), st.Q, v, "storage.secret_key", []byte("SK"))
}

func TestTrashWaitsWhileAdopting(t *testing.T) {
	st, w, gw, a, now := trashSetup(t, "web")
	a.Delete(context.Background(), "service", "svc_1", "owner")
	st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "adopt.pending", Value: "1"})
	*now = tDel.Add(49 * time.Hour)
	w.Tick(context.Background())
	if len(gw.sent) != 0 {
		t.Fatalf("acted while adopting: %v", gw.names())
	}
}

func TestTrashStopWaitsForDeploy(t *testing.T) {
	st, w, gw, a, _ := trashSetup(t, "web")
	a.Delete(context.Background(), "service", "svc_1", "owner")
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_run','svc_1','c','starting',2,2)`)
	w.Tick(context.Background())
	var stopped int
	st.DB.QueryRow(`SELECT stopped FROM services WHERE id = 'svc_1'`).Scan(&stopped)
	if len(gw.sent) != 0 || stopped != 0 {
		t.Fatalf("stopped under a running deploy: %v", gw.names())
	}
}

func TestPurgeRechecksMark(t *testing.T) {
	st, w, _, a, now := trashSetup(t, "web")
	a.Delete(context.Background(), "service", "svc_1", "owner")
	*now = tDel.Add(49 * time.Hour)
	st.DB.Exec(`UPDATE services SET deleted_at = NULL, purge_at = NULL WHERE id = 'svc_1'`)
	w.purgeOne(context.Background(), "svc_1")
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM services WHERE id = 'svc_1'`).Scan(&n)
	if n != 1 {
		t.Fatal("a restored service was purged")
	}
}

func TestPurgeClosesPanelIncidents(t *testing.T) {
	st, w, _, a, now := trashSetup(t, "cron")
	ctx := context.Background()
	watched(st)
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "cron.scheduled.svc_1", Value: "{}"})
	a.Delete(ctx, "service", "svc_1", "owner")
	w.Tick(ctx)
	*now = tDel.Add(49 * time.Hour)
	w.Tick(ctx)
	var closes []string
	for _, k := range keyed(t, st) {
		if f := strings.Split(k, "|"); f[1] == "close" && f[2] == "1" {
			closes = append(closes, f[0])
		}
	}
	for _, want := range []string{"deploy:svc_1", "service:svc_1", "oom:svc_1", "cron:svc_1"} {
		if !slices.Contains(closes, want) {
			t.Fatalf("%s not closed: %v", want, closes)
		}
	}
	if v := setting(ctx, st.Q, "cron.scheduled.svc_1"); v != "" {
		t.Fatalf("cron.scheduled kept: %q", v)
	}
}

type silentSender struct{ n int }

func (s *silentSender) Send(context.Context, string, proto.Command) (proto.Result, error) {
	s.n++
	return proto.Result{}, context.DeadlineExceeded
}

func TestTrashSkipsSilentServer(t *testing.T) {
	st, w, _, a, _ := trashSetup(t, "web")
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, created_at, updated_at)
		VALUES ('svc_2', 'env_1', 'web', 'worker', '', '', 0, '', 0, 0, 1, 1)`)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, container, state, created_at, updated_at) VALUES ('dep_w', 'svc_2', 'c', 'acme-production-web-1', 'live', 1, 1)`)
	st.DB.Exec(`UPDATE services SET current_deploy_id = 'dep_w' WHERE id = 'svc_2'`)
	a.Delete(ctx, "environment", "env_1", "owner")
	gw := &silentSender{}
	w.GW = gw
	w.Tick(ctx)
	if gw.n != 1 {
		t.Fatalf("%d sends to a silent server in one tick", gw.n)
	}
	w.Tick(ctx)
	if gw.n != 2 {
		t.Fatalf("%d sends after two ticks", gw.n)
	}
}

func TestDeleteSupersedesQueuedFinishesStarted(t *testing.T) {
	st, _, _, a, _ := trashSetup(t, "web")
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_q', 'svc_1', 'c', 'queued', 2, 2)`)
	a.Delete(ctx, "service", "svc_1", "owner")
	var state string
	st.DB.QueryRow(`SELECT state FROM deploys WHERE id = 'dep_q'`).Scan(&state)
	if state != "superseded" {
		t.Fatalf("queued deploy %s", state)
	}
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_b', 'svc_1', 'c', 'building', 3, 3)`)
	run, _ := st.Q.RunnableDeploys(ctx)
	if len(run) != 1 || run[0].ID != "dep_b" {
		t.Fatalf("runnable %v", run)
	}
	st.DB.Exec(`UPDATE deploys SET state = 'queued' WHERE id = 'dep_b'`)
	if run, _ := st.Q.RunnableDeploys(ctx); len(run) != 0 {
		t.Fatalf("a deleted service's queued deploy runs: %v", run)
	}
}
