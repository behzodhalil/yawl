package watch

import (
	"context"
	"encoding/json"
	"github.com/behzodhalil/yawl/internal/cron"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

func seedWorld(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, q := range []string{
		`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, created_at, updated_at) VALUES ('srv_1','hel1','1.2.3.4','','',1,1), ('srv_2','hk1','5.6.7.8','','',1,1)`,
		`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('prj_1','acme',1,1)`,
		`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_1','prj_1','production','srv_1',1,1)`,
		`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, public, created_at, updated_at) VALUES
		 ('svc_api','env_1','api','web','r','main',10000,'/health',1,1,1,1,1),
		 ('svc_wrk','env_1','jobs','worker','r','main',0,'/',1,1,0,1,1),
		 ('svc_new','env_1','new','web','r','main',10000,'/',1,1,0,1,1),
		 ('svc_db','env_1','db','postgres','','',5432,'',1,1,0,1,1)`,
		`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, public, schedule, timezone, timeout_ms, created_at, updated_at) VALUES
		 ('svc_cron','env_1','report','cron','r','main',0,'',0,0,0,'0 3 * * *','',3600000,1,1)`,
		`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_c','svc_cron','c','live',1,1)`,
		`UPDATE services SET current_deploy_id = 'dep_c' WHERE id = 'svc_cron'`,
		`INSERT INTO deploys (id, service_id, commit_sha, state, created_at, updated_at) VALUES ('dep_1','svc_api','c','live',1,1), ('dep_2','svc_wrk','c','live',1,1), ('dep_3','svc_new','c','building',1,1)`,
		`UPDATE services SET current_deploy_id = 'dep_1' WHERE id = 'svc_api'`,
		`UPDATE services SET current_deploy_id = 'dep_2' WHERE id = 'svc_wrk'`,
		`UPDATE services SET current_deploy_id = 'dep_3' WHERE id = 'svc_new'`,
		`INSERT INTO domains (id, service_id, host, dns_state, created_at, updated_at) VALUES ('dom_1','svc_api','api.acme.uz','ok',1,1), ('dom_2','svc_api','pending.acme.uz','pending',1,1)`,
		`INSERT INTO heartbeats (id, name, period_s, grace_s, token, created_at, updated_at) VALUES ('hb_1','laptop backup',86400,3600,'tok1',1,1)`,
		`INSERT INTO settings (key, value) VALUES ('panel.server_id','srv_1'), ('watch.deployed','fp'), ('watch.url','https://w'), ('watch.agent.srv_1','fp|https://w'), ('watch.agent.srv_2','old|https://w')`,
	} {
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return st
}

func TestBuild(t *testing.T) {
	st := seedWorld(t)
	got, err := Build(context.Background(), st.Q)
	if err != nil {
		t.Fatal(err)
	}
	want := []Expectation{
		{ID: "backup:svc_db", Kind: "backup", Subject: "backup:svc_db", Label: "acme/production/db backups", Server: "srv_1", Project: "acme"},
		{ID: "cron:svc_cron", Kind: "cron", Subject: "cron:svc_cron", Label: "acme/production/report", Target: cron.PanelZone() + " 0 3 * * *", Server: "srv_1", Project: "acme", PeriodS: 3600},
		{ID: "hb:hb_1", Kind: "heartbeat", Subject: "hb:hb_1", Label: "laptop backup", Target: "tok1", PeriodS: 86400, GraceS: 3600},
		{ID: "panel", Kind: "panel", Subject: "panel", Label: "The panel", Server: "srv_1"},
		{ID: "server:srv_1", Kind: "server", Subject: "server:srv_1", Label: "hel1", Server: "srv_1"},
		{ID: "url:svc_api:api-production.1-2-3-4.sslip.io", Kind: "url", Subject: "service:svc_api", Label: "acme/production/api", Target: "https://api-production.1-2-3-4.sslip.io/health", Server: "srv_1", Project: "acme"},
		{ID: "url:svc_api:api.acme.uz", Kind: "url", Subject: "service:svc_api", Label: "acme/production/api", Target: "https://api.acme.uz/health", Server: "srv_1", Project: "acme"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%+v\nwant\n%+v", got, want)
	}
	st.DB.Exec(`DELETE FROM domains; UPDATE services SET current_deploy_id = NULL WHERE id = 'svc_api'`)
	got, _ = Build(context.Background(), st.Q)
	for _, e := range got {
		if e.Kind == "url" {
			t.Fatalf("still there: %+v", e)
		}
	}
}

func TestAgentMarker(t *testing.T) {
	if AgentMarker("fp", "https://w") != "fp|https://w" {
		t.Fatal(AgentMarker("fp", "https://w"))
	}
}

func TestChats(t *testing.T) {
	st := seedWorld(t)
	ctx := context.Background()
	v, err := vault.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	list, _ := Build(ctx, st.Q)
	got := func() string {
		cs, err := Chats(ctx, st.Q, v, list)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(cs)
		return string(b)
	}
	set := func(k, val string) { st.Q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: val}) }
	set("telegram.relay_routes", `{"-100":{"servers":["srv_2"]}}`)
	if g := got(); g != "{}" {
		t.Fatalf("own bot: %s", g)
	}
	vault.SetSecret(ctx, st.Q, v, "telegram.relay_key", []byte("rk"))
	set("telegram.relay_chats", `[{"id":"7"},{"id":"-100"},{"id":"-200"}]`)
	set("telegram.relay_routes", `{}`)
	if g := got(); g != "{}" {
		t.Fatalf("no routes: %s", g)
	}
	set("telegram.relay_routes", `{"-100":{"servers":["srv_2"]},"-200":{"projects":["acme"]}}`)
	want := `{"backup:svc_db":["7","-200"],"cron:svc_cron":["7","-200"],"server:srv_1":["7"],"service:svc_api":["7","-200"]}`
	if g := got(); g != want {
		t.Fatalf("routed:\n%s\nwant\n%s", g, want)
	}
	set("telegram.relay_routes", `{"7":{"servers":["srv_2"]},"-100":{"servers":["srv_2"]},"-200":{"projects":["acme"]}}`)
	if g := got(); !strings.Contains(g, `"server:srv_1":[]`) || strings.Contains(g, `"panel"`) || strings.Contains(g, `"hb:`) {
		t.Fatalf("none: %s", g)
	}
}
