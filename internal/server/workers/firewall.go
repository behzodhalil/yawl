package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type Firewall struct {
	St  *store.Store
	GW  Sender
	App *app.App
	Now func() time.Time
}

func (w *Firewall) Tick(ctx context.Context) error {
	if app.Adopting(ctx, w.St.Q) {
		return nil
	}
	servers, err := w.St.Q.ListServers(ctx)
	if err != nil {
		return err
	}
	for _, s := range servers {
		if s.Offline == 1 {
			continue
		}
		if _, err := w.St.Q.GetSetting(ctx, "firewall."+s.ID); err != nil {
			continue
		}
		if t, ok := w.App.FirewallTrialOf(ctx, s.ID); ok {
			if t.UntilMs > w.Now().UnixMilli() {
				continue
			}
			w.App.DropFirewallTrial(ctx, s.ID)
		}
		if err := w.server(ctx, s); err != nil {
			slog.Warn("firewall", "server", s.Name, "err", err)
		}
	}
	return nil
}

func (w *Firewall) send(ctx context.Context, serverID, name string, args any) (proto.Result, error) {
	cmd, _ := proto.NewCommand(fmt.Sprintf("fw:%s:%d", serverID, w.Now().UnixNano()), name, args)
	tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := w.GW.Send(tctx, serverID, cmd)
	if err == nil && !res.OK {
		err = fmt.Errorf("%s", res.Cause)
	}
	return res, err
}

func (w *Firewall) server(ctx context.Context, s db.Server) error {
	rules, err := w.App.FirewallRulesOf(ctx, s.ID)
	if err != nil {
		return err
	}
	args, err := w.App.FirewallArgs(ctx, s.ID, rules)
	if err != nil {
		return err
	}
	res, err := w.send(ctx, s.ID, proto.CmdFirewallStatus, struct{}{})
	if err != nil {
		return err
	}
	var st proto.FirewallStatus
	json.Unmarshal(res.Data, &st)
	if st.Version != args.Version {
		if _, err := w.send(ctx, s.ID, proto.CmdFirewall, args); err != nil {
			return err
		}
	}
	return w.bans(ctx, s, st.Bans)
}

func (w *Firewall) bans(ctx context.Context, s db.Server, bans []proto.FwBan) error {
	key := "firewall." + s.ID + ".bans"
	var seen []string
	if v, err := w.St.Q.GetSetting(ctx, key); err == nil {
		json.Unmarshal([]byte(v), &seen)
	}
	now := []string{}
	tg, _ := w.St.Q.GetSetting(ctx, app.FirewallTelegramKey)
	for _, b := range bans {
		now = append(now, b.IP)
		if slices.Contains(seen, b.IP) {
			continue
		}
		text := fmt.Sprintf("banned %s for SSH on %s", b.IP, s.Name)
		at := w.Now().UnixMilli()
		var err error
		if tg == "on" {
			err = app.Announce(ctx, w.St.Q, at, "audit", "firewall.ban", "firewall", app.Banned(s.Name, b.IP, text))
		} else {
			err = app.Event(ctx, w.St.Q, at, "audit", "firewall.ban", "firewall", map[string]string{"text": text})
		}
		if err != nil {
			return err
		}
	}
	b, _ := json.Marshal(now)
	return w.St.Q.SetSetting(ctx, db.SetSettingParams{Key: key, Value: string(b)})
}
