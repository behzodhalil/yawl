package workers

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func volumeWorld(t *testing.T) (*Backups, *store.Store, *fakeSender, *clock) {
	b, st, gw, c := backupWorld(t, true)
	st.DB.Exec(`UPDATE services SET volumes_json = '[{"name":"uploads","path":"/u"},{"name":"index","path":"/i"}]', backup_at = '03:30' WHERE id = 'svc_1'`)
	deployOnce(t, st, "svc_1", &fakeSender{})
	gw.sent = nil
	return b, st, gw, c
}

func TestTwoVolumesTwoBackups(t *testing.T) {
	b, st, gw, c := volumeWorld(t)
	c.t = utc("2026-10-24 03:30")
	b.Tick(context.Background())
	var keys []string
	for _, cmd := range gw.sent {
		if cmd.Name == proto.CmdBackupVolume {
			var a proto.BackupVolumeArgs
			json.Unmarshal(cmd.Args, &a)
			keys = append(keys, a.Name+" "+a.Key)
		}
	}
	if len(keys) != 2 || !strings.Contains(keys[0]+keys[1], "yawl/acme/production/api/volumes/uploads/20261024T033000Z-") || !strings.Contains(keys[0]+keys[1], "/volumes/index/") {
		t.Fatalf("%q", keys)
	}
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM backups WHERE service_id = 'svc_1' AND volume <> '' AND state = 'ok'`).Scan(&n)
	b.Tick(context.Background())
	var m int
	st.DB.QueryRow(`SELECT count(*) FROM backups WHERE service_id = 'svc_1'`).Scan(&m)
	if n != 2 || m != 2 {
		t.Fatalf("ok %d, all %d", n, m)
	}
}

func TestVolumeKeep7(t *testing.T) {
	b, st, gw, c := volumeWorld(t)
	for d := 1; d <= 10; d++ {
		c.t = utc("2026-10-24 03:30").AddDate(0, 0, d)
		b.Tick(context.Background())
	}
	for _, cmd := range gw.sent {
		if cmd.Name == proto.CmdDeleteBackups {
			var a proto.DeleteBackupsArgs
			json.Unmarshal(cmd.Args, &a)
			vol := ""
			for _, k := range a.Keys {
				if !strings.Contains(k, "/volumes/") {
					continue
				}
				v := strings.Split(strings.Split(k, "/volumes/")[1], "/")[0]
				if vol != "" && v != vol {
					t.Fatalf("one prune mixed volumes: %v", a.Keys)
				}
				vol = v
			}
		}
	}
	var kept, idx, dbKept int
	st.DB.QueryRow(`SELECT count(*) FROM backups WHERE service_id = 'svc_1' AND volume = 'uploads' AND state = 'ok'`).Scan(&kept)
	st.DB.QueryRow(`SELECT count(*) FROM backups WHERE service_id = 'svc_1' AND volume = 'index' AND state = 'ok'`).Scan(&idx)
	st.DB.QueryRow(`SELECT count(*) FROM backups WHERE service_id = 'svc_db' AND state = 'ok'`).Scan(&dbKept)
	if kept != dbKept || idx != dbKept || kept >= 10 {
		t.Fatalf("uploads %d, index %d, database %d", kept, idx, dbKept)
	}
}

func TestVolumeBackupFailedAlerts(t *testing.T) {
	b, st, gw, c := volumeWorld(t)
	gw.fail = map[string]string{proto.CmdBackupVolume: "403"}
	c.t = utc("2026-10-24 03:30")
	b.Tick(context.Background())
	got := keyed(t, st)
	if len(got) == 0 || !strings.Contains(got[0], ":failed|open|0|acme/production/api|api volume ") || !strings.HasPrefix(got[0], "backup:svc_1:") || !strings.HasSuffix(got[0], " backup failed: 403") {
		t.Fatalf("%q", got)
	}
}

func TestRestoreVolumeDeploy(t *testing.T) {
	_, st, _, c := volumeWorld(t)
	st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, volume, sha256, created_at, updated_at) VALUES ('bk_v','svc_1','yawl/acme/production/api/volumes/uploads/x.tar.age','ok','uploads','sum-v',1,1)`)
	a := app.New(st)
	a.Now = c.now
	id, err := a.RestoreVolume(context.Background(), "svc_1", "bk_v", "sealed-key", "owner")
	if err != nil {
		t.Fatal(err)
	}
	gw := &fakeSender{}
	r := NewRunner(st, gw, nil)
	r.RestoreKeys, r.V = a, mustVault(t, st)
	if err := r.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(gw.names(), ","); !strings.HasPrefix(got, "Stop,RestoreVolume,Start,HealthCheck,SwitchRoute") {
		t.Fatalf("%s", got)
	}
	var args proto.RestoreVolumeArgs
	json.Unmarshal(gw.sent[1].Args, &args)
	if args.Name != "uploads" || args.RecoveryKey != "sealed-key" || args.Key != "yawl/acme/production/api/volumes/uploads/x.tar.age" || args.SHA256 != "sum-v" {
		t.Fatalf("%+v", args)
	}
}

func TestRestoreVolumeFailedStartsOld(t *testing.T) {
	_, st, _, c := volumeWorld(t)
	st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, volume, created_at, updated_at) VALUES ('bk_v','svc_1','k','ok','uploads',1,1)`)
	a := app.New(st)
	a.Now = c.now
	id, err := a.RestoreVolume(context.Background(), "svc_1", "bk_v", "wrong", "owner")
	if err != nil {
		t.Fatal(err)
	}
	gw := &fakeSender{fail: map[string]string{proto.CmdRestoreVolume: "age: no identity matched any of the recipients"}}
	r := NewRunner(st, gw, nil)
	r.RestoreKeys, r.V = a, mustVault(t, st)
	r.Run(context.Background(), id)
	d, _ := st.Q.GetDeploy(context.Background(), id)
	if got := strings.Join(gw.names(), ","); d.State != "rolled_back" || got != "Stop,RestoreVolume,Start,SwitchRoute" {
		t.Fatalf("state %s, sent %s", d.State, got)
	}
}

func TestRestoreVolumeStopFailedKeepsOldStopped(t *testing.T) {
	_, st, _, c := volumeWorld(t)
	st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, volume, created_at, updated_at) VALUES ('bk_v','svc_1','k','ok','uploads',1,1)`)
	a := app.New(st)
	a.Now = c.now
	id, err := a.RestoreVolume(context.Background(), "svc_1", "bk_v", "k", "owner")
	if err != nil {
		t.Fatal(err)
	}
	gw := &fakeSender{fail: map[string]string{proto.CmdHealthCheck: "boom"}, failKey: func(k string) bool { return strings.HasSuffix(k, ":rollback-stop") }}
	r := NewRunner(st, gw, nil)
	r.RestoreKeys, r.V = a, mustVault(t, st)
	r.Run(context.Background(), id)
	d, _ := st.Q.GetDeploy(context.Background(), id)
	if got := strings.Join(gw.names(), ","); d.State != "failed" || got != "Stop,RestoreVolume,Start,HealthCheck,Stop" {
		t.Fatalf("state %s, sent %s", d.State, got)
	}
}

func TestRestoreVolumeRefusals(t *testing.T) {
	_, st, _, _ := volumeWorld(t)
	st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, volume, created_at, updated_at) VALUES ('bk_d','svc_1','k','ok','',1,1), ('bk_f','svc_1','k','failed','uploads',1,1)`)
	a := app.New(st)
	for id, want := range map[string]string{"bk_d": "bk_d is not a volume backup of api", "bk_f": "backup bk_f is failed, not ok", "nope": "nope is not a volume backup of api"} {
		if _, err := a.RestoreVolume(context.Background(), "svc_1", id, "k", "owner"); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if _, err := a.BackupVolume(context.Background(), "svc_1", "nope", "owner"); err == nil || err.Error() != "api has no volume nope" {
		t.Fatalf("%v", err)
	}
}

func moveVolume(t *testing.T, step string) (*store.Store, *Move, *fakeSender) {
	st, w, gw := seedMoving(t, false, step)
	st.DB.Exec(`UPDATE services SET volumes_json = '[{"Name":"uploads","Path":"/u"}]' WHERE id = 'svc_api'`)
	w.V = mustVault(t, st)
	st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "storage.endpoint", Value: "s3.example.com"})
	st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "storage.bucket", Value: "b"})
	setStorageKeys(t, st, w.V)
	return st, w, gw
}

func TestMoveCarriesVolume(t *testing.T) {
	st, w, gw := moveVolume(t, "stop")
	ctx := context.Background()
	w.Tick(ctx)
	var id, vol string
	st.DB.QueryRow(`SELECT id, volume FROM backups WHERE service_id = 'svc_api'`).Scan(&id, &vol)
	if step, _, _ := moveRow(t, st); step != "backup" || vol != "uploads" {
		t.Fatalf("step %s, backup %s %q", step, id, vol)
	}
	st.DB.Exec(`UPDATE backups SET state = 'ok', sha256 = 'sum-m', object_key = 'yawl/acme/staging/api/volumes/uploads/x.tar.age' WHERE id = ?`, id)
	w.App.KeepMoveKey("mv_1", "rk")
	gw.sent, gw.to = nil, nil
	w.Tick(ctx)
	var a proto.RestoreVolumeArgs
	for i, c := range gw.sent {
		if c.Name == proto.CmdRestoreVolume {
			json.Unmarshal(c.Args, &a)
			if gw.to[i] != "srv_2" {
				t.Fatalf("restored on %s", gw.to[i])
			}
		}
	}
	if a.Name != "uploads" || a.Service != "api" || a.Env != "staging" || a.Key != "yawl/acme/staging/api/volumes/uploads/x.tar.age" || a.SHA256 != "sum-m" || a.RecoveryKey != "rk" {
		t.Fatalf("%+v sent %v", a, gw.names())
	}
	if step, state, cause := moveRow(t, st); step != "deploy" || state != "running" {
		t.Fatal(step, state, cause)
	}
	for _, d := range moveDeploys(t, st) {
		st.DB.Exec(`UPDATE deploys SET state = 'live' WHERE id = ?`, d[0])
	}
	w.Tick(ctx)
	gw.sent, gw.to = nil, nil
	w.Tick(ctx)
	got := sentArgs(gw)
	iDrain, iVol := slices.Index(got, "Drain acme-staging-api-1"), slices.Index(got, "RemoveVolume yawl_acme_staging_api_uploads")
	if iDrain < 0 || iVol < iDrain {
		t.Fatalf("container first, then the volume: %v", got)
	}
}

func TestMoveCarriesDetachedVolume(t *testing.T) {
	st, w, gw := moveVolume(t, "stop")
	st.DB.Exec(`UPDATE services SET volumes_kept = '["old","uploads"]' WHERE id = 'svc_api'`)
	ctx := context.Background()
	w.Tick(ctx)
	st.DB.Exec(`UPDATE backups SET state = 'ok', object_key = 'k-' || volume WHERE service_id = 'svc_api'`)
	var vols string
	st.DB.QueryRow(`SELECT group_concat(volume, ' ') FROM (SELECT volume FROM backups WHERE service_id = 'svc_api' ORDER BY volume)`).Scan(&vols)
	if vols != "old uploads" {
		t.Fatalf("backed up %v", vols)
	}
	w.App.KeepMoveKey("mv_1", "rk")
	gw.sent, gw.to = nil, nil
	w.Tick(ctx)
	var restored []string
	for i, c := range gw.sent {
		if c.Name == proto.CmdRestoreVolume && gw.to[i] == "srv_2" {
			var a proto.RestoreVolumeArgs
			json.Unmarshal(c.Args, &a)
			restored = append(restored, a.Name+" "+a.Key)
		}
	}
	if !slices.Equal(restored, []string{"old k-old", "uploads k-uploads"}) {
		t.Fatalf("%v sent %v", restored, gw.names())
	}
	if slices.Contains(gw.names(), proto.CmdRemoveVolume) {
		t.Fatal("a volume removed before the move finished")
	}
	for _, d := range moveDeploys(t, st) {
		st.DB.Exec(`UPDATE deploys SET state = 'live' WHERE id = ?`, d[0])
	}
	w.Tick(ctx)
	gw.sent, gw.to = nil, nil
	w.Tick(ctx)
	got := sentArgs(gw)
	if !slices.Contains(got, "RemoveVolume yawl_acme_staging_api_old") || !slices.Contains(got, "RemoveVolume yawl_acme_staging_api_uploads") {
		t.Fatalf("%v", got)
	}
}

func TestOfflineMoveSkipsDetachedVolumeWithoutBackup(t *testing.T) {
	st, w, gw := moveVolume(t, "restore")
	switched(st, `{}`, `{}`)
	st.DB.Exec(`UPDATE services SET volumes_json = '[]', volumes_kept = '["old"]' WHERE id = 'svc_api'`)
	w.App.KeepMoveKey("mv_1", "rk")
	w.Tick(context.Background())
	if slices.Contains(gw.names(), proto.CmdRestoreVolume) {
		t.Fatalf("%v", sentArgs(gw))
	}
	if step, state, cause := moveRow(t, st); state == "failed" || step == "restore" {
		t.Fatal(step, state, cause)
	}
}

func TestMoveVolumeFromBackup(t *testing.T) {
	st, w, gw := moveVolume(t, "restore")
	switched(st, `{}`, `{}`)
	st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, volume, state, created_at, updated_at) VALUES ('bk_old', 'svc_api', 'k1', 'uploads', 'ok', 5, 5), ('bk_last', 'svc_api', 'k2', 'uploads', 'ok', 9, 9)`)
	w.App.KeepMoveKey("mv_1", "rk")
	w.Tick(context.Background())
	if got := sentArgs(gw); len(got) == 0 || got[0] != "RestoreVolume uploads" {
		t.Fatalf("%v", got)
	}
	var a proto.RestoreVolumeArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	if a.Key != "k2" {
		t.Fatalf("%+v", a)
	}
}

func TestMoveVolumeRestoreFails(t *testing.T) {
	st, w, gw := moveVolume(t, "restore")
	switched(st, `{}`, `{}`)
	st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, volume, state, created_at, updated_at) VALUES ('bk_last', 'svc_api', 'k2', 'uploads', 'ok', 9, 9)`)
	w.App.KeepMoveKey("mv_1", "rk")
	gw.fail = map[string]string{proto.CmdRestoreVolume: "age: no identity matched"}
	w.Tick(context.Background())
	if step, state, cause := moveRow(t, st); step != "restore" || state != "failed" || cause != "restore api volume uploads: age: no identity matched" {
		t.Fatal(step, state, cause)
	}
}

func TestTrashPurgesVolumes(t *testing.T) {
	st, w, gw, a, now := trashSetup(t, "worker")
	st.DB.Exec(`UPDATE services SET volumes_json = '[{"Name":"uploads","Path":"/u"}]' WHERE id = 'svc_1'`)
	deployOnce(t, st, "svc_1", &fakeSender{})
	a.Delete(context.Background(), "service", "svc_1", "owner")
	w.Tick(context.Background())
	*now = tDel.Add(49 * time.Hour)
	w.Tick(context.Background())
	if got := strings.Join(sentArgs(gw), "\n"); !strings.Contains(got, "RemoveVolume yawl_acme_production_api_uploads") {
		t.Fatalf("%s", got)
	}
}

func TestDetachedVolumeKept(t *testing.T) {
	st, w, gw, a, now := trashSetup(t, "worker")
	st.DB.Exec(`UPDATE services SET volumes_json = '[{"Name":"uploads","Path":"/u"}]' WHERE id = 'svc_1'`)
	deployOnce(t, st, "svc_1", &fakeSender{})
	st.DB.Exec(`UPDATE services SET volumes_json = '[]' WHERE id = 'svc_1'`)
	dep := &fakeSender{}
	deployOnce(t, st, "svc_1", dep)
	if slices.Contains(dep.names(), proto.CmdRemoveVolume) {
		t.Fatal("a deploy removed the detached volume")
	}
	if _, err := a.BackupVolume(context.Background(), "svc_1", "uploads", "schedule"); err == nil {
		t.Fatal("a detached volume is still backed up")
	}
	a.Delete(context.Background(), "service", "svc_1", "owner")
	w.Tick(context.Background())
	*now = tDel.Add(49 * time.Hour)
	w.Tick(context.Background())
	if got := strings.Join(sentArgs(gw), "\n"); !strings.Contains(got, "RemoveVolume yawl_acme_production_api_uploads") {
		t.Fatalf("%s", got)
	}
}

func TestRestoreVolumeRefusedWhileDeploying(t *testing.T) {
	_, st, _, _ := volumeWorld(t)
	st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, volume, created_at, updated_at) VALUES ('bk_v','svc_1','k','ok','uploads',1,1)`)
	a := app.New(st)
	push, err := a.Deploy(context.Background(), "svc_1", "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RestoreVolume(context.Background(), "svc_1", "bk_v", "k", "owner"); err == nil || err.Error() != "api has a deploy in progress; wait for it to finish." {
		t.Fatal(err)
	}
	if d, _ := st.Q.GetDeploy(context.Background(), push); d.State != "queued" {
		t.Fatalf("the push was %s", d.State)
	}
}

func TestReleaseRunsOnVolume(t *testing.T) {
	st, svc := seed(t, "worker")
	st.DB.Exec(`UPDATE services SET volumes_json = '[{"Name":"data","Path":"/app/data"}]', release_json = '["./migrate"]' WHERE id = ?`, svc)
	deployOnce(t, st, svc, &fakeSender{})
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	if got := strings.Join(gw.names(), ","); !strings.HasPrefix(got, "Build,Stop,Release,Start") {
		t.Fatalf("%s", got)
	}
	var a proto.ReleaseArgs
	json.Unmarshal(gw.sent[2].Args, &a)
	if len(a.Volumes) != 1 || a.Volumes[0].Name != "data" {
		t.Fatalf("%+v", a)
	}
	bad := &fakeSender{fail: map[string]string{proto.CmdRelease: "exit 1"}}
	id, _ := deployOnce(t, st, svc, bad)
	if d, _ := st.Q.GetDeploy(context.Background(), id); d.State != "rolled_back" || !strings.HasSuffix(strings.Join(bad.names(), ","), "Release,Start") {
		t.Fatalf("%s: %v", d.State, bad.names())
	}
}

func TestVolumeFailureKeyedPerVolume(t *testing.T) {
	b, st, gw, c := volumeWorld(t)
	watched(st)
	gw.failKey = func(key string) bool { return false }
	gw.fail = nil
	b.GW = &volFail{inner: gw, volume: "uploads"}
	c.t = utc("2026-10-24 03:30")
	b.Tick(context.Background())
	var open, closeUploads bool
	for _, r := range keyed(t, st) {
		open = open || strings.HasPrefix(r, "backup:svc_1:uploads:failed|open|")
		closeUploads = closeUploads || strings.HasPrefix(r, "backup:svc_1:uploads:failed|close|")
	}
	if !open || closeUploads {
		t.Fatalf("%q", keyed(t, st))
	}
}

type volFail struct {
	inner  *fakeSender
	volume string
}

func (v *volFail) Send(ctx context.Context, server string, c proto.Command) (proto.Result, error) {
	if c.Name == proto.CmdBackupVolume {
		var a proto.BackupVolumeArgs
		json.Unmarshal(c.Args, &a)
		if a.Name == v.volume {
			return proto.Result{Key: c.Key, Cause: "403"}, nil
		}
	}
	return v.inner.Send(ctx, server, c)
}

func TestOfflineMoveKeepsOldVolume(t *testing.T) {
	st, w, _ := moveVolume(t, "switch_server")
	w.Tick(context.Background())
	if m := mustMove(t, st); strings.Contains(m.OldJson, "uploads") {
		t.Fatalf("an offline move will remove the old volume: %s", m.OldJson)
	}
	st2, w2, _ := moveVolume(t, "switch_server")
	st2.DB.Exec(`UPDATE env_moves SET backups_json = '{"svc_api/uploads":"bk_fresh"}' WHERE id = 'mv_1'`)
	w2.Tick(context.Background())
	if m := mustMove(t, st2); !strings.Contains(m.OldJson, `"svc_api/uploads":"yawl_acme_staging_api_uploads"`) {
		t.Fatalf("a moved volume is not cleaned: %s", m.OldJson)
	}
}
