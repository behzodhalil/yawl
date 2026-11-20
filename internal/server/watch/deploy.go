package watch

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	worker "github.com/behzodhalil/yawl/watch"

	"github.com/behzodhalil/yawl/internal/server/notify"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

var ErrNoTelegram = errors.New("Link Telegram first: the watcher sends its alerts there.")

var hexs = hex.EncodeToString

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

func PanelID(ctx context.Context, st *store.Store) string {
	id := strings.TrimPrefix(st.InstanceID(ctx), "ins_")
	if len(id) > 8 {
		id = id[:8]
	}
	return id
}

func Name(pid string) string { return "yawl-watch-" + pid }

func Fingerprint(code, key []byte, send ...map[string]string) string {
	h := sha256.New()
	parts := [][]byte{code, key}
	for _, m := range send {
		for _, k := range sortedKeys(m) {
			parts = append(parts, []byte(k), []byte(m[k]))
		}
	}
	for _, p := range parts {
		binary.Write(h, binary.BigEndian, uint64(len(p)))
		h.Write(p)
	}
	return hexs(h.Sum(nil))
}

func Key(ctx context.Context, q *db.Queries, v *vault.Vault) ([]byte, error) {
	k, err := vault.GetSecret(ctx, q, v, "watch.key")
	if err == nil && len(k) == 32 {
		return k, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	k = make([]byte, 32)
	rand.Read(k)
	return k, vault.SetSecret(ctx, q, v, "watch.key", k)
}

func Relay(ctx context.Context, q *db.Queries, v *vault.Vault) (r notify.Relay, chats []notify.RelayChat, ok bool) {
	u, _ := q.GetSetting(ctx, "telegram.relay_url")
	if u == "" {
		u = notify.RelayURL
	}
	key, err := vault.GetSecret(ctx, q, v, "telegram.relay_key")
	cs, _ := q.GetSetting(ctx, "telegram.relay_chats")
	json.Unmarshal([]byte(cs), &chats)
	return notify.Relay{URL: u, Key: string(key)}, chats, err == nil && len(key) > 0
}

func Live(chats []notify.RelayChat) int {
	n := 0
	for _, c := range chats {
		if c.GoneAt == 0 {
			n++
		}
	}
	return n
}

func Routes(ctx context.Context, q *db.Queries) (map[string]notify.Route, error) {
	out := map[string]notify.Route{}
	s, _ := q.GetSetting(ctx, "telegram.relay_routes")
	if s == "" {
		return out, nil
	}
	json.Unmarshal([]byte(s), &out)
	servers, projects, err := Known(ctx, q)
	if err != nil {
		return nil, err
	}
	for id, r := range out {
		out[id] = r.Known(servers, projects)
	}
	return out, nil
}

func Known(ctx context.Context, q *db.Queries) (servers, projects map[string]bool, err error) {
	ss, err := q.ListServers(ctx)
	if err != nil {
		return nil, nil, err
	}
	es, err := q.ListEnvironmentTree(ctx)
	if err != nil {
		return nil, nil, err
	}
	servers, projects = map[string]bool{}, map[string]bool{}
	for _, s := range ss {
		servers[s.ID] = true
	}
	for _, e := range es {
		projects[e.Project] = true
	}
	return servers, projects, nil
}

func Chats(ctx context.Context, q *db.Queries, v *vault.Vault, list []Expectation) (map[string][]string, error) {
	out := map[string][]string{}
	_, chats, ok := Relay(ctx, q, v)
	if !ok || Live(chats) == 0 {
		return out, nil
	}
	routes, err := Routes(ctx, q)
	if err != nil {
		return nil, err
	}
	for _, e := range list {
		if to, all := notify.RouteTo(chats, routes, e.Server, e.Project); !all && e.Kind != ExpectPanel {
			out[e.Subject] = append([]string{}, to...)
		}
	}
	return out, nil
}

func Telegram(ctx context.Context, q *db.Queries, v *vault.Vault) (secrets, vars map[string]string, err error) {
	if r, chats, ok := Relay(ctx, q, v); ok && Live(chats) > 0 {
		return map[string]string{"RELAY_KEY": r.Key}, map[string]string{"RELAY_URL": strings.TrimRight(r.URL, "/")}, nil
	}
	token, err := vault.GetSecret(ctx, q, v, "telegram.token")
	chat, _ := q.GetSetting(ctx, "telegram.chat_id")
	if err != nil || chat == "" || len(token) == 0 {
		return nil, nil, ErrNoTelegram
	}
	return map[string]string{"BOT_TOKEN": string(token), "CHAT_ID": chat}, map[string]string{}, nil
}

var deploying sync.Mutex

func Deploy(ctx context.Context, st *store.Store, v *vault.Vault, cf Cloudflare, code []byte) (string, error) {
	deploying.Lock()
	defer deploying.Unlock()
	url, err := deploy(ctx, st, v, cf, code)
	if err != nil {
		st.Q.SetSetting(ctx, db.SetSettingParams{Key: "watch.error", Value: err.Error()})
		return "", err
	}
	st.Q.DeleteSettingKey(ctx, "watch.error")
	return url, nil
}

func deploy(ctx context.Context, st *store.Store, v *vault.Vault, cf Cloudflare, code []byte) (string, error) {
	q := st.Q
	secrets, vars, err := Telegram(ctx, q, v)
	if err != nil {
		return "", err
	}
	key, err := Key(ctx, q, v)
	if err != nil {
		return "", err
	}
	tg, _ := q.GetSetting(ctx, "telegram.base_url")
	if tg == "" {
		tg = notify.TelegramAPI
	}
	pid := PanelID(ctx, st)
	name := Name(pid)
	step := func(s string, err error) error {
		if err == nil {
			return nil
		}
		return &StepError{Step: s, Msg: err.Error()}
	}
	if err := step("check the token", cf.Verify(ctx)); err != nil {
		return "", err
	}
	dbID, err := cf.EnsureD1(ctx, name)
	if err := step("create the database", err); err != nil {
		return "", err
	}
	if err := step("set up the database", cf.Query(ctx, dbID, worker.Schema())); err != nil {
		return "", err
	}
	fp := Fingerprint(code, key, secrets, vars)
	secrets["PANEL_KEY"], vars["TELEGRAM_API"] = hexs(key), tg
	if err := step("upload the watcher", cf.PutScript(ctx, name, code, dbID, secrets, vars)); err != nil {
		return "", err
	}
	if err := step("set its schedule", cf.PutSchedule(ctx, name)); err != nil {
		return "", err
	}
	sub, err := cf.EnsureSubdomain(ctx, "yawl-"+pid)
	if err == nil {
		err = cf.EnableSubdomain(ctx, name)
	}
	if err := step("turn on workers.dev", err); err != nil {
		return "", err
	}
	url := "https://" + name + "." + sub + ".workers.dev"
	for k, val := range map[string]string{"watch.db_id": dbID, "watch.url": url, "watch.deployed": fp} {
		if err := q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: val}); err != nil {
			return "", err
		}
	}
	return url, nil
}

func FromSettings(ctx context.Context, q *db.Queries, v *vault.Vault, base string) (Cloudflare, bool) {
	tok, err := vault.GetSecret(ctx, q, v, "watch.cf_token")
	acct, _ := q.GetSetting(ctx, "watch.account")
	if err != nil || acct == "" {
		return Cloudflare{}, false
	}
	if base == "" {
		base, _ = q.GetSetting(ctx, "watch.api_url")
	}
	if base == "" {
		base = DefaultAPI
	}
	return Cloudflare{API: base, Token: string(tok), Account: acct}, true
}

func Forget(ctx context.Context, q *db.Queries) {
	for _, k := range []string{"watch.cf_token", "watch.account", "watch.db_id", "watch.url", "watch.deployed",
		"watch.expect_sha", "watch.ack_at", "watch.wait_since", "watch.error", "watch.down_sent"} {
		q.DeleteSettingKey(ctx, k)
	}
}
