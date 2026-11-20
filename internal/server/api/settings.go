package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/notify"
	"github.com/behzodhalil/yawl/internal/server/selfbackup"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/spec"
)

func (h *handler) routesSettings(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/telegram", h.telegram)
	mux.HandleFunc("POST /api/settings/telegram", h.telegramStart)
	mux.HandleFunc("DELETE /api/settings/telegram", h.telegramUnlink)
	mux.HandleFunc("POST /api/settings/telegram/test", h.telegramTest)
	h.routesRelay(mux)
	mux.HandleFunc("GET /api/settings/storage", h.storage)
	mux.HandleFunc("PUT /api/settings/storage", h.setStorage)
	mux.HandleFunc("PUT /api/settings/storage/copies/{name}", h.setStorageCopy)
	mux.HandleFunc("DELETE /api/settings/storage/copies/{name}", h.deleteStorageCopy)
	mux.HandleFunc("POST /api/settings/storage/test", h.storageTest)
	mux.HandleFunc("GET /api/tokens", h.tokens)
	mux.HandleFunc("POST /api/tokens", h.createToken)
	mux.HandleFunc("DELETE /api/tokens/{id}", h.deleteToken)
	mux.HandleFunc("GET /api/recovery-key", h.recoveryStatus)
	mux.HandleFunc("GET /api/settings/server-backup", h.serverBackup)
	mux.HandleFunc("GET /api/settings/panel", h.panel)
	mux.HandleFunc("PUT /api/settings/panel", h.setPanel)
	mux.HandleFunc("POST /api/settings/server-backup", h.serverBackupNow)
	mux.HandleFunc("GET /api/registries", h.registries)
	mux.HandleFunc("PUT /api/registries/{host}", h.saveRegistry)
	mux.HandleFunc("DELETE /api/registries/{host}", h.removeRegistry)
	mux.HandleFunc("POST /api/registries/{host}/test", h.registryTest)
}

const linkTTL = 10 * time.Minute

func (h *handler) tgBase() string {
	if h.d.TelegramBase != "" {
		return h.d.TelegramBase
	}
	return notify.TelegramAPI
}

func (h *handler) setting(ctx context.Context, key string) string {
	v, _ := h.a.St.Q.GetSetting(ctx, key)
	return v
}

func (h *handler) put(ctx context.Context, kv ...string) error {
	for i := 0; i < len(kv); i += 2 {
		if err := h.a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: kv[i], Value: kv[i+1]}); err != nil {
			return err
		}
	}
	return nil
}

func (h *handler) drop(ctx context.Context, keys ...string) {
	for _, k := range keys {
		h.a.St.Q.DeleteSettingKey(ctx, k)
	}
}

var pendingKeys = []string{"telegram.pending_token", "telegram.pending_code", "telegram.pending_expires", "telegram.pending_bot", "telegram.pending_offset"}

func (h *handler) telegramStart(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token string }
	if !readJSON(w, r, &in) {
		return
	}
	in.Token = strings.TrimSpace(in.Token)
	bot, err := notify.Bot{Base: h.tgBase(), Token: in.Token}.Me(r.Context())
	if errors.Is(err, notify.ErrUnknownBot) || in.Token == "" {
		http.Error(w, notify.ErrUnknownBot.Error(), http.StatusBadRequest)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	code := fmt.Sprintf("yawl-%06d", n.Int64())
	ctx := r.Context()
	if err := vault.SetSecret(ctx, h.a.St.Q, h.d.Vault, "telegram.pending_token", []byte(in.Token)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	exp := strconv.FormatInt(h.a.Now().Add(linkTTL).UnixMilli(), 10)
	if err := h.put(ctx, "telegram.pending_code", code, "telegram.pending_expires", exp, "telegram.pending_bot", bot); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"state": "pending", "bot": bot, "code": code})
}

func (h *handler) telegram(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if code := h.setting(ctx, "telegram.pending_code"); code != "" {
		exp, _ := strconv.ParseInt(h.setting(ctx, "telegram.pending_expires"), 10, 64)
		token, err := vault.GetSecret(ctx, h.a.St.Q, h.d.Vault, "telegram.pending_token")
		if err != nil || h.a.Now().UnixMilli() >= exp {
			h.drop(ctx, pendingKeys...)
		} else {
			b := notify.Bot{Base: h.tgBase(), Token: string(token)}
			bot := h.setting(ctx, "telegram.pending_bot")
			offset, _ := strconv.ParseInt(h.setting(ctx, "telegram.pending_offset"), 10, 64)
			chat, found, next, err := b.FindChat(ctx, code, offset)
			if errors.Is(err, notify.ErrWebhook) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			} else if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			if !found {
				h.put(ctx, "telegram.pending_offset", strconv.FormatInt(next, 10))
				writeJSON(w, map[string]string{"state": "pending", "bot": bot, "code": code})
				return
			}
			if err := vault.SetSecret(ctx, h.a.St.Q, h.d.Vault, "telegram.token", token); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			h.put(ctx, "telegram.chat_id", chat.ID, "telegram.bot", bot, "telegram.chat_type", chat.Type, "telegram.chat_name", chat.Name, "telegram.linked_at", strconv.FormatInt(h.a.Now().UnixMilli(), 10))
			h.drop(ctx, pendingKeys...)
			h.dropRelayChats(ctx)
			notify.Telegram(h.tgBase(), string(token), chat.ID).Send(ctx, notify.Message{Text: "Yawl is linked. Messages about your apps will come here."})
		}
	}
	out := map[string]any{"state": "none", "relay": h.relayView(ctx)}
	if h.setting(ctx, "telegram.chat_id") != "" {
		out["state"], out["bot"], out["chat_type"], out["chat_name"], out["linked_at"] = "linked", h.setting(ctx, "telegram.bot"), h.setting(ctx, "telegram.chat_type"), h.setting(ctx, "telegram.chat_name"), h.setting(ctx, "telegram.linked_at")
	}
	writeJSON(w, out)
}

func (h *handler) telegramUnlink(w http.ResponseWriter, r *http.Request) {
	h.drop(r.Context(), append([]string{"telegram.token", "telegram.chat_id", "telegram.bot", "telegram.chat_type", "telegram.chat_name", "telegram.linked_at"}, pendingKeys...)...)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) telegramTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token, err := vault.GetSecret(ctx, h.a.St.Q, h.d.Vault, "telegram.token")
	chat := h.setting(ctx, "telegram.chat_id")
	if err != nil || chat == "" {
		http.Error(w, "Telegram is not linked.", http.StatusConflict)
		return
	}
	base := h.setting(ctx, "telegram.base_url")
	if base == "" {
		base = h.tgBase()
	}
	if err := notify.Telegram(base, string(token), chat).Send(ctx, notify.Message{Text: "Test message from Yawl."}); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type Storage struct {
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	Region    string `json:"region"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

func SaveStorage(ctx context.Context, q *db.Queries, v *vault.Vault, s Storage) error {
	if s.Endpoint == "" || s.Bucket == "" {
		return errors.New("Endpoint, bucket, access key and secret key are all needed.")
	}
	keep := false
	if s.AccessKey == "" || s.SecretKey == "" {
		_, e1 := vault.GetSecret(ctx, q, v, "storage.access_key")
		_, e2 := vault.GetSecret(ctx, q, v, "storage.secret_key")
		if e1 != nil || e2 != nil {
			return errors.New("Endpoint, bucket, access key and secret key are all needed.")
		}
		if cur, _ := q.GetSetting(ctx, "storage.endpoint"); s.Endpoint != cur {
			return errors.New("Enter both keys again to change the endpoint.")
		}
		if s.AccessKey != "" || s.SecretKey != "" {
			return errors.New("Enter both keys, or leave both empty to keep them.")
		}
		keep = true
	}
	for k, val := range map[string]string{"storage.endpoint": s.Endpoint, "storage.bucket": s.Bucket, "storage.region": s.Region} {
		if err := q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: val}); err != nil {
			return err
		}
	}
	if keep {
		return nil
	}
	for k, val := range map[string]string{"storage.access_key": s.AccessKey, "storage.secret_key": s.SecretKey} {
		if err := vault.SetSecret(ctx, q, v, k, []byte(val)); err != nil {
			return err
		}
	}
	return nil
}

func (h *handler) setStorage(w http.ResponseWriter, r *http.Request) {
	var in Storage
	if !readJSON(w, r, &in) {
		return
	}
	if err := SaveStorage(r.Context(), h.a.St.Q, h.d.Vault, in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) storage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ep := h.setting(ctx, "storage.endpoint")
	if ep == "" {
		writeJSON(w, map[string]any{"set": false, "copies": h.storageCopies(ctx)})
		return
	}
	end := ""
	if ak, err := vault.GetSecret(ctx, h.a.St.Q, h.d.Vault, "storage.access_key"); err == nil && len(ak) >= 4 {
		end = string(ak[len(ak)-4:])
	}
	writeJSON(w, map[string]any{"set": true, "endpoint": ep, "bucket": h.setting(ctx, "storage.bucket"),
		"region": h.setting(ctx, "storage.region"), "access_key_end": end, "copies": h.storageCopies(ctx)})
}

func (h *handler) storageTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ep, bucket := h.setting(ctx, "storage.endpoint"), h.setting(ctx, "storage.bucket")
	ak, err1 := vault.GetSecret(ctx, h.a.St.Q, h.d.Vault, "storage.access_key")
	sk, err2 := vault.GetSecret(ctx, h.a.St.Q, h.d.Vault, "storage.secret_key")
	if ep == "" || bucket == "" || errors.Join(err1, err2) != nil {
		http.Error(w, "Backup storage is not set.", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	s, err := selfbackup.S3(ep, bucket, h.setting(ctx, "storage.region"), string(ak), string(sk), false)
	if err == nil {
		_, err = s.List(ctx, selfbackup.Prefix)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type copyView struct {
	Name         string `json:"name"`
	Endpoint     string `json:"endpoint"`
	Bucket       string `json:"bucket"`
	Region       string `json:"region,omitempty"`
	AccessKeyEnd string `json:"access_key_end"`
}

func (h *handler) storageCopies(ctx context.Context) []copyView {
	out := []copyView{}
	rows, _ := h.a.St.Q.ListStorageCopies(ctx)
	for _, c := range rows {
		end := ""
		if ak, err := vault.GetSecret(ctx, h.a.St.Q, h.d.Vault, "storage.copy."+c.Name+".access_key"); err == nil && len(ak) >= 4 {
			end = string(ak[len(ak)-4:])
		}
		out = append(out, copyView{c.Name, c.Endpoint, c.Bucket, c.Region, end})
	}
	return out
}

var copyName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)

func (h *handler) setStorageCopy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !copyName.MatchString(name) {
		http.Error(w, "A copy's name is lowercase letters, digits and dashes, up to 20.", http.StatusBadRequest)
		return
	}
	var in Storage
	if !readJSON(w, r, &in) {
		return
	}
	if in.Endpoint == "" || in.Bucket == "" || in.AccessKey == "" || in.SecretKey == "" {
		http.Error(w, "Endpoint, bucket, access key and secret key are all needed.", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	rows, err := h.a.St.Q.ListStorageCopies(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !slices.ContainsFunc(rows, func(c db.StorageCopy) bool { return c.Name == name }) && len(rows) >= 3 {
		http.Error(w, "A backup goes to at most three copies.", http.StatusBadRequest)
		return
	}
	now := time.Now().UnixMilli()
	err = h.a.St.Tx(ctx, func(q *db.Queries) error {
		if old, err := q.GetStorageCopy(ctx, name); err == nil && (old.Endpoint != in.Endpoint || old.Bucket != in.Bucket) {
			if err := app.ForgetCopy(ctx, q, name, now); err != nil {
				return err
			}
		}
		if err := q.UpsertStorageCopy(ctx, db.UpsertStorageCopyParams{Name: name, Endpoint: in.Endpoint, Bucket: in.Bucket, Region: in.Region, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		for k, val := range map[string]string{"access_key": in.AccessKey, "secret_key": in.SecretKey} {
			if err := vault.SetSecret(ctx, q, h.d.Vault, "storage.copy."+name+"."+k, []byte(val)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) deleteStorageCopy(w http.ResponseWriter, r *http.Request) {
	ctx, name := r.Context(), r.PathValue("name")
	err := h.a.St.Tx(ctx, func(q *db.Queries) error {
		for _, f := range []func() error{
			func() error { return q.DeleteStorageCopy(ctx, name) },
			func() error { return app.ForgetCopy(ctx, q, name, time.Now().UnixMilli()) },
			func() error { return q.DeleteSettingKey(ctx, "storage.copy."+name+".access_key") },
			func() error { return q.DeleteSettingKey(ctx, "storage.copy."+name+".secret_key") },
		} {
			if err := f(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type TokenRow struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	LastUsedAt int64  `json:"last_used_at,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	Member     string `json:"member"`
}

func (h *handler) tokens(w http.ResponseWriter, r *http.Request) {
	ts, err := h.a.St.Q.ListAPITokens(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m, _ := member(r)
	out := []TokenRow{}
	for _, t := range ts {
		if m.Role == "admin" || t.OwnerID == m.ID && m.ID != "" {
			out = append(out, TokenRow{ID: t.ID, Name: t.Name, LastUsedAt: t.LastUsedAt.Int64, CreatedAt: t.CreatedAt, Member: t.Member})
		}
	}
	writeJSON(w, out)
}

func (h *handler) createToken(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 64 {
		http.Error(w, "Give the token a name of 1 to 64 characters.", http.StatusBadRequest)
		return
	}
	var tok string
	var err error
	if m, ok := member(r); ok && m.ID != "" {
		tok, err = h.a.CreateTokenFor(r.Context(), in.Name, m.ID)
	} else {
		tok, err = h.a.CreateToken(r.Context(), in.Name)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"token": tok})
}

func (h *handler) deleteToken(w http.ResponseWriter, r *http.Request) {
	m, _ := member(r)
	var n int64
	var err error
	if m.Role == "admin" {
		n, err = h.a.St.Q.DeleteAPIToken(r.Context(), r.PathValue("id"))
	} else if m.ID != "" {
		n, err = h.a.St.Q.DeleteOwnAPIToken(r.Context(), db.DeleteOwnAPITokenParams{ID: r.PathValue("id"), OwnerID: m.ID})
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if n == 0 {
		http.Error(w, "no such token", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) recoveryStatus(w http.ResponseWriter, r *http.Request) {
	pub := h.setting(r.Context(), "recovery.pubkey")
	if pub == "" {
		writeJSON(w, map[string]any{"set": false})
		return
	}
	writeJSON(w, map[string]any{"set": true, "fingerprint": pub[:8], "pubkey": pub})
}

func (h *handler) serverBackup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ok, _ := strconv.ParseInt(h.setting(ctx, "server_backup.last_ok"), 10, 64)
	writeJSON(w, map[string]any{"last_ok": ok, "last_key": h.setting(ctx, "server_backup.last_key"),
		"last_error": h.setting(ctx, "server_backup.last_error"), "at": h.setting(ctx, "server_backup.at")})
}

func (h *handler) serverBackupNow(w http.ResponseWriter, r *http.Request) {
	if h.d.RunSelfBackup == nil {
		http.Error(w, "The panel's own backup is not running in this process.", http.StatusConflict)
		return
	}
	if err := h.d.RunSelfBackup(r.Context()); err != nil {
		http.Error(w, "The panel's own backup failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) panel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, hosts := app.PanelHosts(ctx, h.a.St.Q, h.d.PublicURL)
	out := []DomainView{}
	for _, host := range hosts {
		get := func(k string) string { return h.setting(ctx, "panel."+host+"."+k) }
		dns, cert := get("dns"), get("cert")
		if dns == "" {
			dns = "pending"
		}
		if cert == "" {
			cert = "pending"
		}
		out = append(out, DomainView{Host: host, DNSState: dns, DNSDetail: get("dns_detail"), CertState: cert})
	}
	writeJSON(w, map[string]any{"domain": h.setting(ctx, "panel.domain"), "hosts": out})
}

func (h *handler) setPanel(w http.ResponseWriter, r *http.Request) {
	var in struct{ Domain string }
	if !readJSON(w, r, &in) {
		return
	}
	if in.Domain != "" && !spec.ValidHost(in.Domain) {
		http.Error(w, "Give a lowercase host name like panel.acme.uz. sslip.io names are Yawl's own.", http.StatusBadRequest)
		return
	}
	if in.Domain != "" {
		ds, err := h.a.St.Q.DomainsOnHost(r.Context(), in.Domain)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sp, err := h.a.St.Q.StatusPagesOnHost(r.Context(), in.Domain)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(ds) > 0 || sp > 0 {
			http.Error(w, in.Domain+" already points to a service or a status page.", http.StatusBadRequest)
			return
		}
	}
	if err := h.put(r.Context(), "panel.domain", in.Domain); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
