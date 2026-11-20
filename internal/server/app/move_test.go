package app

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
)

var moveNow = time.UnixMilli(1_800_000_000_000)

func seedMove(t *testing.T, db bool) (*store.Store, *App) {
	t.Helper()
	st, _ := seed(t)
	ex := func(q string, args ...any) {
		t.Helper()
		if _, err := st.DB.Exec(q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	ex(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, last_heartbeat, created_at, updated_at) VALUES ('srv_2', 'hk1', '5.6.7.8', 'PUBK', '', '', 1, 1, 1)`)
	ex(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_s', 'prj_1', 'staging', 'srv_1', 1, 1)`)
	svcs := [][2]string{{"api", "web"}, {"web", "web"}}
	if db {
		svcs = append(svcs, [2]string{"staging-db", "postgres"})
	}
	for _, s := range svcs {
		ex(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, current_deploy_id, created_at, updated_at)
			VALUES (?, 'env_s', ?, ?, 'https://x/y', 'main', 10000, '/', 60000, 30000, ?, 1, 1)`, "svc_"+s[0], s[0], s[1], "dep_"+s[0])
		ex(`INSERT INTO deploys (id, service_id, commit_sha, image, container, state, created_at, updated_at) VALUES (?, ?, 'c1', ?, ?, 'live', 1, 1)`,
			"dep_"+s[0], "svc_"+s[0], "yawl/"+s[0]+":dep_"+s[0], "acme-staging-"+s[0]+"-1")
	}
	ex(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, ref, created_at, updated_at) VALUES ('var_s', 'env_s', '', 'SHARED', 'a1', 'r1', '', 1, 11)`)
	ex(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, ref, created_at, updated_at) VALUES ('var_a', 'env_s', 'svc_api', 'TOKEN', 'a2', 'r2', '', 1, 12)`)
	ex(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, ref, created_at, updated_at) VALUES ('var_r', 'env_s', 'svc_api', 'DATABASE_URL', '', '', 'staging-db.url', 1, 13)`)
	if db {
		ex(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, ref, created_at, updated_at) VALUES ('var_p', 'env_s', 'svc_staging-db', 'PASSWORD', 'a3', 'r3', '', 1, 14)`)
		ex(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, ref, created_at, updated_at) VALUES ('var_u', 'env_s', 'svc_staging-db', 'URL', 'a4', 'r4', '', 1, 15)`)
	}
	ex(`INSERT INTO settings (key, value) VALUES ('storage.endpoint', 'https://s3.test'), ('recovery.pubkey', 'RPUB')`)
	a := New(st)
	a.Now = func() time.Time { return moveNow }
	return st, a
}

var wantLines = []string{
	"move staging from hel1 to hk1",
	"stop api and web on hel1 (staging is down from here until the switch)",
	"back up staging-db on hel1",
	"create staging-db on hk1 and restore it from that backup",
	"build and start api and web on hk1",
	"switch api-staging.5-6-7-8.sslip.io, web-staging.5-6-7-8.sslip.io",
	"stop staging-db on hel1 (its volume is kept)",
}

func TestPlanMoveLines(t *testing.T) {
	_, a := seedMove(t, true)
	p, err := a.PlanMove(context.Background(), "acme", "staging", "hk1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Lines, wantLines) || !p.Restore || p.ToPubkey != "PUBK" || p.RecoveryPubkey != "RPUB" {
		t.Fatalf("%+v", p)
	}
	var ids []string
	for _, v := range p.Vars {
		ids = append(ids, v.ID+":"+v.Service+":"+v.RecoveryBox)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"var_a:api:r2", "var_p:staging-db:r3", "var_s::r1", "var_u:staging-db:r4"}) {
		t.Fatal(ids)
	}
}

func TestPlanMoveOldServerOffline(t *testing.T) {
	st, a := seedMove(t, true)
	st.DB.Exec(`UPDATE servers SET offline = 1 WHERE id = 'srv_1'`)
	at := moveNow.Add(-9*time.Hour - 10*time.Minute).UnixMilli()
	st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, finished_at, created_at, updated_at) VALUES ('bk_1', 'svc_staging-db', 'k', 'ok', ?, ?, ?)`, at, at, at)
	p, err := a.PlanMove(context.Background(), "acme", "staging", "hk1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"move staging from hel1 to hk1",
		"hel1 is offline: staging-db is restored from its last backup, 9 hours old; changes after it are lost",
		wantLines[3], wantLines[4], wantLines[5], "stop staging on hel1 once it is back online"}
	if !slices.Equal(p.Lines, want) {
		t.Fatalf("%q", p.Lines)
	}
}

func TestPlanMoveNoDatabase(t *testing.T) {
	_, a := seedMove(t, false)
	p, err := a.PlanMove(context.Background(), "acme", "staging", "hk1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{wantLines[0], wantLines[4], wantLines[5], "stop api and web on hel1"}
	if !slices.Equal(p.Lines, want) || p.Restore {
		t.Fatalf("%q %v", p.Lines, p.Restore)
	}
}

func TestPlanMoveCustomDomain(t *testing.T) {
	st, a := seedMove(t, false)
	st.DB.Exec(`INSERT INTO domains (id, service_id, host, created_at, updated_at) VALUES ('dom_1', 'svc_api', 'api.acme.uz', 1, 1)`)
	p, _ := a.PlanMove(context.Background(), "acme", "staging", "hk1")
	if last := p.Lines[len(p.Lines)-1]; last != "api.acme.uz: point its A record to 5.6.7.8; until then it does not answer" {
		t.Fatal(last)
	}
}

func TestPlanMoveRefusals(t *testing.T) {
	cases := []struct {
		name, to, want string
		prep           func(st *store.Store)
	}{
		{"unknown", "hk2", "There is no server named hk2.", nil},
		{"same", "hel1", "staging already runs on hel1.", nil},
		{"offline target", "hk1", "hk1 is offline; a move needs it online.", func(st *store.Store) { st.DB.Exec(`UPDATE servers SET offline = 1 WHERE id = 'srv_2'`) }},
		{"never connected", "hk1", "hk1 has never connected; start its agent first.", func(st *store.Store) { st.DB.Exec(`UPDATE servers SET last_heartbeat = NULL WHERE id = 'srv_2'`) }},
		{"moving", "hk1", "staging is already moving to hk1.", func(st *store.Store) {
			st.DB.Exec(`INSERT INTO env_moves (id, environment_id, from_server_id, to_server_id, step, state, created_at, updated_at) VALUES ('mv_1', 'env_s', 'srv_1', 'srv_2', 'deploy', 'running', 1, 1)`)
		}},
		{"deploying", "hk1", "staging has a deploy in progress; wait for it to finish.", func(st *store.Store) {
			st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_q', 'svc_api', 'c2', 'queued', 2, 2)`)
		}},
		{"no backup", "hk1", "hel1 is offline and staging-db has no backup to restore from.", func(st *store.Store) { st.DB.Exec(`UPDATE servers SET offline = 1 WHERE id = 'srv_1'`) }},
		{"no storage", "hk1", "Set up backup storage first: a move carries staging-db through a backup.", func(st *store.Store) { st.DB.Exec(`DELETE FROM settings WHERE key = 'storage.endpoint'`) }},
		{"outside port", "hk1", "staging-db: outside_port 5433 is already used by other-db on hk1.", func(st *store.Store) {
			st.DB.Exec(`UPDATE services SET outside_port = 5433 WHERE id = 'svc_staging-db'`)
			st.DB.Exec(`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('prj_o', 'other', 1, 1)`)
			st.DB.Exec(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_o', 'prj_o', 'production', 'srv_2', 1, 1)`)
			st.DB.Exec(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, outside_port, created_at, updated_at)
				VALUES ('svc_o', 'env_o', 'db', 'postgres', '', '', 5432, '', 0, 0, 5433, 1, 1)`)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, a := seedMove(t, true)
			if c.prep != nil {
				c.prep(st)
			}
			_, err := a.PlanMove(context.Background(), "acme", "staging", c.to)
			if err == nil || err.Error() != c.want {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
}

func TestApplyRefusesServerChange(t *testing.T) {
	st, _ := seed(t)
	st.DB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, created_at, updated_at) VALUES ('srv_2', 'hk1', '5.6.7.8', '', '', '', 1, 1)`)
	_, err := New(st).Apply(context.Background(), parse(t, "project: acme\nenvironment: production\nserver: hk1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n"), "owner", true)
	if err == nil || err.Error() != "environment production lives on hel1; move it from the dashboard first" {
		t.Fatal(err)
	}
}

func fullBoxes(t *testing.T, a *App) map[string]Reseal {
	p, err := a.PlanMove(context.Background(), "acme", "staging", "hk1")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Reseal{}
	for _, v := range p.Vars {
		out[v.ID] = Reseal{AgentBox: "new-" + v.ID, UpdatedAt: v.UpdatedAt}
	}
	return out
}

func TestStartMove(t *testing.T) {
	for _, c := range []struct {
		db   bool
		step string
	}{{true, "stop"}, {false, "switch_server"}} {
		st, a := seedMove(t, c.db)
		id, err := a.StartMove(context.Background(), "acme", "staging", MoveRequest{To: "hk1", Boxes: fullBoxes(t, a), RecoveryBox: "rk"}, "owner")
		if err != nil {
			t.Fatal(err)
		}
		m, _ := st.Q.GetMove(context.Background(), id)
		var box string
		st.DB.QueryRow(`SELECT agent_box FROM env_vars WHERE id = 'var_a'`).Scan(&box)
		var ev int
		st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'env.move.started' AND subject_id = ?`, id).Scan(&ev)
		if m.State != "running" || m.Step != c.step || !strings.Contains(m.BoxesJson, `"var_a":"new-var_a"`) || box != "a2" || ev != 1 || m.ToServerID != "srv_2" {
			t.Fatalf("db=%v %+v box=%s ev=%d", c.db, m, box, ev)
		}
		if k, ok := a.TakeMoveKey(id); !ok || k != "rk" {
			t.Fatal("recovery box not kept")
		}
	}
}

func TestStartMoveRefusesStaleBoxes(t *testing.T) {
	st, a := seedMove(t, true)
	boxes := fullBoxes(t, a)
	st.DB.Exec(`UPDATE env_vars SET updated_at = 99 WHERE id = 'var_a'`)
	_, err := a.StartMove(context.Background(), "acme", "staging", MoveRequest{To: "hk1", Boxes: boxes, RecoveryBox: "rk"}, "owner")
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM env_moves`).Scan(&n)
	if err == nil || err.Error() != "The re-sealed values do not match staging's keys now; reload the page and try again." || n != 0 {
		t.Fatal(err, n)
	}
}

func TestStartMoveRefusesMissingBox(t *testing.T) {
	_, a := seedMove(t, true)
	want := "The re-sealed values do not match staging's keys now; reload the page and try again."
	missing := fullBoxes(t, a)
	delete(missing, "var_s")
	extra := fullBoxes(t, a)
	extra["var_zzz"] = Reseal{AgentBox: "x"}
	empty := fullBoxes(t, a)
	empty["var_s"] = Reseal{UpdatedAt: 11}
	for _, b := range []map[string]Reseal{missing, extra, empty} {
		if _, err := a.StartMove(context.Background(), "acme", "staging", MoveRequest{To: "hk1", Boxes: b, RecoveryBox: "rk"}, "owner"); err == nil || err.Error() != want {
			t.Fatal(err)
		}
	}
}

func TestStartMoveNeedsRecoveryBox(t *testing.T) {
	_, a := seedMove(t, true)
	_, err := a.StartMove(context.Background(), "acme", "staging", MoveRequest{To: "hk1", Boxes: fullBoxes(t, a)}, "owner")
	if err == nil || err.Error() != "The recovery key is needed to restore staging-db on hk1." {
		t.Fatal(err)
	}
}

func TestStartMoveOldServerOffline(t *testing.T) {
	st, a := seedMove(t, true)
	st.DB.Exec(`UPDATE servers SET offline = 1 WHERE id = 'srv_1'`)
	st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, finished_at, created_at, updated_at) VALUES ('bk_1', 'svc_staging-db', 'k', 'ok', 1, 1, 1)`)
	id, err := a.StartMove(context.Background(), "acme", "staging", MoveRequest{To: "hk1", Boxes: fullBoxes(t, a), RecoveryBox: "rk"}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := st.Q.GetMove(context.Background(), id); m.Step != "switch_server" {
		t.Fatal(m.Step)
	}
}

func TestRetryMove(t *testing.T) {
	st, a := seedMove(t, true)
	ctx := context.Background()
	st.DB.Exec(`INSERT INTO env_moves (id, environment_id, from_server_id, to_server_id, step, state, cause, created_at, updated_at) VALUES ('mv_f', 'env_s', 'srv_1', 'srv_2', 'restore', 'failed', 'boom', 1, 1)`)
	if err := a.RetryMove(ctx, "mv_f", "rk2", "owner"); err != nil {
		t.Fatal(err)
	}
	m, _ := st.Q.GetMove(ctx, "mv_f")
	if k, _ := a.TakeMoveKey("mv_f"); m.State != "running" || m.Step != "restore" || m.Cause != "" || k != "rk2" {
		t.Fatalf("%+v %q", m, k)
	}
	if err := a.RetryMove(ctx, "mv_f", "", "owner"); err == nil || err.Error() != "This move is not stopped." {
		t.Fatal(err)
	}
	st.DB.Exec(`INSERT INTO env_moves (id, environment_id, from_server_id, to_server_id, step, state, cause, created_at, updated_at) VALUES ('mv_b', 'env_s', 'srv_1', 'srv_2', 'backup', 'failed', 'boom', 2, 2)`)
	if err := a.RetryMove(ctx, "mv_b", "", "owner"); err == nil || err.Error() != "This move was undone; start a new one." {
		t.Fatal(err)
	}
}

func TestLatestMoveView(t *testing.T) {
	_, a := seedMove(t, false)
	if _, err := a.StartMove(context.Background(), "acme", "staging", MoveRequest{To: "hk1", Boxes: fullBoxes(t, a)}, "owner"); err != nil {
		t.Fatal(err)
	}
	v, err := a.LatestMove(context.Background(), "acme", "staging")
	if err != nil || v.From != "hel1" || v.To != "hk1" || v.State != "running" || len(v.Lines) != 4 {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestRetryKeepsRecordedContainers(t *testing.T) {
	st, a := seedMove(t, true)
	st.DB.Exec(`INSERT INTO env_moves (id, environment_id, from_server_id, to_server_id, step, state, cause, old_json, backups_json, created_at, updated_at)
		VALUES ('mv_f', 'env_s', 'srv_1', 'srv_2', 'restore', 'failed', 'boom', '{"svc_api":"acme-staging-api-1"}', '{"svc_staging-db":"bk_1"}', 1, 1)`)
	if err := a.RetryMove(context.Background(), "mv_f", "", "owner"); err != nil {
		t.Fatal(err)
	}
	m, _ := st.Q.GetMove(context.Background(), "mv_f")
	if m.OldJson != `{"svc_api":"acme-staging-api-1"}` || m.BackupsJson != `{"svc_staging-db":"bk_1"}` || m.FinishedAt.Valid {
		t.Fatalf("%+v", m)
	}
}

func TestPlanMoveVolume(t *testing.T) {
	st, a := seedMove(t, false)
	st.DB.Exec(`UPDATE services SET volumes_json = '[{"Name":"uploads","Path":"/u"}]' WHERE id = 'svc_api'`)
	p, err := a.PlanMove(context.Background(), "acme", "staging", "hk1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{wantLines[0], wantLines[1], "back up api volume uploads on hel1", "restore api volume uploads on hk1 from that backup",
		wantLines[4], wantLines[5], "stop api and web on hel1", "remove api volume uploads from hel1"}
	if !slices.Equal(p.Lines, want) || !p.Restore {
		t.Fatalf("%q %v", p.Lines, p.Restore)
	}
	if _, err := a.StartMove(context.Background(), "acme", "staging", MoveRequest{To: "hk1", Boxes: fullBoxes(t, a)}, "owner"); err == nil ||
		err.Error() != "The recovery key is needed to restore api volume uploads on hk1." {
		t.Fatal(err)
	}
	id, err := a.StartMove(context.Background(), "acme", "staging", MoveRequest{To: "hk1", Boxes: fullBoxes(t, a), RecoveryBox: "rk"}, "owner")
	if m, _ := st.Q.GetMove(context.Background(), id); err != nil || m.Step != "stop" {
		t.Fatal(err, m.Step)
	}
}

func TestPlanMoveDetachedVolume(t *testing.T) {
	st, a := seedMove(t, false)
	st.DB.Exec(`UPDATE services SET volumes_json = '[]', volumes_kept = '["old"]' WHERE id = 'svc_api'`)
	p, err := a.PlanMove(context.Background(), "acme", "staging", "hk1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{wantLines[0], wantLines[1], "back up api volume old on hel1", "restore api volume old on hk1 from that backup",
		wantLines[4], wantLines[5], "stop api and web on hel1", "remove api volume old from hel1"}
	if !slices.Equal(p.Lines, want) || !p.Restore {
		t.Fatalf("%q %v", p.Lines, p.Restore)
	}
}

func TestPlanMoveDetachedVolumeOldServerOffline(t *testing.T) {
	st, a := seedMove(t, false)
	st.DB.Exec(`UPDATE services SET volumes_json = '[]', volumes_kept = '["old"]' WHERE id = 'svc_api'`)
	st.DB.Exec(`UPDATE servers SET offline = 1 WHERE id = 'srv_1'`)
	p, err := a.PlanMove(context.Background(), "acme", "staging", "hk1")
	if err != nil || p.Restore || strings.Contains(strings.Join(p.Lines, "\n"), "volume old") {
		t.Fatal(err, p.Lines)
	}
}

func TestPlanMoveVolumeOldServerOffline(t *testing.T) {
	st, a := seedMove(t, false)
	st.DB.Exec(`UPDATE services SET volumes_json = '[{"Name":"uploads","Path":"/u"}]' WHERE id = 'svc_api'`)
	st.DB.Exec(`UPDATE servers SET offline = 1 WHERE id = 'srv_1'`)
	if _, err := a.PlanMove(context.Background(), "acme", "staging", "hk1"); err == nil || err.Error() != "hel1 is offline and api volume uploads has no backup to restore from." {
		t.Fatal(err)
	}
	at := moveNow.Add(-3 * time.Hour).UnixMilli()
	st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, volume, state, finished_at, created_at, updated_at) VALUES ('bk_v', 'svc_api', 'k', 'uploads', 'ok', ?, ?, ?)`, at, at, at)
	p, err := a.PlanMove(context.Background(), "acme", "staging", "hk1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.Lines, "hel1 is offline: api volume uploads is restored from its last backup, 3 hours old; changes after it are lost") ||
		!slices.Contains(p.Lines, "keep api volume uploads on hel1: it may hold changes newer than that backup") {
		t.Fatalf("%q", p.Lines)
	}
}

func TestPlanMoveNamesReferencingApps(t *testing.T) {
	st, a := seedMove(t, true)
	for _, q := range []string{
		`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, current_deploy_id, created_at, updated_at)
			VALUES ('svc_pa', 'env_1', 'api-p', 'web', 'https://x/y', 'main', 10000, '/', 60000, 30000, 'dep_pa', 1, 1)`,
		`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, ref, created_at, updated_at) VALUES ('var_x', 'env_1', 'svc_pa', 'DATABASE_URL', '', '', 'staging/staging-db.url', 1, 1)`,
	} {
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	p, err := a.PlanMove(context.Background(), "acme", "staging", "hk1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.Lines, "redeploy production/api-p, which uses staging-db over the private network") {
		t.Fatalf("%q", p.Lines)
	}
}

func TestPlanMoveSharedVars(t *testing.T) {
	st, a := seedMove(t, true)
	st.DB.Exec(`INSERT INTO shared_vars (id, scope, server_id, key, agent_box, recovery_box, created_at, updated_at) VALUES
		('sh1', 'team', 'srv_1', 'SENTRY_DSN', 'b', 'r', 1, 1), ('sh2', 'server', 'srv_1', 'SMTP_HOST', 'b', 'r', 1, 1),
		('sh3', 'team', 'srv_1', 'BOTH', 'b', 'r', 1, 1), ('sh4', 'team', 'srv_2', 'BOTH', 'b', 'r', 1, 1)`)
	p, err := a.PlanMove(context.Background(), "acme", "staging", "hk1")
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(p.Lines, "\n")
	for _, want := range []string{
		"SENTRY_DSN, a team variable, is not set on hk1: set it again in Shared variables, or the apps start without it",
		"SMTP_HOST is hel1's own variable: the apps on hk1 do not get it",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in\n%s", want, all)
		}
	}
	if strings.Contains(all, "BOTH") {
		t.Errorf("a team variable on both servers was named:\n%s", all)
	}
}

func TestMoveCarriesSecretSources(t *testing.T) {
	st, a := seedMove(t, false)
	st.DB.Exec(`INSERT INTO secret_sources (id, environment_id, provider, agent_box, recovery_box, created_at, updated_at) VALUES ('src_1', 'env_s', 'doppler', 'old', 'rb', 1, 5)`)
	p, err := a.PlanMove(context.Background(), "acme", "staging", "hk1")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range p.Vars {
		found = found || (v.ID == "src_1" && v.RecoveryBox == "rb" && v.Key == "doppler")
	}
	if !found {
		t.Fatalf("%+v", p.Vars)
	}
	boxes := fullBoxes(t, a)
	delete(boxes, "src_1")
	if _, err := a.StartMove(context.Background(), "acme", "staging", MoveRequest{To: "hk1", Boxes: boxes, RecoveryBox: "rk"}, "owner"); err == nil {
		t.Fatal("started without the source's box")
	}
	id, err := a.StartMove(context.Background(), "acme", "staging", MoveRequest{To: "hk1", Boxes: fullBoxes(t, a), RecoveryBox: "rk"}, "owner")
	if m, _ := st.Q.GetMove(context.Background(), id); err != nil || !strings.Contains(m.BoxesJson, `"src_1":"new-src_1"`) {
		t.Fatalf("%v %s", err, m.BoxesJson)
	}
}
