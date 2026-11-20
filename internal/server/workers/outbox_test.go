package workers

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/notify"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/spec"
)

type fakeNotifier struct {
	err  error
	sent []string
	msgs []notify.Message
}

func (f *fakeNotifier) Send(_ context.Context, m notify.Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent, f.msgs = append(f.sent, m.Text), append(f.msgs, m)
	return nil
}

func addOutbox(t *testing.T, st *store.Store, at time.Time) string {
	ctx, id := context.Background(), store.NewID("obx_")
	err := st.Tx(ctx, func(q *db.Queries) error { return app.Event(ctx, q, at.UnixMilli(), "x", "s", "system", nil) })
	if err != nil {
		t.Fatal(err)
	}
	var evID string
	st.DB.QueryRow(`SELECT id FROM events LIMIT 1`).Scan(&evID)
	if err := st.Q.InsertOutbox(ctx, db.InsertOutboxParams{ID: id, EventID: evID, Channel: "telegram", Body: "hi",
		NextAttemptAt: at.UnixMilli(), CreatedAt: at.UnixMilli(), UpdatedAt: at.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestOutboxSends(t *testing.T) {
	st, _ := seed(t, "web")
	t0 := time.UnixMilli(1_000_000_000)
	addOutbox(t, st, t0)
	n := &fakeNotifier{}
	o := &Outbox{St: st, Notifier: func(context.Context) (Notifier, error) { return n, nil }}
	o.Tick(context.Background(), t0)
	rows, _ := st.Q.ListOutbox(context.Background())
	if len(n.sent) != 1 || !rows[0].SentAt.Valid {
		t.Fatalf("sent %v rows %+v", n.sent, rows)
	}
	o.Tick(context.Background(), t0.Add(time.Hour))
	if len(n.sent) != 1 {
		t.Fatal("sent twice")
	}
}

func TestOutboxBackoffAndGiveUp(t *testing.T) {
	st, _ := seed(t, "web")
	ctx := context.Background()
	t0 := time.UnixMilli(1_000_000_000)
	addOutbox(t, st, t0)
	n := &fakeNotifier{err: errors.New("telegram 502")}
	o := &Outbox{St: st, Notifier: func(context.Context) (Notifier, error) { return n, nil }}
	now := t0
	for i, wantGap := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute} {
		o.Tick(ctx, now)
		row, _ := st.Q.ListOutbox(ctx)
		if gap := time.UnixMilli(row[0].NextAttemptAt).Sub(now); gap != wantGap || row[0].Attempts != int64(i+1) {
			t.Fatalf("attempt %d: gap %v attempts %d", i+1, gap, row[0].Attempts)
		}
		now = time.UnixMilli(row[0].NextAttemptAt)
	}
	o.Tick(ctx, t0.Add(24*time.Hour+time.Minute))
	row, _ := st.Q.ListOutbox(ctx)
	if !row[0].FailedAt.Valid || row[0].LastError != "telegram 502" {
		t.Fatalf("not given up: %+v", row[0])
	}
	before := row[0].Attempts
	o.Tick(ctx, t0.Add(48*time.Hour))
	if row, _ := st.Q.ListOutbox(ctx); row[0].Attempts != before {
		t.Fatal("retried after giving up")
	}
}

func TestOutboxMessages(t *testing.T) {
	st, _ := seed(t, "web")
	ctx := context.Background()
	t0 := time.UnixMilli(1_000_000_000)
	addOutbox(t, st, t0)
	m := app.DeployFailed("acme/production/api", "dep_1", "3f0a1a3", "build", "exit 1", "nothing", false, "acme-api deploy failed: exit 1.")
	if err := st.Tx(ctx, func(q *db.Queries) error {
		return announceIncident(ctx, q, t0.UnixMilli(), "deploy.failed", "dep_1", nil, m, "", "", "", false)
	}); err != nil {
		t.Fatal(err)
	}
	n := &fakeNotifier{}
	o := &Outbox{St: st, Notifier: func(context.Context) (Notifier, error) { return n, nil }, PublicURL: "https://panel.acme.uz"}
	o.Tick(ctx, t0)
	if len(n.msgs) != 2 || n.msgs[0].State != "" || n.msgs[0].Text != "hi" || n.msgs[0].Links != nil {
		t.Fatalf("legacy row: %+v", n.msgs)
	}
	got := n.msgs[1]
	if got.State != "fail" || got.Text != m.Text || !got.At.Equal(t0) || len(got.Links) != 2 || got.Links[0].URL != "https://panel.acme.uz/p/acme/production/s/api/deploys/dep_1" {
		t.Fatalf("%+v", got)
	}
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.domain", Value: "yawl.acme.uz"})
	if err := st.Tx(ctx, func(q *db.Queries) error {
		return announceIncident(ctx, q, t0.UnixMilli(), "deploy.failed", "dep_1", nil, m, "", "", "", false)
	}); err != nil {
		t.Fatal(err)
	}
	o.Tick(ctx, t0)
	if u := n.msgs[2].Links[1].URL; u != "https://yawl.acme.uz/p/acme/production/s/api" {
		t.Fatalf("%s", u)
	}
}

func TestOutboxWatcherPayload(t *testing.T) {
	st, o, _, wk, now := outboxRig(t)
	m := app.ServiceDown("acme/production/api", spec.KindWeb, "GET / answered 502", "acme-api is down: 502.")
	if err := st.Tx(ctx0, func(q *db.Queries) error {
		return announceIncident(ctx0, q, now.UnixMilli(), "service.down", "svc_1", nil, m, "service:svc_1", "open", "acme/production/api", false)
	}); err != nil {
		t.Fatal(err)
	}
	put(t, st, now, "disk:srv_1:85", "open", false, "hel1 disk is 85% full.")
	o.Tick(ctx0, now)
	by := map[string]map[string]any{}
	for _, i := range wk.Incidents {
		by[i["subject"].(string)] = i
	}
	down, disk := by["service:svc_1"], by["disk:srv_1:85"]
	tg, _ := down["telegram"].(map[string]any)
	if len(wk.Incidents) != 2 || down["text"] != m.Text || tg["parse_mode"] != "HTML" || !strings.HasPrefix(tg["text"].(string), "ⓧ <b>Production service is down</b>") {
		t.Fatalf("%v", wk.Incidents)
	}
	if _, ok := disk["telegram"]; ok {
		t.Fatalf("%v", disk)
	}
}

func TestTelegramFromSettingsPicksRelay(t *testing.T) {
	st, _ := seed(t, "web")
	ctx := context.Background()
	v, err := vault.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if r.URL.Path != "/v1/send" || r.Header.Get("Authorization") != "Bearer rk" {
			w.WriteHeader(401)
			return
		}
		got = append(got, b)
		w.Write([]byte(`{"sent":1,"gone":0}`))
	}))
	defer relay.Close()
	pick := TelegramFromSettings(st, v)
	if n, _ := pick(ctx); n != nil {
		t.Fatalf("nothing set up: %T", n)
	}
	vault.SetSecret(ctx, st.Q, v, "telegram.token", []byte("123:BOT"))
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "telegram.chat_id", Value: "777"})
	if n, _ := pick(ctx); n == nil {
		t.Fatal("own bot not picked")
	} else if _, isRelay := n.(notify.Relay); isRelay {
		t.Fatal("relay picked with no relay chat")
	}
	vault.SetSecret(ctx, st.Q, v, "telegram.relay_key", []byte("rk"))
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "telegram.relay_url", Value: relay.URL})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "telegram.relay_chats", Value: `[{"id":"7","type":"private","title":"B","linked_at":1}]`})
	n, _ := pick(ctx)
	if err := n.Send(ctx, notify.Message{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0]["telegram"].(map[string]any)["text"] != "hi" || got[0]["chat_id"] != nil {
		t.Fatalf("relay got %v", got)
	}
}

func TestOutboxRelayRoutes(t *testing.T) {
	st, _ := seed(t, "web")
	ctx := context.Background()
	st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_2", Name: "fsn1", PublicIp: "5.6.7.8", CreatedAt: 1, UpdatedAt: 1})
	v, err := vault.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	fail := "-200"
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			ChatID   string         `json:"chat_id"`
			Telegram map[string]any `json:"telegram"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		if b.ChatID == fail {
			http.Error(w, "Telegram did not take the message in 1 of 1 chats.", 502)
			return
		}
		got = append(got, cmp.Or(b.ChatID, "all")+" "+strings.SplitN(b.Telegram["text"].(string), "\n", 2)[0])
		w.Write([]byte(`{"sent":1,"gone":0}`))
	}))
	defer relay.Close()
	vault.SetSecret(ctx, st.Q, v, "telegram.relay_key", []byte("rk"))
	for k, val := range map[string]string{
		"telegram.relay_url":    relay.URL,
		"telegram.relay_chats":  `[{"id":"7","linked_at":1},{"id":"-100","linked_at":1},{"id":"-200","linked_at":1},{"id":"-300","linked_at":1,"gone_at":9}]`,
		"telegram.relay_routes": `{"-100":{"servers":["srv_2"],"projects":[]},"-200":{"servers":[],"projects":["acme"]},"-300":{"servers":["srv_1"]}}`,
	} {
		st.Q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: val})
	}
	t0 := time.UnixMilli(1_000_000_000)
	for _, m := range []notify.Message{
		app.ServiceDown("acme/production/api", spec.Kind("web"), "HTTP 502", "api is down."),
		app.ServerDisk("fsn1", 90, 85, "fsn1 disk is 90% full."),
		app.PanelBackupFailed("no storage", "never", "Panel backup failed."),
		{Text: "an old row"},
	} {
		if err := app.Announce(ctx, st.Q, t0.UnixMilli(), "x", "s", "system", m); err != nil {
			t.Fatal(err)
		}
		t0 = t0.Add(time.Millisecond)
	}
	o := &Outbox{St: st, Notifier: TelegramFromSettings(st, v)}
	o.Tick(ctx, t0)
	want := []string{"7 ⓧ <b>Production service is down</b>", "7 ⓘ <b>Server fsn1 disk is filling up</b>", "-100 ⓘ <b>Server fsn1 disk is filling up</b>",
		"all ⓧ <b>Panel backup failed</b>", "all an old row"}
	if !slices.Equal(got, want) {
		t.Fatalf("sent\n%q\nwant\n%q", got, want)
	}
	fail, got = "", nil
	o.Tick(ctx, t0.Add(time.Hour))
	if !slices.Equal(got, []string{"-200 ⓧ <b>Production service is down</b>"}) {
		t.Fatalf("retry sent %q", got)
	}
	rows, _ := st.Q.ListOutbox(ctx)
	for _, r := range rows {
		if !r.SentAt.Valid {
			t.Fatalf("not done: %+v", r)
		}
	}
}
