package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/statuspage"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/server/watch"
)

type StatusPages struct {
	St   *store.Store
	GW   Sender
	V    *vault.Vault
	HTTP *http.Client
	Now  func() time.Time
}

var outsideWait = 5 * time.Second

func (w *StatusPages) Tick(ctx context.Context) error {
	pages, err := statuspage.Pages(ctx, w.St.DB)
	if err != nil {
		return err
	}
	now := w.Now()
	if len(pages) > 0 {
		world, err := statuspage.Load(ctx, w.St.DB, w.outside(ctx), now)
		if err != nil {
			return err
		}
		for _, p := range pages {
			if err := w.page(ctx, world, p, now); err != nil {
				return err
			}
		}
	}
	return w.routes(ctx, pages, now)
}

func (w *StatusPages) outside(ctx context.Context) []statuspage.Outside {
	if w.V == nil {
		return nil
	}
	c, ok := watch.ClientFromSettings(ctx, w.St.Q, w.V)
	if !ok {
		return nil
	}
	if w.HTTP != nil {
		c.HTTP = w.HTTP
	}
	cctx, cancel := context.WithTimeout(ctx, outsideWait)
	defer cancel()
	raw, err := c.Incidents(cctx, statuspage.Days+1)
	var out []statuspage.Outside
	if err != nil || json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

func (w *StatusPages) page(ctx context.Context, world statuspage.World, p statuspage.PageRec, now time.Time) error {
	db := w.St.DB
	ms := now.UnixMilli()
	incs, err := statuspage.Incidents(ctx, db, p.ID, ms-14*24*int64(time.Hour/time.Millisecond))
	if err != nil {
		return err
	}
	pg := world.Page(p, incs, now)
	up := map[string]bool{}
	for _, r := range pg.Rows {
		up[r.ID] = !slices.ContainsFunc(r.Members, func(m statuspage.Member) bool { return m.DownNow })
		if statuspage.RowState(r, pg.Incidents, ms) != statuspage.Down || told(incs, r.ID) {
			continue
		}
		i := statuspage.IncidentRec{ID: store.NewID("si_"), PageID: p.ID, Kind: statuspage.KindIncident, Title: r.Name + " is down",
			Rows: []string{r.ID}, State: statuspage.IncidentDraft, Auto: true, CreatedAt: ms}
		if err := statuspage.InsertIncident(ctx, db, i, "We are looking into it.", store.NewID("su_"), ms); err != nil {
			return err
		}
	}
	if err := statuspage.DropAutoDrafts(ctx, db, p.ID, up); err != nil {
		return err
	}
	args, _ := json.Marshal(proto.StatusPageArgs{ID: p.ID, HTML: string(statuspage.Render(pg))})
	sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	w.GW.Send(sctx, p.ServerID, proto.Command{Key: "status:" + p.ID + ":" + strconv.FormatInt(ms, 10), Name: proto.CmdStatusPage, Args: args})
	return nil
}

func told(incs []statuspage.IncidentRec, row string) bool {
	return slices.ContainsFunc(incs, func(i statuspage.IncidentRec) bool {
		return i.Kind == statuspage.KindIncident && i.ResolvedAt == 0 && slices.Contains(i.Rows, row)
	})
}

func (w *StatusPages) routes(ctx context.Context, pages []statuspage.PageRec, now time.Time) error {
	db := w.St.DB
	have, err := statuspage.Routes(ctx, db)
	if err != nil {
		return err
	}
	type key struct{ host, server string }
	want := map[key]statuspage.PageRec{}
	ips := map[string]string{}
	for _, p := range pages {
		srv, err := w.St.Q.GetServer(ctx, p.ServerID)
		if err != nil {
			continue
		}
		ip, err := netip.ParseAddr(srv.PublicIp)
		if err != nil {
			continue
		}
		ips[p.ServerID] = ip.String()
		want[key{statuspage.FreeHost(p.Slug, ip), p.ServerID}] = p
		if p.Domain != "" {
			want[key{p.Domain, p.ServerID}] = p
		}
	}
	ms := now.UnixMilli()
	send := func(server, name string, args any) (proto.Result, error) {
		b, _ := json.Marshal(args)
		tctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		return w.GW.Send(tctx, server, proto.Command{Key: "status-" + name + ":" + strconv.FormatInt(ms, 10), Name: name, Args: b})
	}
	known := map[key]statuspage.Route{}
	for _, r := range have {
		k := key{r.Host, r.ServerID}
		if _, ok := want[k]; ok {
			known[k] = r
			continue
		}
		if res, err := send(r.ServerID, proto.CmdRemoveRoute, proto.RemoveRouteArgs{Host: r.Host}); err != nil || res.Cause != "" {
			continue
		}
		kept := false
		for k2, p := range want {
			kept = kept || (p.ID == r.PageID && k2.server == r.ServerID)
		}
		if !kept {
			if res, err := send(r.ServerID, proto.CmdStatusPage, proto.StatusPageArgs{ID: r.PageID}); err != nil || res.Cause != "" {
				continue
			}
		}
		if err := statuspage.DeleteRoute(ctx, db, r.Host, r.ServerID); err != nil {
			return err
		}
	}
	for k, p := range want {
		r, ok := known[k]
		wait := time.Duration(0)
		switch {
		case ok && r.PageID != p.ID:
		case r.DNS == "ok" && r.Cert == "issued":
			wait = panelHeal
		case r.DNS == "ok":
			wait = certRetry
		}
		if r.TriedAt > 0 && now.Sub(time.UnixMilli(r.TriedAt)) < wait {
			continue
		}
		r = statuspage.Route{Host: k.host, ServerID: k.server, PageID: p.ID, DNS: r.DNS, DNSDetail: r.DNSDetail, Cert: r.Cert, TriedAt: ms}
		res, err := send(k.server, proto.CmdEnsureDomain, proto.EnsureDomainArgs{Host: k.host, ExpectIP: ips[k.server], Options: proto.RouteOptions{Status: p.ID}})
		if err == nil && res.Cause == "" {
			var d proto.EnsureDomainData
			json.Unmarshal(res.Data, &d)
			r.DNS, r.DNSDetail, r.Cert = d.DNS, d.DNSDetail, d.Cert
		} else if err == nil {
			slog.Warn("status page route", "host", k.host, "cause", res.Cause)
		}
		if err := statuspage.SaveRoute(ctx, db, r); err != nil {
			return err
		}
	}
	return nil
}
