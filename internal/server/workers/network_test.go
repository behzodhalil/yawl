package workers

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type netSender struct {
	sent map[string]proto.NetworkArgs
	seen map[string]map[string]int64
}

func (n *netSender) Send(_ context.Context, server string, c proto.Command) (proto.Result, error) {
	var a proto.NetworkArgs
	json.Unmarshal(c.Args, &a)
	n.sent[server] = a
	d := proto.NetworkData{Handshakes: map[string]int64{}}
	for _, p := range a.Peers {
		d.Handshakes[p.PublicKey] = n.seen[server][p.PublicKey]
	}
	b, _ := json.Marshal(d)
	return proto.Result{Key: c.Key, OK: true, Data: b}, nil
}

func netWorld(t *testing.T) (*store.Store, *Network, *netSender, *time.Time) {
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.DB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, wg_pubkey, private_ip, created_at, updated_at) VALUES
		('srv_1','hel1','1.1.1.1','A','','','K1','10.73.0.1',1,1), ('srv_2','fsn1','2.2.2.2','A','','','K2','10.73.0.2',1,1),
		('srv_3','nbg1','3.3.3.3','A','','','K3','10.73.0.3',1,1), ('srv_4','old1','4.4.4.4','A','','','','10.73.0.4',1,1)`)
	now := time.UnixMilli(1_800_000_000_000)
	gw := &netSender{sent: map[string]proto.NetworkArgs{}, seen: map[string]map[string]int64{}}
	return st, &Network{St: st, GW: gw, Now: func() time.Time { return now }}, gw, &now
}

func TestNetworkSendsMesh(t *testing.T) {
	_, w, gw, _ := netWorld(t)
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	a := gw.sent["srv_1"]
	if a.Address != "10.73.0.1" || a.ListenPort != 51820 || len(a.Peers) != 2 ||
		a.Peers[0] != (proto.Peer{PublicKey: "K2", Endpoint: "2.2.2.2:51820", AllowedIP: "10.73.0.2/32"}) {
		t.Fatalf("%+v", a)
	}
	if _, ok := gw.sent["srv_4"]; ok {
		t.Fatal("a server without a key was sent the mesh")
	}
}

func TestNetworkDropsGoneServer(t *testing.T) {
	st, w, gw, _ := netWorld(t)
	w.Tick(context.Background())
	st.DB.Exec(`UPDATE servers SET wg_pubkey = 'K3new' WHERE id = 'srv_3'`)
	st.DB.Exec(`DELETE FROM server_peers WHERE peer_id = 'srv_2' OR server_id = 'srv_2'`)
	st.DB.Exec(`DELETE FROM servers WHERE id = 'srv_2'`)
	w.Tick(context.Background())
	var keys []string
	for _, p := range gw.sent["srv_1"].Peers {
		keys = append(keys, p.PublicKey)
	}
	if strings.Join(keys, ",") != "K3new" {
		t.Fatalf("%v", keys)
	}
}

func TestNetworkIncidentOpensAndCloses(t *testing.T) {
	st, w, gw, now := netWorld(t)
	st.DB.Exec(`DELETE FROM servers WHERE id = 'srv_4'`)
	st.DB.Exec(`UPDATE servers SET wg_pubkey = '', private_ip = '' WHERE id = 'srv_3'`)
	ctx := context.Background()
	w.Tick(ctx)
	*now = now.Add(4 * time.Minute)
	w.Tick(ctx)
	if got := outbox(t, st); len(got) != 0 {
		t.Fatalf("too early: %q", got)
	}
	*now = now.Add(2 * time.Minute)
	w.Tick(ctx)
	w.Tick(ctx)
	want := "fsn1 cannot reach hel1 on the private network (no handshake for 6 min). Open UDP 51820 between them."
	if got := keyed(t, st); len(got) != 1 || got[0] != "network:srv_1:srv_2|open|0|fsn1 and hel1|"+want {
		t.Fatalf("%q", got)
	}
	gw.seen["srv_2"] = map[string]int64{"K1": now.UnixMilli()}
	*now = now.Add(time.Minute)
	w.Tick(ctx)
	if got := keyed(t, st); len(got) != 2 || got[1] != "network:srv_1:srv_2|close|0|fsn1 and hel1|fsn1 reaches hel1 on the private network again." {
		t.Fatalf("%q", got)
	}
}

func TestNetworkSilentOfflinePairQuiet(t *testing.T) {
	st, w, _, now := netWorld(t)
	st.DB.Exec(`UPDATE servers SET offline = 1 WHERE id = 'srv_2'`)
	ctx := context.Background()
	w.Tick(ctx)
	*now = now.Add(10 * time.Minute)
	w.Tick(ctx)
	for _, b := range outbox(t, st) {
		if strings.Contains(b, "fsn1") {
			t.Fatalf("%q", b)
		}
	}
}

func crossWorld(t *testing.T, server string) *store.Store {
	st, _ := seed(t, "web")
	addDB(t, st)
	st.DB.Exec(`UPDATE servers SET wg_pubkey = 'K1', private_ip = '10.73.0.1' WHERE id = 'srv_1'`)
	st.DB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, wg_pubkey, private_ip, created_at, updated_at) VALUES ('srv_2','fsn1','5.6.7.8','P2','','','K2','10.73.0.2',1,1)`)
	st.DB.Exec(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_2','prj_1','staging',?,1,1)`, server)
	st.DB.Exec(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, created_at, updated_at)
		VALUES ('svc_s','env_2','api','web','https://x/y','main',10000,'/',60000,30000,1,1)`)
	st.DB.Exec(`INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, ref, created_at, updated_at) VALUES
		('v_u','env_1','svc_db','URL','own-url-box','r','',1,1), ('v_p','env_1','svc_db','PASSWORD','pw-box','r','',1,1),
		('v_r','env_2','svc_s','DATABASE_URL','','','production/acme-db.url',1,1)`)
	return st
}

func startArgs(t *testing.T, gw *fakeSender) proto.StartArgs {
	for _, c := range gw.sent {
		if c.Name == proto.CmdStart {
			var a proto.StartArgs
			json.Unmarshal(c.Args, &a)
			return a
		}
	}
	t.Fatalf("no Start in %v", gw.names())
	return proto.StartArgs{}
}

func TestEnsureRecordsShares(t *testing.T) {
	st := crossWorld(t, "srv_2")
	st.DB.Exec(`UPDATE services SET private_port = 15432 WHERE id = 'svc_db'`)
	gw := &fakeSender{}
	deployOnce(t, st, "svc_db", gw)
	var a proto.EnsureDatabaseArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	if a.PrivateIP != "10.73.0.1" || a.PrivatePort != 15432 || len(a.ShareWith) != 1 || a.ShareWith["srv_2"] != "P2" {
		t.Fatalf("%+v", a)
	}
	var box string
	st.DB.QueryRow(`SELECT url_box FROM db_shares WHERE service_id = 'svc_db' AND server_id = 'srv_2'`).Scan(&box)
	if box != "share-srv_2" {
		t.Fatalf("share %q", box)
	}
}

func TestCrossEnvRefOtherServer(t *testing.T) {
	st := crossWorld(t, "srv_2")
	st.DB.Exec(`INSERT INTO db_shares (service_id, server_id, url_box, updated_at) VALUES ('svc_db','srv_2','share-box',1)`)
	gw := &fakeSender{}
	deployOnce(t, st, "svc_s", gw)
	a := startArgs(t, gw)
	if len(a.Vars) != 1 || a.Vars[0] != (proto.SealedVar{Key: "DATABASE_URL", Box: "share-box"}) || len(a.Networks) != 0 {
		t.Fatalf("%+v", a)
	}
}

func TestCrossEnvRefSameServer(t *testing.T) {
	st := crossWorld(t, "srv_1")
	id, _ := deployOnce(t, st, "svc_s", &fakeSender{})
	if d, _ := st.Q.GetDeploy(context.Background(), id); d.Cause != "acme-db has not shared its address with hel1 yet; deploy acme-db first." {
		t.Fatalf("without its share: %q", d.Cause)
	}
	st.DB.Exec(`INSERT INTO db_shares (service_id, server_id, url_box, updated_at) VALUES ('svc_db','srv_1','here-box',1)`)
	gw := &fakeSender{}
	deployOnce(t, st, "svc_s", gw)
	a := startArgs(t, gw)
	if len(a.Vars) != 1 || a.Vars[0].Box != "here-box" || len(a.Networks) != 1 || a.Networks[0] != "yawl-acme-production" {
		t.Fatalf("%+v", a)
	}
}

func TestEnsureSharesHere(t *testing.T) {
	st := crossWorld(t, "srv_1")
	gw := &fakeSender{}
	deployOnce(t, st, "svc_db", gw)
	var a proto.EnsureDatabaseArgs
	json.Unmarshal(gw.sent[0].Args, &a)
	var box string
	st.DB.QueryRow(`SELECT url_box FROM db_shares WHERE service_id = 'svc_db' AND server_id = 'srv_1'`).Scan(&box)
	if !a.ShareHere || len(a.ShareWith) != 0 || box != "here-box" {
		t.Fatalf("%+v share %q", a, box)
	}
}

func TestCrossEnvRefNoShareYet(t *testing.T) {
	st := crossWorld(t, "srv_2")
	id, _ := deployOnce(t, st, "svc_s", &fakeSender{})
	if d, _ := st.Q.GetDeploy(context.Background(), id); d.State != "failed" || d.Cause != "acme-db has not shared its address with fsn1 yet; deploy acme-db first." {
		t.Fatalf("%s %q", d.State, d.Cause)
	}
}

func moveRowAt(st *store.Store, env, from, to, step string) {
	st.DB.Exec(`INSERT INTO env_moves (id, environment_id, from_server_id, to_server_id, step, state, boxes_json, lines_json, created_at, updated_at) VALUES ('mv_1', ?, ?, ?, ?, 'running', '{}', '[]', 100, 100)`, env, from, to, step)
}

func TestMoveRedeploysReferencingApps(t *testing.T) {
	st := crossWorld(t, "srv_2")
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, image, container, state, created_at, updated_at) VALUES ('dep_s','svc_s','c1','yawl/s:dep_s','c-s','live',1,1)`)
	st.DB.Exec(`UPDATE services SET current_deploy_id = 'dep_s' WHERE id = 'svc_s'`)
	st.DB.Exec(`UPDATE environments SET server_id = 'srv_2' WHERE id = 'env_1'`)
	moveRowAt(st, "env_1", "srv_1", "srv_2", "deploy")
	w := &Move{St: st, App: app.New(st), GW: &fakeSender{}, Now: func() time.Time { return time.UnixMilli(200) }}
	w.Tick(context.Background())
	var image string
	var rollback int
	st.DB.QueryRow(`SELECT image, rollback FROM deploys WHERE service_id = 'svc_s' AND state = 'queued'`).Scan(&image, &rollback)
	if image != "yawl/s:dep_s" || rollback != 1 {
		t.Fatalf("%q %d", image, rollback)
	}
}

func TestMoveWaitsForShare(t *testing.T) {
	st := crossWorld(t, "srv_2")
	st.DB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, wg_pubkey, private_ip, created_at, updated_at) VALUES ('srv_3','nbg1','9.9.9.9','P3','','','K3','10.73.0.3',1,1)`)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, image, container, state, created_at, updated_at) VALUES ('dep_s','svc_s','c1','yawl/s:dep_s','c-s','live',1,1)`)
	st.DB.Exec(`UPDATE services SET current_deploy_id = 'dep_s' WHERE id = 'svc_s'`)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, image, state, created_at, updated_at) VALUES ('dep_db','svc_db','','','live',1,1)`)
	st.DB.Exec(`UPDATE services SET current_deploy_id = 'dep_db' WHERE id = 'svc_db'`)
	moveRowAt(st, "env_2", "srv_2", "srv_3", "switch_server")
	w := &Move{St: st, App: app.New(st), GW: &fakeSender{}, Now: func() time.Time { return time.UnixMilli(200) }}
	ctx := context.Background()
	w.Tick(ctx)
	var queued, port int
	st.DB.QueryRow(`SELECT count(*) FROM deploys WHERE service_id = 'svc_db' AND state = 'queued'`).Scan(&queued)
	st.DB.QueryRow(`SELECT private_port FROM services WHERE id = 'svc_db'`).Scan(&port)
	if step, _, _ := moveRow(t, st); step != "deploy" || queued != 1 || port != 15432 || len(moveDeploys(t, st)) != 0 {
		t.Fatalf("step %s, ensure queued %d, port %d, deploys %v", step, queued, port, moveDeploys(t, st))
	}
	st.DB.Exec(`INSERT INTO db_shares (service_id, server_id, url_box, updated_at) VALUES ('svc_db','srv_3','b',1)`)
	w.Tick(ctx)
	if len(moveDeploys(t, st)) != 1 {
		t.Fatal("no deploy once shared")
	}
}

func TestMovedDatabaseResharesItself(t *testing.T) {
	st := crossWorld(t, "srv_2")
	st.DB.Exec(`UPDATE services SET private_port = 15432 WHERE id = 'svc_db'`)
	st.DB.Exec(`INSERT INTO db_shares (service_id, server_id, url_box, updated_at) VALUES ('svc_db','srv_2','old',1)`)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, image, state, created_at, updated_at) VALUES ('dep_db','svc_db','','','live',1,1)`)
	st.DB.Exec(`UPDATE services SET current_deploy_id = 'dep_db' WHERE id = 'svc_db'`)
	st.DB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, wg_pubkey, private_ip, created_at, updated_at) VALUES ('srv_3','nbg1','9.9.9.9','P3','','','K3','10.73.0.3',1,1)`)
	if _, err := st.DB.Exec(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, private_port, created_at, updated_at)
		VALUES ('svc_x', 'env_2', 'x-db', 'postgres', '', '', 0, '', 0, 0, 15432, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	st.DB.Exec(`UPDATE environments SET server_id = 'srv_3' WHERE id = 'env_2'`)
	moveRowAt(st, "env_1", "srv_1", "srv_3", "switch_server")
	(&Move{St: st, App: app.New(st), GW: &fakeSender{}, Now: func() time.Time { return time.UnixMilli(200) }}).Tick(context.Background())
	var port, shares int
	st.DB.QueryRow(`SELECT private_port FROM services WHERE id = 'svc_db'`).Scan(&port)
	st.DB.QueryRow(`SELECT count(*) FROM db_shares WHERE service_id = 'svc_db'`).Scan(&shares)
	if port != 15433 || shares != 0 {
		t.Fatalf("port %d, shares %d", port, shares)
	}
}

func TestPurgeSharedDatabase(t *testing.T) {
	st := crossWorld(t, "srv_2")
	st.DB.Exec(`INSERT INTO db_shares (service_id, server_id, url_box, updated_at) VALUES ('svc_db','srv_2','b',1)`)
	now := tDel
	a := app.New(st)
	a.Now = func() time.Time { return now }
	w := &Trash{St: st, GW: &fakeSender{}, App: a, Now: func() time.Time { return now }}
	if err := a.Delete(context.Background(), "service", "svc_db", "owner"); err != nil {
		t.Fatal(err)
	}
	w.Tick(context.Background())
	now = tDel.Add(49 * time.Hour)
	w.Tick(context.Background())
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM services WHERE id = 'svc_db'`).Scan(&n)
	if n != 0 {
		t.Fatal("the shared database was never purged")
	}
}

func TestMoveShareFailureFailsMove(t *testing.T) {
	st := crossWorld(t, "srv_2")
	st.DB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, wg_pubkey, private_ip, created_at, updated_at) VALUES ('srv_3','nbg1','9.9.9.9','P3','','','K3','10.73.0.3',1,1)`)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, image, container, state, created_at, updated_at) VALUES ('dep_s','svc_s','c1','yawl/s:dep_s','c-s','live',1,1)`)
	st.DB.Exec(`UPDATE services SET current_deploy_id = 'dep_s' WHERE id = 'svc_s'`)
	moveRowAt(st, "env_2", "srv_2", "srv_3", "switch_server")
	w := &Move{St: st, App: app.New(st), GW: &fakeSender{}, Now: func() time.Time { return time.UnixMilli(200) }}
	ctx := context.Background()
	w.Tick(ctx)
	st.DB.Exec(`UPDATE deploys SET state = 'failed', cause = 'bind: cannot assign requested address', created_at = 250 WHERE service_id = 'svc_db' AND state = 'queued'`)
	w.Tick(ctx)
	w.Tick(ctx)
	var queued int
	st.DB.QueryRow(`SELECT count(*) FROM deploys WHERE service_id = 'svc_db' AND state = 'queued'`).Scan(&queued)
	step, state, cause := moveRow(t, st)
	if state != "failed" || step != "deploy" || cause != "acme-db could not share its address with nbg1: bind: cannot assign requested address" || queued != 0 {
		t.Fatalf("%s %s %q, queued %d", step, state, cause, queued)
	}
}

func TestMoveShareRetryAsksAgain(t *testing.T) {
	st := crossWorld(t, "srv_2")
	st.DB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, next_cert_serial, wg_pubkey, private_ip, created_at, updated_at) VALUES ('srv_3','nbg1','9.9.9.9','P3','','','K3','10.73.0.3',1,1)`)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, image, container, state, created_at, updated_at) VALUES ('dep_s','svc_s','c1','yawl/s:dep_s','c-s','live',1,1)`)
	st.DB.Exec(`UPDATE services SET current_deploy_id = 'dep_s' WHERE id = 'svc_s'`)
	st.DB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, image, state, cause, created_at, updated_at) VALUES ('dep_old','svc_db','','','failed','old',150,150)`)
	st.DB.Exec(`UPDATE environments SET server_id = 'srv_3' WHERE id = 'env_2'`)
	moveRowAt(st, "env_2", "srv_2", "srv_3", "deploy")
	st.DB.Exec(`UPDATE env_moves SET updated_at = 300 WHERE id = 'mv_1'`)
	w := &Move{St: st, App: app.New(st), GW: &fakeSender{}, Now: func() time.Time { return time.UnixMilli(400) }}
	w.Tick(context.Background())
	var queued int
	st.DB.QueryRow(`SELECT count(*) FROM deploys WHERE service_id = 'svc_db' AND state = 'queued'`).Scan(&queued)
	if _, state, cause := moveRow(t, st); state != "running" || queued != 1 {
		t.Fatalf("%s %q, queued %d", state, cause, queued)
	}
}

func TestNetworkKeepsRefusal(t *testing.T) {
	st, w, _, _ := netWorld(t)
	w.GW = refuser{}
	w.Tick(context.Background())
	if v, _ := st.Q.GetSetting(context.Background(), "network.cause.srv_1"); v != "the private network needs Linux with WireGuard" {
		t.Fatalf("%q", v)
	}
	st2, w2, _, _ := netWorld(t)
	st2.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "network.cause.srv_1", Value: "old"})
	w2.Tick(context.Background())
	if _, err := st2.Q.GetSetting(context.Background(), "network.cause.srv_1"); err == nil {
		t.Fatal("an applied network keeps the old cause")
	}
}

type refuser struct{}

func (refuser) Send(_ context.Context, _ string, c proto.Command) (proto.Result, error) {
	return proto.Result{Key: c.Key, Cause: "the private network needs Linux with WireGuard"}, nil
}

func TestNetworkClosesWhenServerLeaves(t *testing.T) {
	st, w, _, now := netWorld(t)
	st.DB.Exec(`DELETE FROM servers WHERE id = 'srv_4'`)
	st.DB.Exec(`UPDATE servers SET wg_pubkey = '', private_ip = '' WHERE id = 'srv_3'`)
	ctx := context.Background()
	w.Tick(ctx)
	*now = now.Add(6 * time.Minute)
	w.Tick(ctx)
	if got := keyed(t, st); len(got) != 1 {
		t.Fatalf("no incident: %q", got)
	}
	st.DB.Exec(`UPDATE servers SET wg_pubkey = '', private_ip = '' WHERE id = 'srv_2'`)
	*now = now.Add(time.Minute)
	w.Tick(ctx)
	got := keyed(t, st)
	if len(got) != 2 || got[1] != "network:srv_1:srv_2|close|0|fsn1 and hel1|fsn1 and hel1: one of them left the private network." {
		t.Fatalf("%q", got)
	}
	if v := setting(ctx, st.Q, "network.open.srv_1:srv_2"); v != "" {
		t.Fatal("flag kept")
	}
}

func TestNetworkReturnAfterAwayQuiet(t *testing.T) {
	st, w, gw, now := netWorld(t)
	st.DB.Exec(`DELETE FROM servers WHERE id = 'srv_4'`)
	st.DB.Exec(`UPDATE servers SET wg_pubkey = '', private_ip = '' WHERE id = 'srv_3'`)
	ctx := context.Background()
	gw.seen["srv_1"] = map[string]int64{"K2": now.UnixMilli()}
	gw.seen["srv_2"] = map[string]int64{"K1": now.UnixMilli()}
	w.Tick(ctx)
	st.DB.Exec(`UPDATE servers SET offline = 1 WHERE id = 'srv_2'`)
	for range 7 {
		*now = now.Add(time.Minute)
		w.Tick(ctx)
	}
	st.DB.Exec(`UPDATE servers SET offline = 0 WHERE id = 'srv_2'`)
	*now = now.Add(time.Minute)
	w.Tick(ctx)
	if got := keyed(t, st); len(got) != 0 {
		t.Fatalf("%q", got)
	}
}

func TestNetworkIPv6Endpoint(t *testing.T) {
	st, w, gw, _ := netWorld(t)
	st.DB.Exec(`UPDATE servers SET public_ip = '2a01:4f8::1' WHERE id = 'srv_2'`)
	w.Tick(context.Background())
	for _, p := range gw.sent["srv_1"].Peers {
		if p.PublicKey == "K2" && p.Endpoint != "[2a01:4f8::1]:51820" {
			t.Fatalf("%q", p.Endpoint)
		}
	}
}
