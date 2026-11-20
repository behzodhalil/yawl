package watch

import (
	"context"
	"net/netip"
	"slices"
	"strings"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type ExpectKind string

const (
	ExpectURL       ExpectKind = "url"
	ExpectServer    ExpectKind = "server"
	ExpectPanel     ExpectKind = "panel"
	ExpectHeartbeat ExpectKind = "heartbeat"
	ExpectBackup    ExpectKind = "backup"
	ExpectCron      ExpectKind = "cron"
)

type Expectation struct {
	ID      string     `json:"id"`
	Kind    ExpectKind `json:"kind"`
	Subject string     `json:"subject"`
	Label   string     `json:"label"`
	Target  string     `json:"target"`
	Server  string     `json:"server"`
	PeriodS int64      `json:"period_s"`
	GraceS  int64      `json:"grace_s"`
	Project string     `json:"-"`
}

func AgentMarker(fingerprint, url string) string { return fingerprint + "|" + url }

func Build(ctx context.Context, q *db.Queries) ([]Expectation, error) {
	var out []Expectation
	servers, err := q.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	fp, _ := q.GetSetting(ctx, "watch.deployed")
	u, _ := q.GetSetting(ctx, "watch.url")
	for _, s := range servers {
		if m, _ := q.GetSetting(ctx, "watch.agent."+s.ID); fp == "" || m != AgentMarker(fp, u) {
			continue
		}
		out = append(out, Expectation{ID: "server:" + s.ID, Kind: ExpectServer, Subject: "server:" + s.ID, Label: s.Name, Server: s.ID})
	}
	panel, _ := q.GetSetting(ctx, "panel.server_id")
	out = append(out, Expectation{ID: "panel", Kind: ExpectPanel, Subject: "panel", Label: "The panel", Server: panel})
	web, err := q.WatchedWebServices(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range web {
		label := s.Project + "/" + s.Env + "/" + s.Name
		var hosts []string
		if ip, err := netip.ParseAddr(s.PublicIp); err == nil {
			hosts = append(hosts, app.FreeHost(s.Name, s.Env, ip))
		}
		doms, err := q.ListDomains(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		for _, d := range doms {
			if d.DnsState == "ok" {
				hosts = append(hosts, d.Host)
			}
		}
		path := s.HealthPath
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		for _, h := range hosts {
			out = append(out, Expectation{ID: "url:" + s.ID + ":" + h, Kind: ExpectURL, Subject: "service:" + s.ID, Label: label,
				Target: "https://" + h + path, Server: s.ServerID, Project: s.Project})
		}
	}
	hbs, err := q.ListHeartbeats(ctx)
	if err != nil {
		return nil, err
	}
	for _, h := range hbs {
		out = append(out, Expectation{ID: "hb:" + h.ID, Kind: ExpectHeartbeat, Subject: "hb:" + h.ID, Label: h.Name, Target: h.Token,
			PeriodS: h.PeriodS, GraceS: h.GraceS})
	}
	dbs, err := q.ListDatabaseServices(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range dbs {
		out = append(out, Expectation{ID: "backup:" + d.ID, Kind: ExpectBackup, Subject: "backup:" + d.ID,
			Label: d.Project + "/" + d.Env + "/" + d.Name + " backups", Server: d.ServerID, Project: d.Project})
	}
	crons, err := app.LiveCrons(ctx, q)
	if err != nil {
		return nil, err
	}
	for _, c := range crons {
		out = append(out, Expectation{ID: "cron:" + c.ID, Kind: ExpectCron, Subject: "cron:" + c.ID, Label: c.Project + "/" + c.Env + "/" + c.Name,
			Target: c.Scheduled.Timezone + " " + c.Scheduled.Schedule, Server: c.ServerID, Project: c.Project, PeriodS: c.Scheduled.TimeoutMs / 1000})
	}
	slices.SortFunc(out, func(a, b Expectation) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func Hosts(ctx context.Context, q *db.Queries) (map[string]string, error) {
	web, err := q.WatchedWebServices(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, s := range web {
		if ip, err := netip.ParseAddr(s.PublicIp); err == nil {
			out[app.FreeHost(s.Name, s.Env, ip)] = s.ID
		}
		doms, err := q.ListDomains(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		for _, d := range doms {
			if d.DnsState == "ok" {
				out[d.Host] = s.ID
			}
		}
	}
	return out, nil
}
