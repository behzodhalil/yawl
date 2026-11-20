package workers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/notify"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/server/watch"
)

type Notifier = notify.Notifier

type Outbox struct {
	St        *store.Store
	Notifier  func(ctx context.Context) (Notifier, error)
	Watch     func(ctx context.Context) (watch.Client, bool)
	Extras    func(ctx context.Context) (map[string]Notifier, error)
	PublicURL string
}

const giveUpAfter = 24 * time.Hour

func (o *Outbox) Tick(ctx context.Context, now time.Time) error {
	rows, err := o.St.Q.DueOutbox(ctx, now.UnixMilli())
	if err != nil || len(rows) == 0 {
		return err
	}
	var wc watch.Client
	var watched bool
	if o.Watch != nil {
		wc, watched = o.Watch(ctx)
	}
	answering := watched && watch.Answering(ctx, o.St.Q, now.UnixMilli())
	n, nerr := o.Notifier(ctx)
	extras := map[string]Notifier{}
	if o.Extras != nil {
		if extras, err = o.Extras(ctx); err != nil {
			return err
		}
	}
	names := slices.Sorted(maps.Keys(extras))
	base := o.PublicURL
	if d, _ := o.St.Q.GetSetting(ctx, "panel.domain"); d != "" {
		base = "https://" + d
	}
	var chats []notify.RelayChat
	var routes map[string]notify.Route
	if _, ok := n.(notify.Relay); ok {
		cs, _ := o.St.Q.GetSetting(ctx, "telegram.relay_chats")
		json.Unmarshal([]byte(cs), &chats)
		if routes, err = watch.Routes(ctx, o.St.Q); err != nil {
			return err
		}
	}
	down := map[string]error{}
	for _, row := range rows {
		ms := now.UnixMilli()
		m := notify.Message{Text: row.Body}
		if row.MessageJson != "" {
			json.Unmarshal([]byte(row.MessageJson), &m)
		}
		m = m.Linked(base)
		sent := map[string]bool{}
		for _, c := range strings.Fields(row.SentChannels) {
			sent[c] = true
		}
		var extraErr error
		if row.Quiet == 0 {
			for _, name := range names {
				if sent[name] {
					continue
				}
				if e := down[name]; e != nil {
					extraErr = e
					continue
				}
				if e := extras[name].Send(ctx, m); e != nil {
					extraErr = fmt.Errorf("%s: %w", name, e)
					down[name] = extraErr
				} else {
					sent[name] = true
				}
			}
		}
		extrasDone := row.Quiet == 1 || !slices.ContainsFunc(names, func(c string) bool { return !sent[c] })
		saved := len(sent)
		save := func() error {
			if len(sent) == saved {
				return nil
			}
			saved = len(sent)
			return o.St.Q.SetOutboxChannels(ctx, db.SetOutboxChannelsParams{SentChannels: strings.Join(slices.Sorted(maps.Keys(sent)), " "), UpdatedAt: ms, ID: row.ID})
		}
		if err := save(); err != nil {
			return err
		}
		finish := func(err error) {
			if err == nil && extrasDone {
				o.St.Q.MarkOutboxSent(ctx, db.MarkOutboxSentParams{SentAt: sql.NullInt64{Int64: ms, Valid: true}, UpdatedAt: ms, ID: row.ID})
				return
			}
			if err == nil {
				err = extraErr
			}
			if now.Sub(time.UnixMilli(row.CreatedAt)) >= giveUpAfter {
				o.St.Q.MarkOutboxFailed(ctx, db.MarkOutboxFailedParams{FailedAt: sql.NullInt64{Int64: ms, Valid: true}, LastError: err.Error(), UpdatedAt: ms, ID: row.ID})
				return
			}
			attempts := row.Attempts + 1
			wait := min(time.Minute<<(attempts-1), time.Hour)
			o.St.Q.MarkOutboxRetry(ctx, db.MarkOutboxRetryParams{Attempts: attempts, NextAttemptAt: now.Add(wait).UnixMilli(), LastError: err.Error(), UpdatedAt: ms, ID: row.ID})
		}
		if sent["telegram"] {
			finish(nil)
			continue
		}
		var err error
		switch {
		case row.Incident != "" && answering:
			var tg map[string]any
			if m.State != "" {
				tg = notify.TelegramPayload(m, now)
			}
			err = wc.Incident(ctx, row.Incident, row.Action, row.Label, row.Body, tg, row.Owed == 1, o.chatsFor(ctx, chats, routes, m))
		case row.Incident != "" && row.Action == "close" && watched:
			if now.Sub(time.UnixMilli(row.CreatedAt)) >= giveUpAfter {
				o.St.Q.MarkOutboxFailed(ctx, db.MarkOutboxFailedParams{FailedAt: sql.NullInt64{Int64: ms, Valid: true}, LastError: "the watcher did not answer for 24 hours", UpdatedAt: ms, ID: row.ID})
				continue
			}
			owed := row.Owed
			if row.Quiet == 0 && owed == 0 && n != nil {
				serr, err := o.tell(ctx, n, chats, routes, m, sent, save)
				if err != nil {
					return err
				}
				if serr == nil {
					owed = 1
				}
			}
			o.St.Q.MarkOutboxOwed(ctx, db.MarkOutboxOwedParams{Owed: owed, NextAttemptAt: now.Add(time.Minute).UnixMilli(), UpdatedAt: ms, ID: row.ID})
			continue
		case row.Quiet == 1:
		case nerr != nil:
			return nerr
		case n == nil:
			if len(names) > 0 {
				finish(nil)
			}
			continue
		default:
			var serr error
			if serr, err = o.tell(ctx, n, chats, routes, m, sent, save); err != nil {
				return err
			}
			err = serr
		}
		if err == nil {
			sent["telegram"] = true
			if err := save(); err != nil {
				return err
			}
		}
		finish(err)
	}
	return nil
}

func (o *Outbox) tell(ctx context.Context, n Notifier, chats []notify.RelayChat, routes map[string]notify.Route, m notify.Message, sent map[string]bool, save func() error) (serr, err error) {
	rl, ok := n.(notify.Relay)
	partial := slices.ContainsFunc(slices.Collect(maps.Keys(sent)), func(k string) bool { return strings.HasPrefix(k, "relay:") })
	routed := slices.ContainsFunc(slices.Collect(maps.Values(routes)), func(r notify.Route) bool { return len(r.Servers)+len(r.Projects) > 0 })
	if !ok || !routed && !partial {
		return n.Send(ctx, m), nil
	}
	to, all := notify.RouteTo(chats, routes, serverOf(ctx, o.St.Q, m), m.Project)
	if all && !partial {
		return n.Send(ctx, m), nil
	}
	for _, id := range to {
		if sent["relay:"+id] {
			continue
		}
		rl.Chat = id
		if serr := rl.Send(ctx, m); serr != nil {
			return serr, nil
		}
		sent["relay:"+id] = true
		if err := save(); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (o *Outbox) chatsFor(ctx context.Context, chats []notify.RelayChat, routes map[string]notify.Route, m notify.Message) []string {
	if len(chats) == 0 {
		return nil
	}
	to, all := notify.RouteTo(chats, routes, serverOf(ctx, o.St.Q, m), m.Project)
	if all {
		return nil
	}
	return append([]string{}, to...)
}

func serverOf(ctx context.Context, q *db.Queries, m notify.Message) string {
	if m.Server != "" {
		if s, err := q.GetServerByName(ctx, m.Server); err == nil {
			return s.ID
		}
	} else if m.Env != "" {
		if e, err := q.GetEnvironmentByName(ctx, db.GetEnvironmentByNameParams{Project: m.Project, Env: m.Env}); err == nil {
			return e.ServerID
		}
	}
	return ""
}

func TelegramFromSettings(st *store.Store, v *vault.Vault) func(context.Context) (Notifier, error) {
	return func(ctx context.Context) (Notifier, error) {
		if r, chats, ok := watch.Relay(ctx, st.Q, v); ok && watch.Live(chats) > 0 {
			return r, nil
		}
		token, err1 := vault.GetSecret(ctx, st.Q, v, "telegram.token")
		chat, err2 := st.Q.GetSetting(ctx, "telegram.chat_id")
		if err1 != nil || err2 != nil {
			return nil, nil
		}
		base, err := st.Q.GetSetting(ctx, "telegram.base_url")
		if err != nil {
			base = notify.TelegramAPI
		}
		return notify.Telegram(base, string(token), chat), nil
	}
}

func ExtrasFromSettings(st *store.Store, v *vault.Vault) func(context.Context) (map[string]Notifier, error) {
	return func(ctx context.Context) (map[string]Notifier, error) {
		out := map[string]Notifier{}
		if u, err := vault.GetSecret(ctx, st.Q, v, "notify.slack.url"); err == nil {
			out["slack"] = notify.Slack(string(u))
		}
		if u, err := vault.GetSecret(ctx, st.Q, v, "notify.discord.url"); err == nil {
			out["discord"] = notify.Discord(string(u))
		}
		if u, err := vault.GetSecret(ctx, st.Q, v, "notify.webhook.url"); err == nil {
			out["webhook"] = notify.Webhook(string(u))
		}
		if u, err := vault.GetSecret(ctx, st.Q, v, "notify.ntfy.url"); err == nil {
			out["ntfy"] = notify.Ntfy(string(u))
		}
		if c, ok := EmailSettings(ctx, st, v); ok {
			out["email"] = notify.Email(c)
		}
		return out, nil
	}
}

func EmailSettings(ctx context.Context, st *store.Store, v *vault.Vault) (notify.EmailConfig, bool) {
	get := func(k string) string { s, _ := st.Q.GetSetting(ctx, "notify.email."+k); return s }
	c := notify.EmailConfig{Host: get("host"), Port: get("port"), Username: get("username"), From: get("from"), To: get("to")}
	if pw, err := vault.GetSecret(ctx, st.Q, v, "notify.email.password"); err == nil {
		c.Password = string(pw)
	}
	return c, c.Host != "" && c.To != ""
}
