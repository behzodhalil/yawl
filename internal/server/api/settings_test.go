package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/gitea"
	"github.com/behzodhalil/yawl/internal/server/gitlab"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

type fakeTelegram struct {
	mu      sync.Mutex
	token   string
	updates []string
	sent    []string
	webhook bool
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(r.URL.Path, "/bot"+f.token+"/") {
		w.WriteHeader(401)
		w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
		return
	}
	switch strings.TrimPrefix(r.URL.Path, "/bot"+f.token+"/") {
	case "getMe":
		w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"username":"acme_bot"}}`))
	case "getUpdates":
		if f.webhook {
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"ok":false,"error_code":409,"description":"Conflict: can't use getUpdates method while webhook is active"}`))
			return
		}
		var res []map[string]any
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		for i, u := range f.updates {
			if i+1 >= off && len(res) < 100 { // like Telegram: from the offset, 100 at most
				res = append(res, map[string]any{"update_id": i + 1, "message": map[string]any{"text": u, "chat": map[string]any{"id": 777}}})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": res})
	case "sendMessage":
		var b struct{ Text string }
		json.NewDecoder(r.Body).Decode(&b)
		f.sent = append(f.sent, b.Text)
		w.Write([]byte(`{"ok":true}`))
	}
}

func setupSettings(t *testing.T) (*httptest.Server, string, *app.App, *vault.Vault, *fakeTelegram) {
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
	tg := &fakeTelegram{token: "123:GOOD"}
	tgs := httptest.NewServer(tg)
	t.Cleanup(tgs.Close)
	a := app.New(st)
	tok, _ := a.CreateToken(context.Background(), "test")
	st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "telegram.relay_url", Value: "http://127.0.0.1:1"})
	srv := httptest.NewServer(New(Deps{App: a, Vault: v, TelegramBase: tgs.URL}))
	t.Cleanup(srv.Close)
	return srv, tok, a, v, tg
}

func tgState(t *testing.T, b []byte) (state, code, bot string) {
	var out struct{ State, Code, Bot string }
	json.Unmarshal(b, &out)
	return out.State, out.Code, out.Bot
}

func TestTelegramLink(t *testing.T) {
	srv, tok, a, v, tg := setupSettings(t)
	res, b := call(t, srv, tok, "POST", "/api/settings/telegram", `{"token":"123:GOOD"}`)
	state, code, bot := tgState(t, b)
	if res.StatusCode != 200 || state != "pending" || bot != "acme_bot" || !regexp.MustCompile(`^yawl-\d{6}$`).MatchString(code) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if _, b := call(t, srv, tok, "GET", "/api/settings/telegram", ""); func() string { s, _, _ := tgState(t, b); return s }() != "pending" {
		t.Fatalf("before the message: %s", b)
	}
	tg.mu.Lock()
	tg.updates = []string{"hello", "/start " + code}
	tg.mu.Unlock()
	_, b = call(t, srv, tok, "GET", "/api/settings/telegram", "")
	if s, _, bot := tgState(t, b); s != "linked" || bot != "acme_bot" {
		t.Fatalf("after the message: %s", b)
	}
	ctx := context.Background()
	if chat, _ := a.St.Q.GetSetting(ctx, "telegram.chat_id"); chat != "777" {
		t.Fatalf("chat %q", chat)
	}
	if got, _ := vault.GetSecret(ctx, a.St.Q, v, "telegram.token"); string(got) != "123:GOOD" {
		t.Fatalf("token %q", got)
	}
	if len(tg.sent) != 1 || tg.sent[0] != "Yawl is linked. Messages about your apps will come here." {
		t.Fatalf("sent %v", tg.sent)
	}
	if res, _ := call(t, srv, tok, "POST", "/api/settings/telegram/test", ""); res.StatusCode != 204 || tg.sent[1] != "Test message from Yawl." {
		t.Fatalf("test message: %d %v", res.StatusCode, tg.sent)
	}
	call(t, srv, tok, "DELETE", "/api/settings/telegram", "")
	if _, b := call(t, srv, tok, "GET", "/api/settings/telegram", ""); func() string { s, _, _ := tgState(t, b); return s }() != "none" {
		t.Fatalf("after unlink: %s", b)
	}
}

func TestTelegramLinkPastHundred(t *testing.T) {
	srv, tok, _, _, tg := setupSettings(t)
	_, b := call(t, srv, tok, "POST", "/api/settings/telegram", `{"token":"123:GOOD"}`)
	_, code, _ := tgState(t, b)
	tg.mu.Lock()
	for range 120 {
		tg.updates = append(tg.updates, "hello")
	}
	tg.updates = append(tg.updates, "/start "+code)
	tg.mu.Unlock()
	if _, b := call(t, srv, tok, "GET", "/api/settings/telegram", ""); func() string { s, _, _ := tgState(t, b); return s }() != "pending" {
		t.Fatalf("first read: %s", b)
	}
	if _, b := call(t, srv, tok, "GET", "/api/settings/telegram", ""); func() string { s, _, _ := tgState(t, b); return s }() != "linked" {
		t.Fatalf("second read: %s", b)
	}
}

func TestTelegramWebhookBot(t *testing.T) {
	srv, tok, _, _, tg := setupSettings(t)
	call(t, srv, tok, "POST", "/api/settings/telegram", `{"token":"123:GOOD"}`)
	tg.mu.Lock()
	tg.webhook = true
	tg.mu.Unlock()
	if res, b := call(t, srv, tok, "GET", "/api/settings/telegram", ""); res.StatusCode != 409 || strings.TrimSpace(string(b)) != "This bot has a webhook; use a bot of its own." {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestTelegramBadToken(t *testing.T) {
	srv, tok, _, _, _ := setupSettings(t)
	res, b := call(t, srv, tok, "POST", "/api/settings/telegram", `{"token":"999:BAD"}`)
	if res.StatusCode != 400 || strings.TrimSpace(string(b)) != "Telegram does not know that bot token." {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if strings.Contains(string(b), "999:BAD") {
		t.Fatal("the token is in the answer")
	}
}

func TestTelegramPendingExpires(t *testing.T) {
	srv, tok, a, _, _ := setupSettings(t)
	call(t, srv, tok, "POST", "/api/settings/telegram", `{"token":"123:GOOD"}`)
	a.Now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	_, b := call(t, srv, tok, "GET", "/api/settings/telegram", "")
	if s, _, _ := tgState(t, b); s != "none" {
		t.Fatalf("%s", b)
	}
}

func TestStorageNeverReturnsSecret(t *testing.T) {
	srv, tok, _, _, _ := setupSettings(t)
	if _, b := call(t, srv, tok, "GET", "/api/settings/storage", ""); !strings.Contains(string(b), `"set":false`) {
		t.Fatalf("unset: %s", b)
	}
	body := `{"endpoint":"s3.eu-central-003.backblazeb2.com","bucket":"acme","region":"","access_key":"AKIAEXAMPLE1234","secret_key":"very-secret-key-value"}`
	if res, b := call(t, srv, tok, "PUT", "/api/settings/storage", body); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	_, b := call(t, srv, tok, "GET", "/api/settings/storage", "")
	if strings.Contains(string(b), "very-secret") || strings.Contains(string(b), "AKIAEXAMPLE1234") ||
		!strings.Contains(string(b), `"access_key_end":"1234"`) || !strings.Contains(string(b), `"bucket":"acme"`) {
		t.Fatalf("%s", b)
	}
	if res, _ := call(t, srv, tok, "PUT", "/api/settings/storage", `{"endpoint":"x","bucket":"y","access_key":"k","secret_key":""}`); res.StatusCode != 400 {
		t.Fatalf("empty secret accepted: %d", res.StatusCode)
	}
}

func TestTokensLifecycle(t *testing.T) {
	srv, tok, _, _, _ := setupSettings(t)
	res, b := call(t, srv, tok, "POST", "/api/tokens", `{"name":"laptop"}`)
	var made struct{ Token string }
	json.Unmarshal(b, &made)
	if res.StatusCode != 200 || !strings.HasPrefix(made.Token, "yawl_") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, made.Token, "GET", "/api/status", ""); res.StatusCode != 200 {
		t.Fatal("new token refused")
	}
	_, b = call(t, srv, tok, "GET", "/api/tokens", "")
	var list []struct{ ID, Name string }
	json.Unmarshal(b, &list)
	if len(list) != 2 || strings.Contains(string(b), made.Token) || strings.Contains(string(b), "hash") {
		t.Fatalf("%s", b)
	}
	var id string
	for _, l := range list {
		if l.Name == "laptop" {
			id = l.ID
		}
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/tokens/"+id, ""); res.StatusCode != 204 {
		t.Fatalf("delete %d", res.StatusCode)
	}
	if res, _ := call(t, srv, made.Token, "GET", "/api/status", ""); res.StatusCode != 401 {
		t.Fatal("deleted token still works")
	}
	if res, _ := call(t, srv, tok, "POST", "/api/tokens", `{"name":""}`); res.StatusCode != 400 {
		t.Fatal("empty name accepted")
	}
}

func TestRecoveryKeyStatus(t *testing.T) {
	srv, tok, _, _, _ := setupSettings(t)
	if _, b := call(t, srv, tok, "GET", "/api/recovery-key", ""); !strings.Contains(string(b), `"set":false`) {
		t.Fatalf("%s", b)
	}
	pub := "q83vEjRWeJCrze8SNFZ4kKvN7xI0VniQq83vEjRWeJA="
	call(t, srv, tok, "PUT", "/api/recovery-key", `{"pubkey":"`+pub+`"}`)
	if _, b := call(t, srv, tok, "GET", "/api/recovery-key", ""); !strings.Contains(string(b), `"fingerprint":"q83vEjRW"`) {
		t.Fatalf("%s", b)
	}
}

func TestServerBackupNow(t *testing.T) {
	_, _, a, v, _ := setupSettings(t)
	ran := 0
	srv := httptest.NewServer(New(Deps{App: a, Vault: v, RunSelfBackup: func(context.Context) error { ran++; return nil }}))
	defer srv.Close()
	tok, _ := a.CreateToken(context.Background(), "x")
	if res, b := call(t, srv, tok, "POST", "/api/settings/server-backup", ""); res.StatusCode != 204 || ran != 1 {
		t.Fatalf("%d %s ran %d", res.StatusCode, b, ran)
	}
	a.St.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "server_backup.last_key", Value: "yawl-server/k"})
	if _, b := call(t, srv, tok, "GET", "/api/settings/server-backup", ""); !strings.Contains(string(b), `"last_key":"yawl-server/k"`) {
		t.Fatalf("%s", b)
	}
}

func TestPanelSettings(t *testing.T) {
	srv, tok, a, _, _ := setupSettings(t)
	ctx := context.Background()
	a.St.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_p", Name: "hel1", PublicIp: "5.6.7.8", CreatedAt: 1, UpdatedAt: 1})
	a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.server_id", Value: "srv_p"})
	a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.panel.5-6-7-8.sslip.io.cert", Value: "issued"})
	_, b := call(t, srv, tok, "GET", "/api/settings/panel", "")
	if !strings.Contains(string(b), `"host":"panel.5-6-7-8.sslip.io"`) || !strings.Contains(string(b), `"cert_state":"issued"`) {
		t.Fatalf("%s", b)
	}
	for _, bad := range []string{"Panel.Acme.uz", "x.sslip.io", "not a host"} {
		if res, _ := call(t, srv, tok, "PUT", "/api/settings/panel", `{"domain":"`+bad+`"}`); res.StatusCode != 400 {
			t.Fatalf("%q accepted", bad)
		}
	}
	if res, b := call(t, srv, tok, "POST", "/api/apply", strings.Replace(yaml, `"https://x/y"`, `"https://x/y", domains: [docs.acme.uz/api]`, 1)); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if _, err := a.St.DB.Exec(`INSERT INTO status_pages (id, slug, title, server_id, domain, created_at, updated_at) VALUES ('sp_1','acme','Acme','srv_p','status.acme.uz',1,1)`); err != nil {
		t.Fatal(err)
	}
	for _, taken := range []string{"docs.acme.uz", "status.acme.uz"} {
		if res, b := call(t, srv, tok, "PUT", "/api/settings/panel", `{"domain":"`+taken+`"}`); res.StatusCode != 400 || !strings.Contains(string(b), "already points") {
			t.Fatalf("%s: %d %s", taken, res.StatusCode, b)
		}
	}
	if res, b := call(t, srv, tok, "PUT", "/api/settings/panel", `{"domain":"panel.acme.uz"}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if v, _ := a.St.Q.GetSetting(ctx, "panel.domain"); v != "panel.acme.uz" {
		t.Fatalf("%q", v)
	}
	a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.panel.acme.uz.dns", Value: "ok"})
	a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.panel.acme.uz.cert", Value: "issued"})
	if _, b := call(t, srv, tok, "GET", "/api/settings/panel", ""); strings.Contains(string(b), "sslip.io") || !strings.Contains(string(b), `"host":"panel.acme.uz"`) {
		t.Fatalf("%s", b)
	}
	call(t, srv, tok, "PUT", "/api/settings/panel", `{"domain":""}`)
	if v, _ := a.St.Q.GetSetting(ctx, "panel.domain"); v != "" {
		t.Fatalf("not cleared: %q", v)
	}
}

func TestTokensAreMembersOwn(t *testing.T) {
	srv, tok, _, _, _ := setupSettings(t)
	dev := memberToken(t, srv, tok, "dev@acme.uz", "developer")
	if res, b := call(t, srv, dev, "POST", "/api/tokens", `{"name":"laptop"}`); res.StatusCode != 200 {
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	list := func(as string) map[string]TokenRow {
		res, b := call(t, srv, as, "GET", "/api/tokens", "")
		var rows []TokenRow
		if res.StatusCode != 200 || json.Unmarshal(b, &rows) != nil {
			t.Fatalf("list %d %s", res.StatusCode, b)
		}
		out := map[string]TokenRow{}
		for _, r := range rows {
			out[r.Name] = r
		}
		return out
	}
	all := list(tok)
	if len(all) != 3 || all["test"].Member != "admin@acme.uz" || all["laptop"].Member != "dev@acme.uz" {
		t.Fatalf("admin sees %+v", all)
	}
	mine := list(dev)
	if len(mine) != 2 || mine["laptop"].ID == "" || mine["developer-ci"].ID == "" {
		t.Fatalf("developer sees %+v", mine)
	}
	if res, _ := call(t, srv, dev, "DELETE", "/api/tokens/"+all["test"].ID, ""); res.StatusCode != 404 {
		t.Fatalf("deleting another's token: %d", res.StatusCode)
	}
	if res, _ := call(t, srv, dev, "DELETE", "/api/tokens/"+mine["laptop"].ID, ""); res.StatusCode != 204 {
		t.Fatalf("deleting one's own token: %d", res.StatusCode)
	}
	if len(list(tok)) != 2 {
		t.Fatal("the token was not deleted")
	}
}

const storageFull = `{"endpoint":"s3.a.com","bucket":"acme","region":"","access_key":"AKIAEXAMPLE1234","secret_key":"very-secret-key-value"}`

func TestSaveStorageKeepsKeysForSameEndpoint(t *testing.T) {
	srv, tok, a, v, _ := setupSettings(t)
	if res, b := call(t, srv, tok, "PUT", "/api/settings/storage", storageFull); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "PUT", "/api/settings/storage", `{"endpoint":"s3.a.com","bucket":"acme","region":"eu","access_key":"","secret_key":""}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if r, _ := a.St.Q.GetSetting(context.Background(), "storage.region"); r != "eu" {
		t.Fatalf("region %q", r)
	}
	ak, _ := vault.GetSecret(context.Background(), a.St.Q, v, "storage.access_key")
	sk, _ := vault.GetSecret(context.Background(), a.St.Q, v, "storage.secret_key")
	if string(ak) != "AKIAEXAMPLE1234" || string(sk) != "very-secret-key-value" {
		t.Fatalf("keys changed: %q %q", ak, sk)
	}
}

func TestSaveStorageRefusesKeptKeysOnNewEndpoint(t *testing.T) {
	srv, tok, a, _, _ := setupSettings(t)
	call(t, srv, tok, "PUT", "/api/settings/storage", storageFull)
	res, b := call(t, srv, tok, "PUT", "/api/settings/storage", `{"endpoint":"evil.example.com","bucket":"other","region":"eu","access_key":"","secret_key":""}`)
	if res.StatusCode != 400 || !strings.Contains(string(b), "Enter both keys again to change the endpoint.") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	ep, _ := a.St.Q.GetSetting(context.Background(), "storage.endpoint")
	bk, _ := a.St.Q.GetSetting(context.Background(), "storage.bucket")
	if ep != "s3.a.com" || bk != "acme" {
		t.Fatalf("changed: %q %q", ep, bk)
	}
}

func TestSaveStorageRefusesOneKey(t *testing.T) {
	srv, tok, _, _, _ := setupSettings(t)
	call(t, srv, tok, "PUT", "/api/settings/storage", storageFull)
	res, b := call(t, srv, tok, "PUT", "/api/settings/storage", `{"endpoint":"s3.a.com","bucket":"acme","access_key":"NEW","secret_key":""}`)
	if res.StatusCode != 400 || !strings.Contains(string(b), "Enter both keys, or leave both empty to keep them.") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	srv2, tok2, _, _, _ := setupSettings(t)
	res, b = call(t, srv2, tok2, "PUT", "/api/settings/storage", `{"endpoint":"s3.a.com","bucket":"acme","access_key":"","secret_key":""}`)
	if res.StatusCode != 400 || !strings.Contains(string(b), "all needed") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestTestEndpointsNeedSavedLogin(t *testing.T) {
	hits := 0
	far := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer far.Close()
	oldGitea, oldGitLab := gitea.APIBase, gitlab.APIBase
	gitea.APIBase = func(string) string { return far.URL }
	gitlab.APIBase = func(string) string { return far.URL }
	t.Cleanup(func() { gitea.APIBase, gitlab.APIBase = oldGitea, oldGitLab })
	srv, tok := setup(t)
	for path, want := range map[string]int{
		"/api/settings/storage/test":                                409,
		"/api/registries/" + far.Listener.Addr().String() + "/test": 404,
		"/api/settings/gitea/codeberg.org/test":                     404,
		"/api/settings/gitlab/gitlab.com/test":                      404,
	} {
		if res, b := call(t, srv, tok, "POST", path, ""); res.StatusCode != want {
			t.Errorf("%s: %d %s", path, res.StatusCode, b)
		}
	}
	if hits != 0 {
		t.Fatalf("%d requests went out", hits)
	}
}

func TestStorageTest(t *testing.T) {
	var status int
	var asked string
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path + "?" + r.URL.RawQuery
		w.WriteHeader(status)
		if status == 200 {
			w.Write([]byte(`<ListBucketResult><Name>acme</Name><IsTruncated>false</IsTruncated></ListBucketResult>`))
		}
	}))
	defer s3.Close()
	srv, tok := setup(t)
	body := `{"endpoint":"` + s3.URL + `","bucket":"acme","region":"us-east-1","access_key":"AKIAEXAMPLE1234","secret_key":"very-secret-key-value"}`
	if res, b := call(t, srv, tok, "PUT", "/api/settings/storage", body); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	status = 200
	if res, b := call(t, srv, tok, "POST", "/api/settings/storage/test", ""); res.StatusCode != 204 || !strings.Contains(asked, "/acme") || !strings.Contains(asked, "prefix=yawl-server%2F") {
		t.Fatalf("%d %s, asked %s", res.StatusCode, b, asked)
	}
	status = 403
	if res, b := call(t, srv, tok, "POST", "/api/settings/storage/test", ""); res.StatusCode != 502 || len(b) == 0 {
		t.Fatalf("refused: %d %s", res.StatusCode, b)
	}
}
