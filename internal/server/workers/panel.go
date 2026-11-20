package workers

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

// Panel routes the panel's own hosts through the agent on its machine, the
// same DNS-then-certificate path as a service's domain (§12). Routes live in
// Caddy, which an agent or Caddy restart can lose, so even a finished host is
// re-ensured hourly.
type Panel struct {
	St        *store.Store
	GW        Sender
	Upstream  string
	PublicURL string
	Now       func() time.Time
}

const panelHeal = time.Hour

func (w *Panel) Tick(ctx context.Context) error {
	ip, hosts := app.PanelHosts(ctx, w.St.Q, w.PublicURL)
	serverID, _ := w.St.Q.GetSetting(ctx, "panel.server_id")
	now := w.Now()
	if free := app.PanelFreeHost(ip); ip != "" {
		w.dropFree(ctx, serverID, free, slices.Contains(hosts, free), now)
	}
	for _, h := range hosts {
		get := func(k string) string { v, _ := w.St.Q.GetSetting(ctx, "panel."+h+"."+k); return v }
		tried, _ := strconv.ParseInt(get("tried"), 10, 64)
		wait := time.Duration(0)
		switch {
		case get("dns") == "ok" && get("cert") == "issued":
			wait = panelHeal
		case get("dns") == "ok":
			wait = certRetry
		}
		if tried > 0 && now.Sub(time.UnixMilli(tried)) < wait {
			continue
		}
		args, _ := json.Marshal(proto.EnsureDomainArgs{Host: h, ExpectIP: ip, Upstream: w.Upstream})
		tctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		res, err := w.GW.Send(tctx, serverID, proto.Command{Key: "panel:" + h + ":" + strconv.FormatInt(now.UnixMilli(), 10), Name: proto.CmdEnsureDomain, Args: args})
		cancel()
		set := func(k, v string) { w.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel." + h + "." + k, Value: v}) }
		set("tried", strconv.FormatInt(now.UnixMilli(), 10))
		if err != nil || res.Cause != "" {
			continue
		}
		var d proto.EnsureDomainData
		json.Unmarshal(res.Data, &d)
		set("dns", d.DNS)
		set("dns_detail", d.DNSDetail)
		set("cert", d.Cert)
	}
	return nil
}

func (w *Panel) dropFree(ctx context.Context, serverID, free string, served bool, now time.Time) {
	key := "panel." + free + ".removed"
	if served {
		w.St.Q.DeleteSettingKey(ctx, key)
		return
	}
	if v, _ := w.St.Q.GetSetting(ctx, key); v == "1" {
		return
	}
	args, _ := json.Marshal(proto.RemoveRouteArgs{Host: free})
	tctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	res, err := w.GW.Send(tctx, serverID, proto.Command{Key: "panel-unroute:" + free + ":" + strconv.FormatInt(now.UnixMilli(), 10), Name: proto.CmdRemoveRoute, Args: args})
	if err != nil || res.Cause != "" {
		return
	}
	w.St.Q.SetSetting(ctx, db.SetSettingParams{Key: key, Value: "1"})
	w.St.Q.DeleteSettingKey(ctx, "panel."+free+".tried")
}
