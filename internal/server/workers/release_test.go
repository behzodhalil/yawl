package workers

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func releaseSvc(t *testing.T, kind string, withDB bool) (*store.Store, string) {
	t.Helper()
	st, svc := seed(t, kind)
	ctx := context.Background()
	if withDB {
		dbID := addDB(t, st)
		deployOnce(t, st, dbID, &fakeSender{})
		st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "v1", EnvironmentID: "env_1", ServiceID: svc, Key: "DATABASE_URL", Ref: "acme-db.url"})
	}
	if _, err := st.DB.Exec(`UPDATE services SET release_json = '["/app/pgnote","migrate"]', release_timeout_ms = 600000 WHERE id = ?`, svc); err != nil {
		t.Fatal(err)
	}
	return st, svc
}

func TestReleaseRunsAfterSnapshot(t *testing.T) {
	st, svc := releaseSvc(t, "web", true)
	gw := &fakeSender{}
	id, _ := deployOnce(t, st, svc, gw)
	if got := gw.names()[:4]; !slices.Equal(got, []string{proto.CmdBuild, proto.CmdSnapshot, proto.CmdRelease, proto.CmdStart}) {
		t.Fatalf("sent %v", gw.names())
	}
	var snap proto.SnapshotArgs
	json.Unmarshal(gw.sent[1].Args, &snap)
	if snap.Name != "acme-db" || snap.Password != "pw-a" || snap.Version != 17 || snap.Project != "acme" {
		t.Fatalf("snapshot %+v", snap)
	}
	var rel proto.ReleaseArgs
	json.Unmarshal(gw.sent[2].Args, &rel)
	if rel.Image != "yawl/acme-production-api:"+id || !slices.Equal(rel.Cmd, []string{"/app/pgnote", "migrate"}) ||
		!slices.Equal(rel.Vars, []proto.SealedVar{{Key: "DATABASE_URL", Box: "url-a"}}) || rel.Port != 10000 || rel.Kind != "web" {
		t.Fatalf("release %+v", rel)
	}
	if d, _ := st.Q.GetDeploy(context.Background(), id); d.State != "live" {
		t.Fatal(d.State)
	}
}

func TestReleaseWithoutDBNoSnapshot(t *testing.T) {
	st, svc := releaseSvc(t, "web", false)
	gw := &fakeSender{}
	deployOnce(t, st, svc, gw)
	if got := gw.names()[:3]; !slices.Equal(got, []string{proto.CmdBuild, proto.CmdRelease, proto.CmdStart}) {
		t.Fatalf("sent %v", gw.names())
	}
}

func TestReleaseFailureKeepsOld(t *testing.T) {
	st, svc := seed(t, "web")
	first, _ := deployOnce(t, st, svc, &fakeSender{})
	st.DB.Exec(`UPDATE services SET release_json = '["/app/pgnote","migrate"]', release_timeout_ms = 600000 WHERE id = ?`, svc)
	gw := &fakeSender{fail: map[string]string{proto.CmdRelease: "Release command exited with code 1: migration failed"}}
	id, _ := deployOnce(t, st, svc, gw)
	ctx := context.Background()
	d, _ := st.Q.GetDeploy(ctx, id)
	s, _ := st.Q.GetService(ctx, svc)
	if d.State != "failed" || d.Cause != "Release command exited with code 1: migration failed" || s.CurrentDeployID.String != first {
		t.Fatalf("deploy %s %q current %s", d.State, d.Cause, s.CurrentDeployID.String)
	}
	if slices.Contains(gw.names(), proto.CmdStart) {
		t.Fatal("started after a failed release")
	}
	rows, _ := st.Q.ListOutbox(ctx)
	if len(rows) != 1 || !strings.HasSuffix(rows[0].Body, "The previous version is still live.") {
		t.Fatalf("outbox %+v", rows)
	}
}

func TestReleaseNotRepeatedOnResume(t *testing.T) {
	st, svc := releaseSvc(t, "web", false)
	ctx := context.Background()
	id, _ := app.New(st).Deploy(ctx, svc, "", "owner")
	gw := &fakeSender{}
	stop := &stopAfter{inner: gw, after: proto.CmdStart}
	cctx, cancel := context.WithCancel(ctx)
	stop.cancel = cancel
	NewRunner(st, stop, nil).Run(cctx, id)
	gw2 := &fakeSender{}
	if err := NewRunner(st, gw2, nil).Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(gw2.names(), proto.CmdRelease) || slices.Contains(gw2.names(), proto.CmdBuild) {
		t.Fatalf("resume sent %v", gw2.names())
	}
}

func TestRollbackDeploy(t *testing.T) {
	st, svc := releaseSvc(t, "web", true)
	ctx := context.Background()
	first, _ := deployOnce(t, st, svc, &fakeSender{})
	second, _ := deployOnce(t, st, svc, &fakeSender{})
	a := app.New(st)
	id, _, err := a.Rollback(ctx, svc, "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := st.Q.GetDeploy(ctx, id)
	f, _ := st.Q.GetDeploy(ctx, first)
	if d.Rollback != 1 || d.Image != f.Image {
		t.Fatalf("rollback deploy %+v", d)
	}
	gw := &fakeSender{}
	if err := NewRunner(st, gw, nil).Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{proto.CmdBuild, proto.CmdSnapshot, proto.CmdRelease} {
		if slices.Contains(gw.names(), n) {
			t.Fatalf("rollback sent %v", gw.names())
		}
	}
	var start proto.StartArgs
	json.Unmarshal(gw.sent[0].Args, &start)
	d, _ = st.Q.GetDeploy(ctx, id)
	s, _ := st.Q.GetService(ctx, svc)
	if start.Image != f.Image || d.State != "live" || s.CurrentDeployID.String != id {
		t.Fatalf("start %s state %s current %s", start.Image, d.State, s.CurrentDeployID.String)
	}
	note := app.RollbackNote(ctx, st.Q, second)
	if want := "The database is unchanged: " + second + " ran a release command. Its snapshot is kept on hel1 at /var/lib/yawl/snapshots/yawl-acme-production-acme-db/" + second + ".dump."; note != want {
		t.Fatalf("note %q\nwant %q", note, want)
	}
}

func TestRollbackPrunedImage(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	first, _ := deployOnce(t, st, svc, &fakeSender{})
	deployOnce(t, st, svc, &fakeSender{})
	id, _, _ := app.New(st).Rollback(ctx, svc, "", "owner")
	gw := &fakeSender{fail: map[string]string{proto.CmdStart: "Error response from daemon: No such image: yawl/acme-production-api:" + first}}
	NewRunner(st, gw, nil).Run(ctx, id)
	if d, _ := st.Q.GetDeploy(ctx, id); d.Cause != "The image of "+first+" is no longer kept (Yawl keeps the last 3)." {
		t.Fatalf("cause %q", d.Cause)
	}
}

// Images are kept by the deploys that used them, not by their creation time:
// BuildKit gives an image rebuilt from cache its old time, so "newest three by
// time" once pruned the version a rollback needed.
func TestPruneKeepsRecentLiveImages(t *testing.T) {
	st, svc := seed(t, "web")
	ctx := context.Background()
	var ids []string
	for i := 0; i < 4; i++ {
		id, _ := deployOnce(t, st, svc, &fakeSender{})
		ids = append(ids, id)
	}
	img := func(id string) string { return "yawl/acme-production-api:" + id }
	rb, _, _ := app.New(st).Rollback(ctx, svc, ids[0], "owner")
	gw := &fakeSender{}
	if err := NewRunner(st, gw, nil).Run(ctx, rb); err != nil {
		t.Fatal(err)
	}
	var a proto.PruneArgs
	for _, c := range gw.sent {
		if c.Name == proto.CmdPrune {
			json.Unmarshal(c.Args, &a)
		}
	}
	if a.Repo != "yawl/acme-production-api" || !slices.Equal(a.Keep, []string{img(ids[0]), img(ids[3]), img(ids[2])}) {
		t.Fatalf("prune %+v", a)
	}
}

func TestSnapshotEveryLinkedDB(t *testing.T) {
	st, svc := releaseSvc(t, "web", true)
	ctx := context.Background()
	st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_db2", EnvironmentID: "env_1", Name: "jobs-db", Kind: "postgres", Version: 16, BackupAt: "03:00", CreatedAt: 1, UpdatedAt: 1})
	deployOnce(t, st, "svc_db2", &fakeSender{})
	st.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: "v2", EnvironmentID: "env_1", ServiceID: svc, Key: "JOBS_URL", Ref: "jobs-db.url"})
	gw := &fakeSender{}
	id, _ := deployOnce(t, st, svc, gw)
	var names []string
	for _, c := range gw.sent {
		if c.Name == proto.CmdSnapshot {
			var a proto.SnapshotArgs
			json.Unmarshal(c.Args, &a)
			names = append(names, a.Name)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"acme-db", "jobs-db"}) || !slices.Equal(gw.names()[:4], []string{proto.CmdBuild, proto.CmdSnapshot, proto.CmdSnapshot, proto.CmdRelease}) {
		t.Fatalf("snapshots of %v; sent %v", names, gw.names())
	}
	note := app.RollbackNote(ctx, st.Q, id)
	if !strings.Contains(note, "Its snapshots are kept on hel1 at ") || !strings.Contains(note, "acme-db/"+id+".dump") || !strings.Contains(note, "jobs-db/"+id+".dump") {
		t.Fatalf("note %q", note)
	}
}
