package workers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/server/watch"
	"github.com/behzodhalil/yawl/internal/server/watch/watchtest"
	"github.com/behzodhalil/yawl/internal/watchsig"
)

var watchCode = []byte("export default {}")

type watchRig struct {
	st  *store.Store
	v   *vault.Vault
	key []byte
	wk  *watchtest.Worker
	cf  *watchtest.Cloudflare
	gw  *fakeSender
	clk *time.Time
}

func (r *watchRig) watcher() *Watcher {
	return &Watcher{St: r.st, V: r.v, GW: r.gw, Code: func() ([]byte, error) { return watchCode, nil }, CFBase: r.cf.URL,
		Now: func() time.Time { return *r.clk }}
}

func watchSetup(t *testing.T) *watchRig {
	t.Helper()
	st, _ := seed(t, "web")
	ctx := context.Background()
	v, err := vault.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st.DB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, offline, created_at, updated_at) VALUES ('srv_2','hk1','5.6.7.8','','',1,1,1)`)
	set := func(k, val string) { st.Q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: val}) }
	set("instance.id", "ins_abcdefgh1234")
	vault.SetSecret(ctx, st.Q, v, "telegram.token", []byte("123:BOT"))
	set("telegram.chat_id", "777")
	vault.SetSecret(ctx, st.Q, v, "watch.cf_token", []byte("cf"))
	set("watch.account", "acc1")
	key, _ := watch.Key(ctx, st.Q, v)
	wk := watchtest.NewWorker(t, key)
	set("watch.url", wk.URL)
	set("watch.deployed", watch.Fingerprint(watchCode, key, map[string]string{"BOT_TOKEN": "123:BOT", "CHAT_ID": "777"}))
	clk := time.UnixMilli(1_798_000_000_000)
	return &watchRig{st: st, v: v, key: key, wk: wk, cf: watchtest.NewCloudflare(t, "cf"), gw: &fakeSender{}, clk: &clk}
}

func (r *watchRig) tick(t *testing.T, w *Watcher) {
	t.Helper()
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (r *watchRig) setting(k string) string {
	v, _ := r.st.Q.GetSetting(context.Background(), k)
	return v
}

func TestWatcherTick(t *testing.T) {
	r := watchSetup(t)
	ctx := context.Background()
	addDB(t, r.st)
	r.st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, finished_at, created_at, updated_at) VALUES ('bk_1','svc_db','k','ok',1700,1,1)`)
	w := r.watcher()
	r.tick(t, w)
	if got := r.wk.Subjects(); strings.Join(got, ",") != "panel,backup:svc_db" {
		t.Fatalf("signals %v", got)
	}
	if r.wk.Signals[1]["at"] != float64(1700) {
		t.Fatalf("backup signal %v", r.wk.Signals[1])
	}
	if _, n, _ := r.wk.Count(); n != 1 {
		t.Fatalf("expectations sent %d times", n)
	}
	cfg := r.wk.Expects[0]["config"].(map[string]any)
	if cfg["watch_url"] != r.wk.URL || cfg["tz"] == "" {
		t.Fatalf("config %v", cfg)
	}
	if len(r.gw.sent) != 1 || r.gw.sent[0].Name != proto.CmdWatch || r.gw.to[0] != "srv_1" {
		t.Fatalf("sent %v to %v", r.gw.names(), r.gw.to)
	}
	var a proto.WatchArgs
	json.Unmarshal(r.gw.sent[0].Args, &a)
	if a.URL != r.wk.URL || a.Key != hex.EncodeToString(watchsig.AgentKey(r.key, "srv_1")) {
		t.Fatalf("args %+v", a)
	}
	if !proto.Unrecorded(proto.CmdWatch) {
		t.Fatal("Watch must be unrecorded")
	}
	if !watch.Answering(ctx, r.st.Q, r.clk.UnixMilli()) {
		t.Fatal("not answering after an ack")
	}
	*r.clk = r.clk.Add(time.Minute)
	r.tick(t, w)
	if got := r.wk.Subjects(); len(got) != 3 || got[2] != "panel" {
		t.Fatalf("second tick signals %v", got)
	}
	if _, n, _ := r.wk.Count(); n != 1 || len(r.gw.sent) != 1 {
		t.Fatalf("second tick resent: expects %d, commands %d", n, len(r.gw.sent))
	}
	r.st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, finished_at, created_at, updated_at) VALUES ('bk_2','svc_db','k','ok',2700,2,2)`)
	r.tick(t, w)
	r.tick(t, w)
	if got := r.wk.Subjects(); strings.Join(got, ",") != "panel,backup:svc_db,panel,panel,backup:svc_db,panel" {
		t.Fatalf("signals %v", got)
	}
}

func TestWatchRestartQuiet(t *testing.T) {
	r := watchSetup(t)
	r.tick(t, r.watcher())
	r.tick(t, r.watcher())
	if len(r.cf.CallList()) != 0 {
		t.Fatalf("redeployed: %v", r.cf.CallList())
	}
	if _, n, _ := r.wk.Count(); n != 1 {
		t.Fatalf("expectations sent %d times", n)
	}
	r2 := watchSetup(t)
	r2.wk.Status = 503
	r2.tick(t, r2.watcher())
	if texts := outboxTexts(t, r2.st); len(texts) != 0 {
		t.Fatalf("outbox %v", texts)
	}
}

func TestWatchDownFallsBack(t *testing.T) {
	r := watchSetup(t)
	w := r.watcher()
	r.tick(t, w)
	r.wk.Status = 401
	*r.clk = r.clk.Add(time.Minute)
	r.tick(t, w)
	if e := r.setting("watch.error"); !strings.Contains(e, "401") {
		t.Fatalf("watch.error %q", e)
	}
	if texts := outboxTexts(t, r.st); len(texts) != 0 {
		t.Fatalf("too early: %v", texts)
	}
	*r.clk = r.clk.Add(5 * time.Minute)
	r.tick(t, w)
	r.tick(t, w)
	texts := outboxTexts(t, r.st)
	want := "The watcher is not answering: " + strings.TrimSuffix(r.setting("watch.error"), ".") + ". Alerts come from the panel until it does."
	if len(texts) != 1 || texts[0] != want {
		t.Fatalf("outbox %q, want %q", texts, want)
	}
	if watch.Answering(context.Background(), r.st.Q, r.clk.UnixMilli()) {
		t.Fatal("still answering")
	}
	r.wk.Status = 0
	r.tick(t, w)
	if texts := outboxTexts(t, r.st); len(texts) != 2 || texts[1] != "The watcher is answering again." {
		t.Fatalf("outbox %q", texts)
	}
	if e := r.setting("watch.error"); e != "" {
		t.Fatalf("watch.error kept %q", e)
	}
}

func TestWatchRedeploysOnChange(t *testing.T) {
	r := watchSetup(t)
	w := r.watcher()
	r.tick(t, w)
	r.st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "telegram.chat_id", Value: "888"})
	r.tick(t, w)
	if len(r.cf.CallList()) == 0 {
		t.Fatal("no redeploy after the chat changed")
	}
}

func TestWatchSendsRouteChanges(t *testing.T) {
	r := watchSetup(t)
	ctx := context.Background()
	set := func(k, val string) { r.st.Q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: val}) }
	vault.SetSecret(ctx, r.st.Q, r.v, "telegram.relay_key", []byte("rk"))
	set("telegram.relay_url", "https://tg.test")
	set("telegram.relay_chats", `[{"id":"7"},{"id":"-100"}]`)
	set("watch.deployed", watch.Fingerprint(watchCode, r.key, map[string]string{"RELAY_KEY": "rk"}, map[string]string{"RELAY_URL": "https://tg.test"}))
	w := r.watcher()
	r.tick(t, w)
	chats := func(i int) string { return r.wk.Expects[i]["config"].(map[string]any)["chats"].(string) }
	if chats(0) != "{}" {
		t.Fatalf("chats %s", chats(0))
	}
	set("telegram.relay_routes", `{"-100":{"servers":["srv_2"]}}`)
	*r.clk = r.clk.Add(time.Minute)
	r.tick(t, w)
	if _, n, _ := r.wk.Count(); n != 2 || !strings.Contains(chats(1), `"server:srv_1":["7"]`) || len(r.cf.CallList()) != 0 {
		t.Fatalf("expects %d, chats %s, cloudflare %v", n, chats(1), r.cf.CallList())
	}
}

func TestWatchUnknownCommand(t *testing.T) {
	r := watchSetup(t)
	r.gw.fail = map[string]string{proto.CmdWatch: "unknown command Watch"}
	w := r.watcher()
	r.tick(t, w)
	if r.setting("watch.agent.srv_1") != "" {
		t.Fatal("an agent that refused Watch is marked watched")
	}
	if got := r.wk.Subjects(); len(got) != 1 {
		t.Fatalf("signals %v", got)
	}
}

func TestWatchNotRunningIsNotAnswering(t *testing.T) {
	r := watchSetup(t)
	w := r.watcher()
	r.tick(t, w)
	r.wk.LastRunAge = 4 * time.Minute
	*r.clk = r.clk.Add(time.Minute)
	r.tick(t, w)
	if e := r.setting("watch.error"); e != "The watcher's minute last ran 4 minutes ago" {
		t.Fatalf("watch.error %q", e)
	}
	if watch.Answering(context.Background(), r.st.Q, r.clk.Add(5*time.Minute).UnixMilli()) {
		t.Fatal("answering")
	}
	*r.clk = r.clk.Add(5 * time.Minute)
	r.tick(t, w)
	if texts := outboxTexts(t, r.st); len(texts) != 1 || texts[0] != "The watcher is not answering: The watcher's minute last ran 4 minutes ago. Alerts come from the panel until it does." {
		t.Fatalf("%q", texts)
	}
}

func TestWatchNeverRanOrStuckBacklog(t *testing.T) {
	r := watchSetup(t)
	w := r.watcher()
	r.wk.NeverRan = true
	r.tick(t, w)
	if e := r.setting("watch.error"); e != "" {
		t.Fatalf("too early: %q", e)
	}
	r.st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "watch.norun_since", Value: strconv.FormatInt(time.Now().Add(-6*time.Minute).UnixMilli(), 10)})
	r.tick(t, w)
	if e := r.setting("watch.error"); e != "The watcher's minute has not run since it was deployed" {
		t.Fatalf("watch.error %q", e)
	}
	r2 := watchSetup(t)
	r2.wk.BacklogAge = 11 * time.Minute
	r2.tick(t, r2.watcher())
	if e := r2.setting("watch.error"); e != "The watcher has not sent a message for 11 minutes" {
		t.Fatalf("watch.error %q", e)
	}
}

func TestWatchNewURLResendsAgents(t *testing.T) {
	r := watchSetup(t)
	addDB(t, r.st)
	r.st.DB.Exec(`INSERT INTO backups (id, service_id, object_key, state, finished_at, created_at, updated_at) VALUES ('bk_1','svc_db','k','ok',1700,1,1)`)
	w := r.watcher()
	r.tick(t, w)
	wk2 := watchtest.NewWorker(t, r.key)
	r.st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "watch.url", Value: wk2.URL})
	r.tick(t, w)
	if len(r.gw.sent) != 2 {
		t.Fatalf("sent %v", r.gw.names())
	}
	var a proto.WatchArgs
	json.Unmarshal(r.gw.sent[1].Args, &a)
	if a.URL != wk2.URL {
		t.Fatalf("args %+v", a)
	}
	if got := strings.Join(wk2.Subjects(), ","); got != "panel,backup:svc_db" {
		t.Fatalf("new worker signals %s", got)
	}
}

func TestWatchResetAfterSetup(t *testing.T) {
	r := watchSetup(t)
	r.st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "watch.reset", Value: "1"})
	w := r.watcher()
	r.tick(t, w)
	if r.wk.Expects[0]["reset_panel"] != true {
		t.Fatalf("expects %v", r.wk.Expects[0])
	}
	if r.setting("watch.reset") != "" {
		t.Fatal("reset kept")
	}
}

func TestWatchNeverAnsweredNotAnswering(t *testing.T) {
	r := watchSetup(t)
	r.wk.Status = 503
	r.tick(t, r.watcher())
	if watch.Answering(context.Background(), r.st.Q, r.clk.UnixMilli()) {
		t.Fatal("answering without an acknowledgement")
	}
	*r.clk = r.clk.Add(5 * time.Minute)
	r.tick(t, r.watcher())
	if texts := outboxTexts(t, r.st); len(texts) != 1 || !strings.HasPrefix(texts[0], "The watcher is not answering") {
		t.Fatalf("outbox %q", texts)
	}
}
