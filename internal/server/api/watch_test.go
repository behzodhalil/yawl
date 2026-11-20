package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/server/watch"
	"github.com/behzodhalil/yawl/internal/server/watch/watchtest"
)

var testVault *vault.Vault

type watchState struct {
	State, URL, Error, Account, Note string
}

func setupWatch(t *testing.T) (*httptest.Server, string, *app.App, *watchtest.Cloudflare) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	testDB = st.DB
	v, err := vault.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	testVault = v
	ctx := context.Background()
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "instance.id", Value: "ins_abcdefgh1234"})
	vault.SetSecret(ctx, st.Q, v, "telegram.token", []byte("123:BOT"))
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "telegram.chat_id", Value: "777"})
	cf := watchtest.NewCloudflare(t, "cf-token")
	a := app.New(st)
	tok, _ := a.CreateToken(ctx, "test")
	srv := httptest.NewServer(New(Deps{App: a, Vault: v, CloudflareBase: cf.URL,
		WatchCode: func() ([]byte, error) { return []byte("export default {}"), nil }}))
	t.Cleanup(srv.Close)
	return srv, tok, a, cf
}

func watchGet(t *testing.T, b []byte) watchState {
	var s watchState
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return s
}

func TestWatchSettings(t *testing.T) {
	srv, tok, _, _ := setupWatch(t)
	if _, b := call(t, srv, tok, "GET", "/api/settings/watch", ""); watchGet(t, b).State != "off" {
		t.Fatalf("before: %s", b)
	}
	res, b := call(t, srv, tok, "PUT", "/api/settings/watch", `{"token":" cf-token ","account":"acc1"}`)
	s := watchGet(t, b)
	if res.StatusCode != 200 || s.State != "on" || s.URL != "https://yawl-watch-abcdefgh.yawl-abcdefgh.workers.dev" || s.Account != "acc1" {
		t.Fatalf("put: %d %s", res.StatusCode, b)
	}
	if _, b := call(t, srv, tok, "GET", "/api/settings/watch", ""); watchGet(t, b).State != "on" {
		t.Fatalf("get: %s", b)
	}
	res, b = call(t, srv, tok, "DELETE", "/api/settings/watch", "")
	s = watchGet(t, b)
	if res.StatusCode != 200 || s.State != "off" || s.Note != "The watcher stays in your Cloudflare account; delete yawl-watch-abcdefgh there to remove it." {
		t.Fatalf("delete: %d %s", res.StatusCode, b)
	}
}

func TestWatchSettingsStepError(t *testing.T) {
	srv, tok, _, _ := setupWatch(t)
	res, b := call(t, srv, tok, "PUT", "/api/settings/watch", `{"token":"wrong","account":"acc1"}`)
	if res.StatusCode != 400 || string(b) != "Could not check the token: Authentication error\n" {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
	if _, b := call(t, srv, tok, "GET", "/api/settings/watch", ""); watchGet(t, b).State != "error" || watchGet(t, b).Error != "Could not check the token: Authentication error" {
		t.Fatalf("get: %s", b)
	}
}

func TestWatchSettingsNeedsBoth(t *testing.T) {
	srv, tok, _, _ := setupWatch(t)
	res, b := call(t, srv, tok, "PUT", "/api/settings/watch", `{"token":"","account":"acc1"}`)
	if res.StatusCode != 400 || string(b) != "Paste a Cloudflare API token and your account id.\n" {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
}

func TestWatchSettingsRelayChatsOnly(t *testing.T) {
	srv, tok, a, _ := setupWatch(t)
	ctx := context.Background()
	a.St.Q.DeleteSettingKey(ctx, "telegram.token")
	a.St.Q.DeleteSettingKey(ctx, "telegram.chat_id")
	res, b := call(t, srv, tok, "PUT", "/api/settings/watch", `{"token":"cf-token","account":"acc1"}`)
	if res.StatusCode != 400 || string(b) != watch.ErrNoTelegram.Error()+"\n" {
		t.Fatalf("neither: %d %q", res.StatusCode, b)
	}
	vault.SetSecret(ctx, a.St.Q, testVault, "telegram.relay_key", []byte("rk"))
	a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "telegram.relay_chats", Value: `[{"id":"-100","linked_at":1}]`})
	res, b = call(t, srv, tok, "PUT", "/api/settings/watch", `{"token":"cf-token","account":"acc1"}`)
	if s := watchGet(t, b); res.StatusCode != 200 || s.State != "on" {
		t.Fatalf("relay chats: %d %s", res.StatusCode, b)
	}
}

func withWorker(t *testing.T, a *app.App, v *vault.Vault) *watchtest.Worker {
	ctx := context.Background()
	key, _ := watch.Key(ctx, a.St.Q, v)
	wk := watchtest.NewWorker(t, key)
	for k, val := range map[string]string{"watch.url": wk.URL, "watch.account": "acc1"} {
		a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: val})
	}
	vault.SetSecret(ctx, a.St.Q, v, "watch.cf_token", []byte("cf-token"))
	return wk
}

func TestIncidentsProxy(t *testing.T) {
	srv, tok, a, _ := setupWatch(t)
	if _, b := call(t, srv, tok, "GET", "/api/incidents?days=90", ""); string(b) != "{\"off\":true}\n" {
		t.Fatalf("off: %s", b)
	}
	if _, b := call(t, srv, tok, "GET", "/api/checks", ""); string(b) != "{\"off\":true}\n" {
		t.Fatalf("off: %s", b)
	}
	wk := withWorker(t, a, testVault)
	if res, b := call(t, srv, tok, "GET", "/api/incidents?days=90", ""); res.StatusCode != 200 || !strings.Contains(string(b), `"inc_1"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if _, b := call(t, srv, tok, "GET", "/api/checks", ""); !strings.Contains(string(b), `"service:svc_1"`) {
		t.Fatalf("%s", b)
	}
	if res, _ := call(t, srv, tok, "POST", "/api/incidents/inc_1/note", `{"text":"rolling back"}`); res.StatusCode != 204 {
		t.Fatalf("note %d", res.StatusCode)
	}
	if n := wk.Incidents[len(wk.Incidents)-1]; n["action"] != "note" || n["id"] != "inc_1" || n["text"] != "rolling back" {
		t.Fatalf("note %v", n)
	}
	wk.Status = 503
	if res, b := call(t, srv, tok, "GET", "/api/incidents", ""); res.StatusCode != 502 || !strings.Contains(string(b), "503") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestWatchStateCounts(t *testing.T) {
	srv, tok, a, _ := setupWatch(t)
	wk := withWorker(t, a, testVault)
	a.St.DB.Exec(`INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, created_at, updated_at) VALUES ('srv_1','hel1','1.2.3.4','','',1,1)`)
	a.St.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "watch.agent.srv_1", Value: watch.AgentMarker("x", wk.URL)})
	a.St.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "watch.deployed", Value: "x"})
	_, b := call(t, srv, tok, "GET", "/api/settings/watch", "")
	var s struct {
		State   string
		Checks  int
		LastRun int64 `json:"last_run"`
		Servers []struct{ Name, State string }
	}
	json.Unmarshal(b, &s)
	if s.State != "on" || s.Checks != 1 || s.LastRun != 1 || len(s.Servers) != 1 || s.Servers[0].Name != "hel1" || s.Servers[0].State != "watched" {
		t.Fatalf("%s", b)
	}
	a.St.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "watch.deployed", Value: "y"})
	_, b = call(t, srv, tok, "GET", "/api/settings/watch", "")
	json.Unmarshal(b, &s)
	if s.Servers[0].State != "not watched" {
		t.Fatalf("%s", b)
	}
}

func TestHeartbeatsAPI(t *testing.T) {
	srv, tok, a, _ := setupWatch(t)
	if res, b := call(t, srv, tok, "POST", "/api/heartbeats", `{"name":"laptop","period_s":3600,"grace_s":600}`); res.StatusCode != 400 || string(b) != "Deploy the watcher first: heartbeats are checked from Cloudflare.\n" {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
	withWorker(t, a, testVault)
	res, b := call(t, srv, tok, "POST", "/api/heartbeats", `{"name":"laptop","period_s":3600,"grace_s":600}`)
	var hb struct{ ID, URL string }
	json.Unmarshal(b, &hb)
	u, _ := a.St.Q.GetSetting(context.Background(), "watch.url")
	if res.StatusCode != 200 || !regexp.MustCompile(`^`+regexp.QuoteMeta(u)+`/hb/[0-9a-f]{32}$`).MatchString(hb.URL) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/heartbeats", `{"name":"laptop","period_s":3600,"grace_s":600}`); res.StatusCode != 400 || string(b) != "There is already a heartbeat named laptop.\n" {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/heartbeats", `{"name":"fast","period_s":30,"grace_s":0}`); res.StatusCode != 400 || string(b) != "A heartbeat's period is at least 60 seconds.\n" {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
	_, b = call(t, srv, tok, "GET", "/api/heartbeats", "")
	if !strings.Contains(string(b), hb.URL) {
		t.Fatalf("list %s", b)
	}
	viewer := memberToken(t, srv, tok, "view@acme.uz", "viewer")
	if _, b := call(t, srv, viewer, "GET", "/api/heartbeats", ""); strings.Contains(string(b), "/hb/") || !strings.Contains(string(b), `"name":"laptop"`) {
		t.Fatalf("viewer list %s", b)
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/heartbeats/"+hb.ID, ""); res.StatusCode != 204 {
		t.Fatalf("delete %d", res.StatusCode)
	}
	if _, b := call(t, srv, tok, "GET", "/api/heartbeats", ""); string(b) != "[]\n" {
		t.Fatalf("after delete %s", b)
	}
}

func TestWatchStateHungWorker(t *testing.T) {
	srv, tok, a, _ := setupWatch(t)
	withWorker(t, a, testVault)
	release := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(hung.Close)
	t.Cleanup(func() { close(release) })
	a.St.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "watch.url", Value: hung.URL})
	old := checksWait
	checksWait = 200 * time.Millisecond
	t.Cleanup(func() { checksWait = old })
	start := time.Now()
	res, b := call(t, srv, tok, "GET", "/api/settings/watch", "")
	if res.StatusCode != 200 || time.Since(start) > 5*time.Second || watchGet(t, b).State != "on" {
		t.Fatalf("%d after %s: %s", res.StatusCode, time.Since(start), b)
	}
}
