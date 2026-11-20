package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type Domains struct {
	St  *store.Store
	GW  Sender
	Now func() time.Time

	mu    sync.Mutex
	tried map[string]time.Time
}

const certRetry = 10 * time.Minute

func (w *Domains) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Domains) Tick(ctx context.Context) error {
	if app.Adopting(ctx, w.St.Q) {
		return nil
	}
	if err := w.removals(ctx); err != nil {
		return err
	}
	ds, err := w.St.Q.PendingDomains(ctx)
	if err != nil {
		return err
	}
	w.mu.Lock()
	if w.tried == nil {
		w.tried = map[string]time.Time{}
	}
	w.mu.Unlock()
	for _, d := range ds {
		now := w.now()
		w.mu.Lock()
		last, seen := w.tried[d.ID]
		w.mu.Unlock()
		if d.DnsState == "ok" && seen && now.Sub(last) < certRetry {
			continue
		}
		w.mu.Lock()
		w.tried[d.ID] = now
		w.mu.Unlock()
		container, err := w.liveContainer(ctx, d.ServiceID)
		if err != nil || container == "" {
			continue
		}
		var rows []domainRow
		if ds, err := w.St.Q.ListDomains(ctx, d.ServiceID); err == nil {
			for _, x := range ds {
				rows = append(rows, domainRow{x.Host, x.DnsState})
			}
		}
		hosts := reachable(rows, d.Host)
		opts, err := RouteOptions(ctx, w.St.Q, d.ServiceID, hosts)
		if err != nil {
			continue
		}
		cmd, _ := proto.NewCommand(fmt.Sprintf("domain:%s:%d", d.ID, now.UnixMilli()), proto.CmdEnsureDomain,
			proto.EnsureDomainArgs{Host: d.Host, ExpectIP: d.PublicIp, Container: container, Port: int(d.Port), Options: opts})
		sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		res, err := w.GW.Send(sctx, d.ServerID, cmd)
		cancel()
		if err != nil || !res.OK {
			continue
		}
		var data proto.EnsureDomainData
		if json.Unmarshal(res.Data, &data) != nil {
			continue
		}
		if data.DNS == d.DnsState && data.Cert == d.CertState {
			continue
		}
		ms := now.UnixMilli()
		if err := w.St.Tx(ctx, func(q *db.Queries) error {
			if err := q.SetDomainState(ctx, db.SetDomainStateParams{DnsState: data.DNS, DnsDetail: data.DNSDetail, CertState: data.Cert, UpdatedAt: ms, ID: d.ID}); err != nil {
				return err
			}
			return app.Event(ctx, q, ms, "domain.state", d.ID, "system", map[string]string{"host": d.Host, "dns": data.DNS, "cert": data.Cert, "detail": data.DNSDetail})
		}); err != nil {
			return err
		}
		if data.DNS == "ok" && d.DnsState != "ok" {
			Reroute(ctx, w.St.Q, w.GW, d.ServiceID)
		}
	}
	return nil
}

func (w *Domains) liveContainer(ctx context.Context, serviceID string) (string, error) {
	svc, err := w.St.Q.GetService(ctx, serviceID)
	if err != nil || !svc.CurrentDeployID.Valid {
		return "", err
	}
	d, err := w.St.Q.GetDeploy(ctx, svc.CurrentDeployID.String)
	if err != nil || d.State != "live" {
		return "", err
	}
	return d.Container, nil
}

func (w *Domains) ownedOn(ctx context.Context, host, serverID string) bool {
	ds, _ := w.St.Q.DomainsOnHost(ctx, host)
	return slices.ContainsFunc(ds, func(d db.DomainsOnHostRow) bool { return d.Host == host && d.ServerID == serverID })
}

func (w *Domains) removals(ctx context.Context) error {
	srvs, err := w.St.Q.ListServers(ctx)
	if err != nil {
		return err
	}
	for _, s := range srvs {
		v, err := w.St.Q.GetSetting(ctx, app.RemovalKey(s.ID))
		if err != nil {
			continue
		}
		var hosts, done []string
		json.Unmarshal([]byte(v), &hosts)
		for _, h := range hosts {
			if w.ownedOn(ctx, h, s.ID) {
				done = append(done, h)
				continue
			}
			cmd, _ := proto.NewCommand(fmt.Sprintf("unroute:%s:%d", h, w.now().UnixMilli()), proto.CmdRemoveRoute, proto.RemoveRouteArgs{Host: h})
			sctx, cancel := context.WithTimeout(ctx, time.Minute)
			res, err := w.GW.Send(sctx, s.ID, cmd)
			cancel()
			if err == nil && res.OK {
				done = append(done, h)
			}
		}
		if len(done) == 0 {
			continue
		}
		if err := w.St.Tx(ctx, func(q *db.Queries) error {
			var now []string
			if v, err := q.GetSetting(ctx, app.RemovalKey(s.ID)); err == nil {
				json.Unmarshal([]byte(v), &now)
			}
			now = slices.DeleteFunc(now, func(x string) bool { return slices.Contains(done, x) })
			if len(now) == 0 {
				return q.DeleteSettingKey(ctx, app.RemovalKey(s.ID))
			}
			b, _ := json.Marshal(now)
			return q.SetSetting(ctx, db.SetSettingParams{Key: app.RemovalKey(s.ID), Value: string(b)})
		}); err != nil {
			return err
		}
	}
	return nil
}
