package statuspage

import (
	"bytes"
	_ "embed"
	"html/template"
	"slices"
	"strings"
	"time"
)

//go:embed page.html
var pageHTML string

var tmpl = template.Must(template.New("page").Parse(pageHTML))

type viewRow struct {
	Name   string
	State  State
	Bars   []Bar
	Uptime string
}

type viewIncident struct {
	Title, When string
	Badge       State
	Affects     string
	Updates     []viewUpdate
}

type viewUpdate struct{ At, Body string }

type view struct {
	Title                        string
	Summary                      State
	Updated                      string
	Rows                         []viewRow
	Ongoing, Scheduled, Recently []viewIncident
}

func Render(p Page) []byte {
	now := p.Now.UnixMilli()
	at := func(ms int64) string { return time.UnixMilli(ms).In(p.Now.Location()).Format("2 Jan 15:04 MST") }
	v := view{Title: p.Title, Updated: at(now)}
	var states []State
	names := map[string]string{}
	for _, r := range p.Rows {
		names[r.ID] = r.Name
		st := RowState(r, p.Incidents, now)
		states = append(states, st)
		bars, up := Bars(r, p.Now)
		v.Rows = append(v.Rows, viewRow{Name: r.Name, State: st, Bars: bars, Uptime: up})
	}
	v.Summary = Summary(states)
	show := func(i Incident, badge State, when string) viewIncident {
		var aff []string
		for _, id := range i.Rows {
			if n, ok := names[id]; ok {
				aff = append(aff, n)
			}
		}
		vi := viewIncident{Title: i.Title, When: when, Badge: badge, Affects: strings.Join(aff, ", ")}
		for _, u := range i.Updates {
			vi.Updates = append(vi.Updates, viewUpdate{At: at(u.At), Body: u.Body})
		}
		return vi
	}
	inc := slices.Clone(p.Incidents)
	slices.SortFunc(inc, func(a, b Incident) int { return int(b.start() - a.start()) })
	for _, i := range inc {
		switch {
		case i.open() && len(v.Ongoing) < maxOngoing:
			v.Ongoing = append(v.Ongoing, show(i, State{"busy", "Ongoing"}, "Since "+at(i.start())))
		case i.inWindow(now) && len(v.Ongoing) < maxOngoing:
			v.Ongoing = append(v.Ongoing, show(i, Maintenance, "Until "+at(i.EndsAt)))
		case i.Kind == KindMaint && i.StartsAt > now:
			v.Scheduled = append(v.Scheduled, show(i, State{"off", "Scheduled"}, at(i.StartsAt)+" to "+at(i.EndsAt)))
		case len(v.Recently) >= maxRecent:
		case i.Kind == KindIncident && now-i.ResolvedAt < recent:
			v.Recently = append(v.Recently, show(i, State{"ok", "Resolved"}, at(i.start())+", "+Lasted(i.ResolvedAt-i.start())))
		case i.Kind == KindMaint && i.EndsAt <= now && now-i.EndsAt < recent:
			v.Recently = append(v.Recently, show(i, State{"ok", "Completed"}, at(i.StartsAt)+" to "+at(i.EndsAt)))
		}
	}
	slices.Reverse(v.Scheduled)
	var b bytes.Buffer
	if err := tmpl.Execute(&b, v); err != nil {
		panic(err)
	}
	return b.Bytes()
}

func (i Incident) start() int64 {
	if i.Kind == KindMaint {
		return i.StartsAt
	}
	return i.CreatedAt
}
