package workers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/notify"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/server/watch"
	"github.com/behzodhalil/yawl/internal/server/watch/watchtest"
	"github.com/behzodhalil/yawl/internal/spec"
)

func keyed(t *testing.T, st *store.Store) []string {
	rows, _ := st.Q.ListOutbox(context.Background())
	var out []string
	for _, r := range rows {
		out = append(out, fmt.Sprintf("%s|%s|%d|%s|%s", r.Incident, r.Action, r.Quiet, r.Label, r.Body))
	}
	return out
}

func watched(st *store.Store) {
	st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "watch.url", Value: "https://w.example"})
}

func TestIncidentKeysProbe(t *testing.T) {
	st, svc := seed(t, "web")
	id, _ := deployOnce(t, st, svc, &fakeSender{})
	liveAt(t, st, id, tLive)
	gw := &probes{}
	w := &Watch{St: st, GW: gw, App: app.New(st)}
	at := ticks(t, w, tLive.Add(30*time.Second), 3)
	gw.up = true
	ticks(t, w, at, 1)
	want := "service:svc_1|open|0|acme/production/api|acme-api is down: GET / returned 502.\nservice:svc_1|close|0|acme/production/api|acme-api recovered."
	if got := strings.Join(keyed(t, st), "\n"); got != want {
		t.Fatalf("got\n%s", got)
	}
}

func TestIncidentKeysDiskAndOffline(t *testing.T) {
	st, _ := seed(t, "web")
	watched(st)
	w := &Watch{St: st, GW: &probes{up: true}, App: app.New(st)}
	for _, pct := range []int{86, 96, 50} {
		heartbeat(t, st, pct, tLive)
		ticks(t, w, tLive, 1)
	}
	ticks(t, w, tLive.Add(2*time.Minute+time.Second), 1)
	heartbeat(t, st, 10, tLive.Add(3*time.Minute))
	ticks(t, w, tLive.Add(3*time.Minute+5*time.Second), 1)
	want := []string{
		"disk:srv_1:85|open|0|hel1|hel1 disk is 85% full.",
		"disk:srv_1:95|open|0|hel1|hel1 disk is 95% full.",
		"disk:srv_1:85|close|1|hel1|hel1 disk is under 80% again.",
		"disk:srv_1:95|close|1|hel1|",
		"server:srv_1|open|0|hel1|hel1 agent has been offline for 2 minutes.",
		"server:srv_1|close|0|hel1|hel1 agent is back.",
	}
	if got := keyed(t, st); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
}

func TestIncidentKeysDeploy(t *testing.T) {
	st, svc := seed(t, "web")
	watched(st)
	deployOnce(t, st, svc, &fakeSender{fail: map[string]string{proto.CmdHealthCheck: "boom"}})
	deployOnce(t, st, svc, &fakeSender{})
	want := []string{
		"deploy:svc_1|open|0|acme/production/api|acme-api deploy failed: boom. Nothing was live before this deploy.",
		"deploy:svc_1|close|1|acme/production/api|acme-api deployed.",
	}
	if got := keyed(t, st); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
}

func TestQuietClosesNeedAWatcher(t *testing.T) {
	st, svc := seed(t, "web")
	deployOnce(t, st, svc, &fakeSender{})
	if got := keyed(t, st); len(got) != 0 {
		t.Fatalf("without a watcher a good deploy wrote %q", got)
	}
}

func TestIncidentKeysBackups(t *testing.T) {
	b, st, gw, c := backupWorld(t, true)
	watched(st)
	gw.fail = map[string]string{proto.CmdBackup: "403"}
	c.t = utc("2026-10-24 03:00")
	b.Tick(context.Background())
	gw.fail = nil
	a := app.New(st)
	a.Now = c.now
	a.Backup(context.Background(), "svc_db", "owner")
	b.Tick(context.Background())
	got := keyed(t, st)
	want := []string{
		"backup:svc_db:failed|open|0|acme/production/acme-db|acme-db backup failed: 403",
		"backup:svc_db|open|0|acme/production/acme-db|acme-db: no backup has succeeded in 26 hours (last: never).",
		"backup:svc_db:failed|close|1|acme/production/acme-db|acme-db backup succeeded again.",
		"backup:svc_db|close|1|acme/production/acme-db|",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
}

func TestIncidentKeysPanelBackup(t *testing.T) {
	day := time.Date(2026, 10, 25, 4, 0, 0, 0, time.UTC)
	w, mem, st, now := selfSetup(t, day)
	watched(st)
	mem.PutErr = errors.New("access denied")
	w.Tick(context.Background())
	mem.PutErr = nil
	*now = day.Add(24 * time.Hour)
	w.Tick(context.Background())
	want := []string{
		"panel-backup|open|0|The panel|The panel's own backup failed: access denied.",
		"panel-backup|close|1|The panel|Panel backup succeeded again.",
	}
	if got := keyed(t, st); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
}

func outboxRig(t *testing.T) (*store.Store, *Outbox, *fakeNotifier, *watchtest.Worker, time.Time) {
	st, _ := seed(t, "web")
	ctx := context.Background()
	v := mustVault(t, st)
	key, _ := watch.Key(ctx, st.Q, v)
	wk := watchtest.NewWorker(t, key)
	now := time.Now()
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "watch.url", Value: wk.URL})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "watch.ack_at", Value: strconv.FormatInt(now.UnixMilli(), 10)})
	n := &fakeNotifier{}
	o := &Outbox{St: st, Notifier: func(context.Context) (Notifier, error) { return n, nil },
		Watch: func(ctx context.Context) (watch.Client, bool) { return watch.ClientFromSettings(ctx, st.Q, v) }}
	return st, o, n, wk, now
}

func put(t *testing.T, st *store.Store, now time.Time, incident, action string, quiet bool, text string) {
	if err := st.Tx(context.Background(), func(q *db.Queries) error {
		return announceIncident(ctx0, q, now.UnixMilli(), "x", "s", nil, notify.Message{Text: text}, incident, action, "label", quiet)
	}); err != nil {
		t.Fatal(err)
	}
}

var ctx0 = context.Background()

func TestOutboxRoutesByWatcher(t *testing.T) {
	st, o, n, wk, now := outboxRig(t)
	put(t, st, now, "disk:srv_1:85", "open", false, "hel1 disk is 85% full.")
	put(t, st, now, "", "", false, "staging moved to hk1.")
	o.Tick(ctx0, now)
	if _, _, inc := wk.Count(); inc != 1 || wk.Incidents[0]["subject"] != "disk:srv_1:85" || wk.Incidents[0]["text"] != "hel1 disk is 85% full." || wk.Incidents[0]["label"] != "label" {
		t.Fatalf("incidents %v", wk.Incidents)
	}
	if strings.Join(n.sent, "|") != "staging moved to hk1." {
		t.Fatalf("telegram %v", n.sent)
	}
	st.Q.SetSetting(ctx0, db.SetSettingParams{Key: "watch.ack_at", Value: strconv.FormatInt(now.Add(-6*time.Minute).UnixMilli(), 10)})
	put(t, st, now, "service:svc_1", "open", false, "acme-api is down: x.")
	put(t, st, now, "deploy:svc_1", "close", true, "acme-api deployed.")
	o.Tick(ctx0, now)
	if strings.Join(n.sent, "|") != "staging moved to hk1.|acme-api is down: x." {
		t.Fatalf("telegram %v", n.sent)
	}
	rows, _ := st.Q.ListOutbox(ctx0)
	for _, r := range rows {
		if r.SentAt.Valid == (r.Incident == "deploy:svc_1") {
			t.Fatalf("row %+v", r)
		}
	}
	st.DB.Exec(`DELETE FROM outbox WHERE incident = 'deploy:svc_1'`)
	st.Q.SetSetting(ctx0, db.SetSettingParams{Key: "watch.ack_at", Value: strconv.FormatInt(now.UnixMilli(), 10)})
	wk.Status = 503
	put(t, st, now, "panel-backup", "open", false, "The panel's own backup failed: x.")
	o.Tick(ctx0, now)
	rows, _ = st.Q.ListOutbox(ctx0)
	last := rows[len(rows)-1]
	if last.SentAt.Valid || last.Attempts != 1 || last.NextAttemptAt != now.Add(time.Minute).UnixMilli() {
		t.Fatalf("row %+v", last)
	}
}

func TestProbeDownIgnoredWhenWatched(t *testing.T) {
	st, o, n, wk, now := outboxRig(t)
	wk.Watched["service:svc_1"] = true
	id, _ := deployOnce(t, st, "svc_1", &fakeSender{})
	liveAt(t, st, id, tLive)
	w := &Watch{St: st, GW: &probes{}, App: app.New(st)}
	ticks(t, w, tLive.Add(30*time.Second), 3)
	o.Tick(ctx0, now)
	if _, _, inc := wk.Count(); inc != 1 || wk.Incidents[0]["subject"] != "service:svc_1" {
		t.Fatalf("incidents %v", wk.Incidents)
	}
	if len(n.sent) != 0 {
		t.Fatalf("telegram %v", n.sent)
	}
}

var _ = vault.SetSecret

func TestOutboxOwesCloseToWatcher(t *testing.T) {
	st, o, n, wk, now := outboxRig(t)
	st.Q.SetSetting(ctx0, db.SetSettingParams{Key: "watch.ack_at", Value: strconv.FormatInt(now.Add(-6*time.Minute).UnixMilli(), 10)})
	put(t, st, now, "service:svc_1", "close", false, "acme-api recovered.")
	put(t, st, now, "deploy:svc_1", "close", true, "acme-api deployed.")
	put(t, st, now, "disk:srv_1:85", "open", false, "hel1 disk is 85% full.")
	o.Tick(ctx0, now)
	o.Tick(ctx0, now.Add(time.Minute))
	if strings.Join(n.sent, "|") != "acme-api recovered.|hel1 disk is 85% full." {
		t.Fatalf("telegram %v", n.sent)
	}
	if _, _, inc := wk.Count(); inc != 0 {
		t.Fatalf("incidents %v", wk.Incidents)
	}
	st.Q.SetSetting(ctx0, db.SetSettingParams{Key: "watch.ack_at", Value: strconv.FormatInt(now.Add(2*time.Minute).UnixMilli(), 10)})
	o.Tick(ctx0, now.Add(2*time.Minute))
	if _, _, inc := wk.Count(); inc != 2 {
		t.Fatalf("incidents %v", wk.Incidents)
	}
	if wk.Incidents[0]["subject"] != "service:svc_1" || wk.Incidents[0]["silent"] != true || wk.Incidents[1]["subject"] != "deploy:svc_1" || wk.Incidents[1]["silent"] == true {
		t.Fatalf("incidents %v", wk.Incidents)
	}
	if len(n.sent) != 2 {
		t.Fatalf("telegram again %v", n.sent)
	}
	rows, _ := st.Q.ListOutbox(ctx0)
	for _, r := range rows {
		if !r.SentAt.Valid {
			t.Fatalf("left: %+v", r)
		}
	}
}

func TestOutboxNoWatcherNothingOwed(t *testing.T) {
	st, _ := seed(t, "web")
	n := &fakeNotifier{}
	v := mustVault(t, st)
	o := &Outbox{St: st, Notifier: func(context.Context) (Notifier, error) { return n, nil },
		Watch: func(ctx context.Context) (watch.Client, bool) { return watch.ClientFromSettings(ctx, st.Q, v) }}
	now := time.Now()
	put(t, st, now, "service:svc_1", "close", false, "acme-api recovered.")
	o.Tick(ctx0, now)
	rows, _ := st.Q.ListOutbox(ctx0)
	if len(n.sent) != 1 || !rows[0].SentAt.Valid {
		t.Fatalf("sent %v rows %+v", n.sent, rows)
	}
}

func TestOutboxWatcherChats(t *testing.T) {
	st, o, _, wk, now := outboxRig(t)
	v := mustVault(t, st)
	st.Q.UpsertServer(ctx0, db.UpsertServerParams{ID: "srv_2", Name: "fsn1", PublicIp: "5.6.7.8", CreatedAt: 1, UpdatedAt: 1})
	vault.SetSecret(ctx0, st.Q, v, "telegram.relay_key", []byte("rk"))
	for k, val := range map[string]string{
		"telegram.relay_url":    "http://127.0.0.1:1",
		"telegram.relay_chats":  `[{"id":"7","linked_at":1},{"id":"-100","linked_at":1},{"id":"-200","linked_at":1}]`,
		"telegram.relay_routes": `{"-100":{"servers":["srv_2"]},"-200":{"projects":["acme"]}}`,
	} {
		st.Q.SetSetting(ctx0, db.SetSettingParams{Key: k, Value: val})
	}
	o.Notifier = TelegramFromSettings(st, v)
	for _, r := range []struct {
		incident, action string
		m                notify.Message
	}{
		{"service:svc_1", "open", app.ServiceDown("acme/production/api", spec.KindWeb, "GET / answered 502", "api is down.")},
		{"service:svc_1", "close", app.ServiceUp("acme/production/api", "2 minutes", "api is back.")},
		{"disk:srv_2:85", "open", app.ServerDisk("fsn1", 90, 85, "fsn1 disk is 90% full.")},
		{"panel-backup", "open", app.PanelBackupFailed("no storage", "never", "Panel backup failed.")},
	} {
		if err := st.Tx(ctx0, func(q *db.Queries) error {
			return announceIncident(ctx0, q, now.UnixMilli(), "x", "s", nil, r.m, r.incident, r.action, "label", false)
		}); err != nil {
			t.Fatal(err)
		}
	}
	o.Tick(ctx0, now)
	var got []string
	for _, i := range wk.Incidents {
		c, ok := i["chats"]
		got = append(got, fmt.Sprintf("%s %s %v", i["subject"], i["action"], map[bool]any{true: c, false: "all"}[ok]))
	}
	want := "service:svc_1 open [7 -200]|service:svc_1 close [7 -200]|disk:srv_2:85 open [7 -100]|panel-backup open all"
	if strings.Join(got, "|") != want {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "|"), want)
	}
}
