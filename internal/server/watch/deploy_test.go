package watch

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/server/watch/watchtest"
)

const cfToken = "cf-SECRET-token"

func setup(t *testing.T, telegram bool) (*store.Store, *vault.Vault, *watchtest.Cloudflare, Cloudflare) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	v, err := vault.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "instance.id", Value: "ins_abcdefgh1234"})
	if telegram {
		vault.SetSecret(ctx, st.Q, v, "telegram.token", []byte("123:BOT"))
		st.Q.SetSetting(ctx, db.SetSettingParams{Key: "telegram.chat_id", Value: "777"})
	}
	f := watchtest.NewCloudflare(t, cfToken)
	return st, v, f, Cloudflare{API: f.URL, Token: cfToken, Account: "acc1"}
}

var code = []byte("export default {}")

func TestPanelID(t *testing.T) {
	st, _, _, _ := setup(t, false)
	if got := PanelID(context.Background(), st); got != "abcdefgh" {
		t.Fatalf("PanelID = %q", got)
	}
}

func TestDeployCallsInOrder(t *testing.T) {
	st, v, f, cf := setup(t, true)
	ctx := context.Background()
	url, err := Deploy(ctx, st, v, cf, code)
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://yawl-watch-abcdefgh.yawl-abcdefgh.workers.dev" {
		t.Fatalf("url %q", url)
	}
	want := []string{
		"GET /user/tokens/verify",
		"GET /accounts/acc1/d1/database",
		"POST /accounts/acc1/d1/database",
		"POST /accounts/acc1/d1/database/db-1/query",
		"PUT /accounts/acc1/workers/scripts/yawl-watch-abcdefgh",
		"PUT /accounts/acc1/workers/scripts/yawl-watch-abcdefgh/schedules",
		"GET /accounts/acc1/workers/subdomain",
		"PUT /accounts/acc1/workers/subdomain",
		"POST /accounts/acc1/workers/scripts/yawl-watch-abcdefgh/subdomain",
	}
	if got := f.CallList(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(f.Queries[0], "CREATE TABLE IF NOT EXISTS incidents") {
		t.Fatalf("schema %q", f.Queries[0])
	}
	if f.Metadata["main_module"] != "index.js" || !bytes.Equal(f.Module, code) || f.ModType != "application/javascript+module" {
		t.Fatalf("upload %v %q %q", f.Metadata, f.Module, f.ModType)
	}
	binds := map[string]map[string]any{}
	for _, b := range f.Metadata["bindings"].([]any) {
		m := b.(map[string]any)
		binds[m["name"].(string)] = m
	}
	if binds["DB"]["type"] != "d1" || binds["DB"]["database_id"] != "db-1" {
		t.Fatalf("DB binding %v", binds["DB"])
	}
	key, _ := vault.GetSecret(ctx, st.Q, v, "watch.key")
	if len(key) != 32 {
		t.Fatalf("key %d bytes", len(key))
	}
	for name, want := range map[string]string{"BOT_TOKEN": "123:BOT", "CHAT_ID": "777", "PANEL_KEY": hexs(key)} {
		if binds[name]["type"] != "secret_text" || binds[name]["text"] != want {
			t.Fatalf("%s binding %v", name, binds[name])
		}
	}
	if binds["TELEGRAM_API"]["type"] != "plain_text" || binds["TELEGRAM_API"]["text"] != "https://api.telegram.org" {
		t.Fatalf("TELEGRAM_API %v", binds["TELEGRAM_API"])
	}
	if f.Schedule != `[{"cron":"* * * * *"}]` || !f.Enabled {
		t.Fatalf("schedule %q enabled %v", f.Schedule, f.Enabled)
	}
	for k, want := range map[string]string{"watch.db_id": "db-1", "watch.url": url, "watch.deployed": Fingerprint(code, key, map[string]string{"BOT_TOKEN": "123:BOT", "CHAT_ID": "777"})} {
		if got, _ := st.Q.GetSetting(ctx, k); got != want {
			t.Fatalf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestDeployIdempotent(t *testing.T) {
	st, v, f, cf := setup(t, true)
	ctx := context.Background()
	f.Subdomain = "acme"
	if _, err := Deploy(ctx, st, v, cf, code); err != nil {
		t.Fatal(err)
	}
	key1, _ := vault.GetSecret(ctx, st.Q, v, "watch.key")
	n := len(f.CallList())
	url, err := Deploy(ctx, st, v, cf, code)
	if err != nil {
		t.Fatal(err)
	}
	second := f.CallList()[n:]
	for _, c := range second {
		if c == "POST /accounts/acc1/d1/database" || c == "PUT /accounts/acc1/workers/subdomain" {
			t.Fatalf("second deploy made %s", c)
		}
	}
	if url != "https://yawl-watch-abcdefgh.acme.workers.dev" {
		t.Fatalf("url %q", url)
	}
	if key2, _ := vault.GetSecret(ctx, st.Q, v, "watch.key"); !bytes.Equal(key1, key2) {
		t.Fatal("the key changed")
	}
}

func TestDeployStepError(t *testing.T) {
	st, v, f, cf := setup(t, true)
	f.FailOn, f.FailMsg = "PUT /accounts/acc1/workers/scripts/yawl-watch-abcdefgh", "Authentication error"
	_, err := Deploy(context.Background(), st, v, cf, code)
	if err == nil || err.Error() != "Could not upload the watcher: Authentication error" {
		t.Fatalf("err %v", err)
	}
	if got, _ := st.Q.GetSetting(context.Background(), "watch.error"); got != err.Error() {
		t.Fatalf("watch.error %q", got)
	}
	if strings.Contains(err.Error(), cfToken) {
		t.Fatal("token in error")
	}
}

func TestDeployBadToken(t *testing.T) {
	st, v, _, cf := setup(t, true)
	cf.Token = "wrong"
	_, err := Deploy(context.Background(), st, v, cf, code)
	if err == nil || err.Error() != "Could not check the token: Authentication error" {
		t.Fatalf("err %v", err)
	}
}

func TestDeployNeedsTelegram(t *testing.T) {
	st, v, f, cf := setup(t, false)
	_, err := Deploy(context.Background(), st, v, cf, code)
	if err == nil || err.Error() != "Link Telegram first: the watcher sends its alerts there." {
		t.Fatalf("err %v", err)
	}
	if len(f.CallList()) != 0 {
		t.Fatalf("calls %v", f.CallList())
	}
}

func TestDeployTakesTurns(t *testing.T) {
	st, v, f, cf := setup(t, true)
	ctx := context.Background()
	f.Subdomain = "acme"
	done := make(chan error, 2)
	for range 2 {
		go func() { _, err := Deploy(ctx, st, v, cf, code); done <- err }()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for _, c := range f.CallList() {
		if c == "POST /accounts/acc1/d1/database" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d databases made", n)
	}
}

func TestDeployWithRelay(t *testing.T) {
	st, v, f, cf := setup(t, true)
	ctx := context.Background()
	if _, err := Deploy(ctx, st, v, cf, code); err != nil {
		t.Fatal(err)
	}
	before, _ := st.Q.GetSetting(ctx, "watch.deployed")
	vault.SetSecret(ctx, st.Q, v, "telegram.relay_key", []byte("rk"))
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "telegram.relay_url", Value: "https://relay.test/"})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "telegram.relay_chats", Value: `[{"id":"7","gone_at":5}]`})
	if s, _, _ := Telegram(ctx, st.Q, v); s["BOT_TOKEN"] == "" {
		t.Fatalf("a gone chat alone keeps the own bot: %v", s)
	}
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "telegram.relay_chats", Value: `[{"id":"7","gone_at":5},{"id":"8"}]`})
	if _, err := Deploy(ctx, st, v, cf, code); err != nil {
		t.Fatal(err)
	}
	binds := map[string]map[string]any{}
	for _, b := range f.Metadata["bindings"].([]any) {
		m := b.(map[string]any)
		binds[m["name"].(string)] = m
	}
	if binds["RELAY_KEY"]["type"] != "secret_text" || binds["RELAY_KEY"]["text"] != "rk" || binds["RELAY_URL"]["text"] != "https://relay.test" {
		t.Fatalf("relay bindings %v", binds)
	}
	if binds["BOT_TOKEN"] != nil || binds["CHAT_ID"] != nil {
		t.Fatalf("own bot still bound: %v", binds)
	}
	if after, _ := st.Q.GetSetting(ctx, "watch.deployed"); after == before {
		t.Fatal("the fingerprint did not change, so the minute would not redeploy")
	}
}
