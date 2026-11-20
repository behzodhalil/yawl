package workers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestRunnerBuildsOnBuildServer(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_b", Name: "builder", PublicIp: "5.6.7.8", CreatedAt: 1, UpdatedAt: 1})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "build.server", Value: "builder"})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "build.repository", Value: "ghcr.io/acme/builds"})
	gw := &fakeSender{}
	id, _ := deployOnce(t, st, svc, gw)
	var b proto.BuildArgs
	json.Unmarshal(gw.sent[0].Args, &b)
	if gw.sent[0].Name != proto.CmdBuild || gw.to[0] != "srv_b" || b.Push == nil || b.Push.Ref != "ghcr.io/acme/builds:"+id {
		t.Fatalf("%s to %s %+v", gw.sent[0].Name, gw.to[0], b.Push)
	}
	if gw.sent[0].Key == id+":build" {
		t.Fatalf("key %s", gw.sent[0].Key)
	}
	var p proto.PullArgs
	json.Unmarshal(gw.sent[1].Args, &p)
	if gw.sent[1].Name != proto.CmdPull || gw.to[1] != "srv_1" || p.Ref != "ghcr.io/acme/builds:"+id || !strings.HasSuffix(p.Tag, ":"+id) {
		t.Fatalf("%s to %s %+v", gw.sent[1].Name, gw.to[1], p)
	}
	for i, to := range gw.to[2:] {
		if to != "srv_1" {
			t.Fatalf("%s went to %s", gw.sent[i+2].Name, to)
		}
	}
	d, _ := st.Q.GetDeploy(ctx, id)
	if d.State != "live" || d.Image != p.Tag {
		t.Fatalf("%s %s", d.State, d.Image)
	}
}

func TestRunnerBuildVarsBuildAtHome(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_b", Name: "builder", PublicIp: "5.6.7.8", CreatedAt: 1, UpdatedAt: 1})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "build.server", Value: "builder"})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "build.repository", Value: "ghcr.io/acme/builds"})
	st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "v1", EnvironmentID: "env_1", ServiceID: svc, Key: "NPM_TOKEN", AgentBox: "b1", RecoveryBox: "r", Build: 1})
	gw := &fakeSender{}
	id, _ := deployOnce(t, st, svc, gw)
	var b proto.BuildArgs
	json.Unmarshal(gw.sent[0].Args, &b)
	var warn string
	st.DB.QueryRow(`SELECT body_json FROM events WHERE kind = 'deploy.warning' AND subject_id = ?`, id).Scan(&warn)
	if gw.to[0] != "srv_1" || b.Push != nil || !strings.Contains(warn, "Built on hel1, not builder: NPM_TOKEN is a build variable sealed to hel1.") {
		t.Fatalf("%s %+v %s", gw.to[0], b.Push, warn)
	}
}

func TestRunnerBuildServerOffline(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_b", Name: "builder", PublicIp: "5.6.7.8", CreatedAt: 1, UpdatedAt: 1})
	st.DB.Exec(`UPDATE servers SET offline = 1 WHERE id = 'srv_b'`)
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "build.server", Value: "builder"})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "build.repository", Value: "ghcr.io/acme/builds"})
	gw := &fakeSender{}
	id, _ := deployOnce(t, st, svc, gw)
	var warn string
	st.DB.QueryRow(`SELECT body_json FROM events WHERE kind = 'deploy.warning' AND subject_id = ?`, id).Scan(&warn)
	if gw.to[0] != "srv_1" || !strings.Contains(warn, "Built on hel1: the build server builder is offline.") {
		t.Fatalf("%s %s", gw.to[0], warn)
	}
}
