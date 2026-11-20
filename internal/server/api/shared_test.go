package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestAPISharedVars(t *testing.T) {
	srv, tok, a := setupApp(t)
	ctx := context.Background()
	a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "recovery.pubkey", Value: "age1x"})
	a.St.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_2", Name: "hk1", PublicIp: "5.6.7.8", CreatedAt: 1, UpdatedAt: 1})
	testDB.Exec(`UPDATE servers SET agent_pubkey = 'pk'`)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	box := base64.StdEncoding.EncodeToString([]byte("sealed"))
	keys := `,"keys":{"hel1":"pk","hk1":"pk"}`
	res, b := call(t, srv, tok, "PUT", "/api/shared-env/team/SENTRY_DSN", `{"boxes":{"hel1":"`+box+`"},"recovery_box":"`+box+`"`+keys+`}`)
	if res.StatusCode != 400 || !strings.Contains(string(b), "hk1") {
		t.Fatalf("a team variable not sealed to hk1: %d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "PUT", "/api/shared-env/team/SENTRY_DSN", `{"boxes":{"hel1":"`+box+`","hk1":"`+box+`"},"recovery_box":"`+box+`","keys":{"hel1":"old","hk1":"pk"}}`); res.StatusCode != 409 || !strings.Contains(string(b), "hel1") {
		t.Fatalf("a stale key: %d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "PUT", "/api/shared-env/team/SENTRY_DSN", `{"boxes":{"hel1":"`+box+`","hk1":"`+box+`"},"recovery_box":"`+box+`"`+keys+`}`); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "PUT", "/api/shared-env/servers/hel1/SMTP_HOST", `{"agent_box":"`+box+`","recovery_box":"`+box+`","build":true,"keys":{"hel1":"pk"}}`); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, tok, "PUT", "/api/shared-env/team/SENTRY_DSN", `{"boxes":{"hel1":"x y","hk1":"`+box+`"},"recovery_box":"`+box+`"`+keys+`}`); res.StatusCode != 400 {
		t.Fatalf("a bad box: %d", res.StatusCode)
	}
	_, b = call(t, srv, tok, "GET", "/api/shared-env", "")
	var list []struct {
		Scope, Key string
		Servers    []string
		Build      bool
	}
	json.Unmarshal(b, &list)
	if len(list) != 2 || list[0].Key != "SENTRY_DSN" || strings.Join(list[0].Servers, ",") != "hel1,hk1" || list[1].Scope != "server" || !list[1].Build {
		t.Fatalf("%s", b)
	}
	testDB.Exec(`UPDATE servers SET agent_pubkey = 'pk2' WHERE name = 'hk1'`)
	_, b = call(t, srv, tok, "GET", "/api/shared-env", "")
	if !strings.Contains(string(b), `"stale":["hk1"]`) {
		t.Fatalf("%s", b)
	}
	_, b = call(t, srv, tok, "GET", "/api/services/acme/production/api/env", "")
	if !strings.Contains(string(b), `"key":"SENTRY_DSN","scope":"team"`) || !strings.Contains(string(b), `"key":"SMTP_HOST","scope":"server"`) {
		t.Fatalf("%s", b)
	}
	dev := memberToken(t, srv, tok, "dev7@acme.uz", "developer")
	if res, _ := call(t, srv, dev, "PUT", "/api/shared-env/servers/hel1/X", `{"agent_box":"`+box+`","recovery_box":"`+box+`"}`); res.StatusCode != 403 {
		t.Fatalf("a Developer: %d", res.StatusCode)
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/shared-env/team/SENTRY_DSN", ""); res.StatusCode != 200 {
		t.Fatalf("%d", res.StatusCode)
	}
	if n, _ := a.St.Q.ListSharedVars(ctx); len(n) != 1 {
		t.Fatalf("%+v", n)
	}
}

func TestAPISharedVarsRefusals(t *testing.T) {
	srv, tok, a := setupApp(t)
	ctx := context.Background()
	a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "recovery.pubkey", Value: "age1x"})
	big := `{"recovery_box":"` + strings.Repeat("A", 64*maxBox) + `"}`
	if res, b := call(t, srv, tok, "PUT", "/api/shared-env/team/BIG", big); res.StatusCode != 413 {
		t.Fatalf("oversized: %d %s", res.StatusCode, b)
	}
	if _, err := testDB.Exec(`DELETE FROM servers`); err != nil {
		t.Fatal(err)
	}
	box := base64.StdEncoding.EncodeToString([]byte("sealed"))
	if res, b := call(t, srv, tok, "PUT", "/api/shared-env/team/SENTRY_DSN", `{"boxes":{},"recovery_box":"`+box+`"}`); res.StatusCode != 409 || !strings.Contains(string(b), "no server") {
		t.Fatalf("no servers: %d %s", res.StatusCode, b)
	}
}

func TestAPISharedVarsListedAndOpened(t *testing.T) {
	srv, tok, _ := setupApp(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	testDB.Exec(`INSERT INTO shared_vars (id, scope, server_id, key, agent_box, recovery_box, created_at, updated_at) SELECT 'shv_1', 'team', id, 'TEAM_KEY', 'b', 'rbox', 1, 1 FROM servers WHERE name = 'hel1'`)
	if _, b := call(t, srv, tok, "GET", "/api/environments/acme/production/env", ""); !strings.Contains(string(b), `"key":"TEAM_KEY","scope":"team"`) {
		t.Fatalf("list: %s", b)
	}
	if res, b := call(t, srv, tok, "GET", "/api/services/acme/production/api/env/TEAM_KEY/recovery_box", ""); res.StatusCode != 200 || !strings.Contains(string(b), "rbox") {
		t.Fatalf("recovery box: %d %s", res.StatusCode, b)
	}
}
