package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type App struct {
	St  *store.Store
	Now func() time.Time

	mu          sync.Mutex
	restoreKeys map[string]string
	moveKeys    map[string]string

	CompareTimeout time.Duration
	Limits         Limits
	Billing        *Billing
}

func New(st *store.Store) *App { return &App{St: st, Now: time.Now} }

func (a *App) Deploy(ctx context.Context, serviceID, commit, actor string) (string, error) {
	return a.deploy(ctx, serviceID, commit, actor, false)
}

func (a *App) DeployNoCache(ctx context.Context, serviceID, commit, actor string) (string, error) {
	return a.deploy(ctx, serviceID, commit, actor, true)
}

func (a *App) deploy(ctx context.Context, serviceID, commit, actor string, noCache bool) (string, error) {
	if err := a.unpaid(ctx); err != nil {
		return "", err
	}
	var id string
	err := a.St.Tx(ctx, func(q *db.Queries) (err error) {
		if id, err = a.queue(ctx, q, serviceID, commit, actor, actor, ""); err != nil || !noCache {
			return err
		}
		return q.SetDeployNoCache(ctx, id)
	})
	return id, err
}

func (a *App) queue(ctx context.Context, q *db.Queries, serviceID, commit, actor, by, delivery string) (string, error) {
	id, now := store.NewID("dep_"), a.Now().UnixMilli()
	noCache, err := q.QueuedNoCache(ctx, serviceID)
	if err != nil {
		return "", err
	}
	if err := q.SupersedeQueued(ctx, db.SupersedeQueuedParams{UpdatedAt: now, ServiceID: serviceID}); err != nil {
		return "", err
	}
	if err := q.CreateDeploy(ctx, db.CreateDeployParams{ID: id, ServiceID: serviceID, CommitSha: commit, Actor: by, CreatedAt: now, UpdatedAt: now}); err != nil {
		return "", err
	}
	if noCache > 0 {
		if err := q.SetDeployNoCache(ctx, id); err != nil {
			return "", err
		}
	}
	body := map[string]string{"service": serviceID, "commit": commit}
	if delivery != "" {
		body["delivery"] = delivery
	}
	return id, Event(ctx, q, now, "deploy.queued", id, actor, body)
}

func Event(ctx context.Context, q *db.Queries, now int64, kind, subject, actor string, body any) error {
	b, _ := json.Marshal(body)
	return q.InsertEvent(ctx, db.InsertEventParams{ID: store.NewID("evt_"), Kind: kind, SubjectID: subject, Actor: actor,
		BodyJson: string(b), CreatedAt: now, UpdatedAt: now})
}

func FreeHost(service, env string, ip netip.Addr) string {
	return fmt.Sprintf("%s-%s.%s.sslip.io", service, env, strings.NewReplacer(".", "-", ":", "-").Replace(ip.String()))
}

type HostTaken string

func (e HostTaken) Error() string { return string(e) }

func CheckFreeHost(ctx context.Context, q *db.Queries, serverID, project, env, service string) error {
	srv, err := q.GetServer(ctx, serverID)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(srv.PublicIp)
	if err != nil {
		return nil
	}
	host := FreeHost(service, env, ip)
	owner, err := FreeHostOwner(ctx, q, serverID, ip, host, project, env, service)
	if err != nil || owner == "" {
		return err
	}
	return HostTaken(fmt.Sprintf("%s is already %s's; name this service or environment differently.", host, owner))
}

func FreeHostOwner(ctx context.Context, q *db.Queries, serverID string, ip netip.Addr, host, project, env, service string) (string, error) {
	ws, err := q.WebServicesOnServer(ctx, serverID)
	if err != nil {
		return "", err
	}
	for _, w := range ws {
		if FreeHost(w.Name, w.Env, ip) == host && (w.Project != project || w.Env != env || w.Name != service) {
			return w.Project + "/" + w.Env + "/" + w.Name, nil
		}
	}
	return "", nil
}

func (a *App) Adopting(ctx context.Context) bool { return Adopting(ctx, a.St.Q) }

func Adopting(ctx context.Context, q *db.Queries) bool {
	v, err := q.GetSetting(ctx, "adopt.pending")
	return err == nil && v == "1"
}

func PanelHosts(ctx context.Context, q *db.Queries, publicURL string) (ip string, hosts []string) {
	id, err := q.GetSetting(ctx, "panel.server_id")
	if err != nil {
		return "", nil
	}
	srv, err := q.GetServer(ctx, id)
	if err != nil {
		return "", nil
	}
	d, _ := q.GetSetting(ctx, "panel.domain")
	if d != "" {
		hosts = []string{d}
		dns, _ := q.GetSetting(ctx, "panel."+d+".dns")
		cert, _ := q.GetSetting(ctx, "panel."+d+".cert")
		u, _ := url.Parse(publicURL)
		if dns == "ok" && cert == "issued" && (u == nil || u.Hostname() != PanelFreeHost(srv.PublicIp)) && !Adopting(ctx, q) {
			return srv.PublicIp, hosts
		}
	}
	return srv.PublicIp, append([]string{PanelFreeHost(srv.PublicIp)}, hosts...)
}

func PanelFreeHost(ip string) string {
	return "panel." + strings.NewReplacer(".", "-", ":", "-").Replace(ip) + ".sslip.io"
}
