package statuspage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

type Outside struct {
	Subject  string `json:"subject"`
	OpenedAt int64  `json:"opened_at"`
	ClosedAt *int64 `json:"closed_at"`
}

type service struct {
	project, server string
	member          Member
}

type World struct {
	services map[string]service
	projects map[string][]string
	panel    Member
}

func Load(ctx context.Context, db DB, outside []Outside, now time.Time) (World, error) {
	n := now.UnixMilli()
	from := n - (Days+1)*day
	w := World{services: map[string]service{}, projects: map[string][]string{}}
	evs, err := HealthEvents(ctx, db, from)
	if err != nil {
		return w, err
	}
	by := map[string][]Event{}
	for _, e := range evs {
		by[e.Subject] = append(by[e.Subject], e)
	}
	out, open := map[string][]Span{}, map[string]bool{}
	for _, o := range outside {
		to := n
		if o.ClosedAt != nil {
			to = *o.ClosedAt
		} else {
			open[o.Subject] = true
		}
		out[o.Subject] = append(out[o.Subject], Span{o.OpenedAt, to})
	}
	rs, err := db.QueryContext(ctx, `SELECT s.id, s.health, s.user_stopped, s.created_at, e.project_id, e.server_id, srv.offline, COALESCE(dep.state, '')
FROM services s JOIN environments e ON e.id = s.environment_id JOIN servers srv ON srv.id = e.server_id
LEFT JOIN deploys dep ON dep.id = s.current_deploy_id
WHERE s.deleted_at IS NULL AND s.kind <> 'cron' AND e.preview_of = '' ORDER BY s.id`)
	if err != nil {
		return w, err
	}
	defer rs.Close()
	for rs.Next() {
		var id, health, project, server, deploy string
		var stopped, offline, created int64
		if err := rs.Scan(&id, &health, &stopped, &created, &project, &server, &offline, &deploy); err != nil {
			return w, err
		}
		spans := Spans(by[id], "service.down", "service.recovered", from, n)
		spans = append(spans, Spans(by[server], "server.offline", "server.online", from, n)...)
		spans = append(spans, out["service:"+id]...)
		spans = append(spans, out["server:"+server]...)
		down := health == "down" || offline == 1 || stopped == 1 || deploy != "live" || open["service:"+id] || open["server:"+server]
		w.services[id] = service{project: project, server: server, member: Member{Since: created, Down: Join(spans), DownNow: down}}
		w.projects[project] = append(w.projects[project], id)
	}
	w.panel = Member{Down: Join(out["panel"]), DownNow: open["panel"]}
	return w, rs.Err()
}

func (w World) Members(subject string) []Member {
	kind, id, _ := strings.Cut(subject, ":")
	switch kind {
	case "panel":
		return []Member{w.panel}
	case "service":
		if s, ok := w.services[id]; ok {
			return []Member{s.member}
		}
	case "project":
		var out []Member
		for _, sid := range w.projects[id] {
			out = append(out, w.services[sid].member)
		}
		return out
	}
	return nil
}

func (w World) Page(p PageRec, incidents []IncidentRec, now time.Time) Page {
	out := Page{Title: p.Title, Now: now}
	for _, r := range p.Rows {
		if m := w.Members(r.Subject); len(m) > 0 {
			out.Rows = append(out.Rows, Row{ID: r.ID, Name: r.Name, Members: m})
		}
	}
	for _, i := range incidents {
		if i.State == IncidentPublished {
			out.Incidents = append(out.Incidents, i.Shown())
		}
	}
	return out
}

func FreeHost(slug string, ip netip.Addr) string {
	return fmt.Sprintf("%s.status.%s.sslip.io", slug, strings.NewReplacer(".", "-", ":", "-").Replace(ip.String()))
}

type watcherRow struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Subjects []string `json:"subjects"`
}

type watcherIncident struct {
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	Rows       []string `json:"rows"`
	Updates    []Update `json:"updates"`
	StartsAt   int64    `json:"starts_at"`
	EndsAt     int64    `json:"ends_at"`
	ResolvedAt int64    `json:"resolved_at"`
	CreatedAt  int64    `json:"created_at"`
}

func WatcherConfig(ctx context.Context, db DB, now time.Time) (string, error) {
	pages, err := Pages(ctx, db)
	if err != nil {
		return "", err
	}
	for _, p := range pages {
		if !p.Watcher {
			continue
		}
		w, err := Load(ctx, db, nil, now)
		if err != nil {
			return "", err
		}
		rows := []watcherRow{}
		for _, r := range p.Rows {
			var subjects []string
			kind, id, _ := strings.Cut(r.Subject, ":")
			switch kind {
			case "panel":
				subjects = []string{"panel"}
			case "service":
				if _, ok := w.services[id]; ok {
					subjects = []string{r.Subject}
				}
			case "project":
				for _, sid := range w.projects[id] {
					subjects = append(subjects, "service:"+sid)
				}
			}
			if len(subjects) > 0 {
				rows = append(rows, watcherRow{ID: r.ID, Name: r.Name, Subjects: subjects})
			}
		}
		incs, err := Incidents(ctx, db, p.ID, now.UnixMilli()-recent)
		if err != nil {
			return "", err
		}
		list := []watcherIncident{}
		for _, i := range incs {
			if i.State == IncidentPublished {
				list = append(list, watcherIncident{Kind: i.Kind, Title: i.Title, Rows: i.Rows, Updates: i.Updates, StartsAt: i.StartsAt, EndsAt: i.EndsAt, ResolvedAt: i.ResolvedAt, CreatedAt: i.CreatedAt})
			}
		}
		b, err := json.Marshal(map[string]any{"title": p.Title, "rows": rows, "incidents": list})
		return string(b), err
	}
	return "", nil
}
