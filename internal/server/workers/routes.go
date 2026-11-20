package workers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func RouteOptions(ctx context.Context, q *db.Queries, serviceID string, hosts []string) (proto.RouteOptions, error) {
	svc, err := q.GetService(ctx, serviceID)
	if err != nil {
		return proto.RouteOptions{}, err
	}
	o := proto.RouteOptions{NoIndex: svc.Noindex == 1 || svc.PreviewOf != ""}
	if svc.Maintenance == 1 {
		o.Maintenance = svc.Name
	}
	var auth struct{ User, Hash string }
	if json.Unmarshal([]byte(svc.BasicAuth), &auth) == nil && auth.User != "" {
		o.BasicUser, o.BasicHash = auth.User, auth.Hash
	}
	has := map[string]bool{}
	for _, h := range hosts {
		has[h] = true
	}
	for _, h := range hosts {
		bare, isWWW := strings.CutPrefix(h, "www.")
		switch {
		case strings.Contains(h, "/"):
		case svc.Www == "bare" && isWWW && has[bare]:
			o.Redirect = setRedirect(o.Redirect, h, bare)
		case svc.Www == "www" && !isWWW && has["www."+h]:
			o.Redirect = setRedirect(o.Redirect, h, "www."+h)
		}
	}
	return o, nil
}

func setRedirect(m map[string]string, from, to string) map[string]string {
	if m == nil {
		m = map[string]string{}
	}
	m[from] = to
	return m
}

var ErrDeployRunning = errors.New("a deploy is running; it takes the change when it switches")

var ErrMoving = errors.New("the environment is moving; its deploys on the new server take the change")

type domainRow struct{ host, dns string }

func reachable(ds []domainRow, self string) []string {
	out := []string{self}
	for _, d := range ds {
		if d.dns == "ok" && d.host != self {
			out = append(out, d.host)
		}
	}
	return out
}

func Reroute(ctx context.Context, q *db.Queries, gw Sender, serviceID string) error {
	svc, err := q.GetService(ctx, serviceID)
	if err != nil || svc.Kind != "web" || !svc.CurrentDeployID.Valid || svc.UserStopped == 1 || svc.DeletedAt.Valid {
		return err
	}
	if n, err := q.ActiveDeploys(ctx, serviceID); err != nil {
		return err
	} else if n > 0 {
		return ErrDeployRunning
	}
	if app.Adopting(ctx, q) {
		return errors.New("the panel waits for its adoption to be accepted; the change applies with the next deploy")
	}
	if _, err := q.ActiveMove(ctx, svc.EnvironmentID); err == nil {
		return ErrMoving
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if gw == nil {
		return nil
	}
	d, err := q.GetDeploy(ctx, svc.CurrentDeployID.String)
	if err != nil || d.State != "live" || d.Container == "" {
		return err
	}
	srv, err := q.GetServer(ctx, svc.ServerID)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(srv.PublicIp)
	if err != nil {
		return err
	}
	hosts := []string{app.FreeHost(svc.Name, svc.Env, ip)}
	ds, err := q.ListDomains(ctx, svc.ID)
	if err != nil {
		return err
	}
	for _, x := range ds {
		if x.DnsState == "ok" {
			hosts = append(hosts, x.Host)
		}
	}
	opts, err := RouteOptions(ctx, q, svc.ID, hosts)
	if err != nil {
		return err
	}
	cmd, err := proto.NewCommand(fmt.Sprintf("reroute:%s:%d", svc.ID, time.Now().UnixNano()), proto.CmdSwitchRoute,
		proto.SwitchArgs{Hosts: hosts, Container: d.Container, Port: int(svc.Port), Options: opts})
	if err != nil {
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	res, err := gw.Send(sctx, svc.ServerID, cmd)
	if err == nil && !res.OK {
		err = errors.New(res.Cause)
	}
	return err
}
