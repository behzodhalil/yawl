package statuspage

import (
	"fmt"
	"math"
	"slices"
	"time"
)

const (
	Days   = 90
	minute = int64(time.Minute / time.Millisecond)
	day    = 24 * 60 * minute
	recent = 14 * day

	MaxRows      = 50
	MaxTitle     = 120
	MaxName      = 60
	MaxUpdate    = 2000
	maxOngoing   = 20
	maxRecent    = 30
	KindIncident = "incident"
	KindMaint    = "maintenance"
)

type Span struct{ FromMs, ToMs int64 }

type Member struct {
	Since   int64
	Down    []Span
	DownNow bool
}

type Row struct {
	ID, Name string
	Members  []Member
}

type Update struct {
	ID   string `json:"id"`
	At   int64  `json:"at"`
	Body string `json:"body"`
}

type Incident struct {
	Kind, Title                             string
	Rows                                    []string
	Updates                                 []Update
	StartsAt, EndsAt, ResolvedAt, CreatedAt int64
}

type Page struct {
	Title     string
	Rows      []Row
	Incidents []Incident
	Now       time.Time
}

func Join(spans []Span) []Span {
	s := slices.Clone(spans)
	slices.SortFunc(s, func(a, b Span) int { return int(a.FromMs - b.FromMs) })
	var out []Span
	for _, x := range s {
		if x.ToMs <= x.FromMs {
			continue
		}
		if n := len(out); n > 0 && x.FromMs <= out[n-1].ToMs {
			out[n-1].ToMs = max(out[n-1].ToMs, x.ToMs)
			continue
		}
		out = append(out, x)
	}
	return out
}

type Event struct {
	Kind    string
	Subject string
	At      int64
}

func Spans(evs []Event, down, up string, from, now int64) []Span {
	var out []Span
	open, at := false, int64(0)
	for i, e := range evs {
		switch {
		case e.Kind == down && !open:
			open, at = true, e.At
		case e.Kind == up && open:
			out = append(out, Span{at, e.At})
			open = false
		case e.Kind == up && i == 0:
			out = append(out, Span{from, e.At})
		}
	}
	if open {
		out = append(out, Span{at, now})
	}
	return out
}

func overlap(spans []Span, from, to int64) int64 {
	var n int64
	for _, s := range spans {
		if lo, hi := max(s.FromMs, from), min(s.ToMs, to); hi > lo {
			n += hi - lo
		}
	}
	return n
}

type State struct{ Kind, Word string }

var (
	Operational = State{"ok", "Operational"}
	Degraded    = State{"busy", "Degraded"}
	Down        = State{"bad", "Down"}
	Maintenance = State{"busy", "Maintenance"}
)

func (i Incident) names(row string) bool { return slices.Contains(i.Rows, row) }
func (i Incident) inWindow(now int64) bool {
	return i.Kind == KindMaint && i.StartsAt <= now && now < i.EndsAt
}
func (i Incident) open() bool { return i.Kind == KindIncident && i.ResolvedAt == 0 }

func RowState(r Row, incidents []Incident, now int64) State {
	for _, i := range incidents {
		if i.inWindow(now) && i.names(r.ID) {
			return Maintenance
		}
	}
	down := 0
	for _, m := range r.Members {
		if m.DownNow {
			down++
		}
	}
	switch {
	case down > 0 && down == len(r.Members):
		return Down
	case down > 0:
		return Degraded
	}
	for _, i := range incidents {
		if i.open() && i.names(r.ID) {
			return Degraded
		}
	}
	return Operational
}

func Summary(states []State) State {
	n := map[State]int{}
	for _, s := range states {
		n[s]++
	}
	switch {
	case len(states) == 0:
		return State{"off", "Nothing is shown here yet"}
	case n[Down] == len(states):
		return State{"bad", "All services are down"}
	case n[Down] > 0:
		return State{"bad", "Some services are down"}
	case n[Degraded] > 0:
		return State{"busy", "Some services are degraded"}
	case n[Maintenance] > 0:
		return State{"busy", "Maintenance is under way"}
	}
	return State{"ok", "All services are operational"}
}

type Bar struct{ Class, Title string }

func Bars(r Row, now time.Time) ([]Bar, string) {
	loc, nowMs := now.Location(), now.UnixMilli()
	y, mo, d := now.Date()
	out := make([]Bar, 0, Days)
	var down, total int64
	for k := Days - 1; k >= 0; k-- {
		start := time.Date(y, mo, d-k, 0, 0, 0, 0, loc)
		from, to := start.UnixMilli(), min(time.Date(y, mo, d-k+1, 0, 0, 0, 0, loc).UnixMilli(), nowMs)
		label := start.Format("2 Jan")
		var sum, worst int64
		seen := 0
		for _, m := range r.Members {
			lo := max(from, m.Since)
			if lo >= to {
				continue
			}
			seen++
			dn := overlap(m.Down, lo, to)
			sum, worst = sum+dn, max(worst, dn)
			down, total = down+dn, total+(to-lo)
		}
		switch {
		case seen == 0:
			out = append(out, Bar{"off", label + ": no data"})
		case sum/int64(seen) >= minute:
			out = append(out, Bar{"bad", label + ": " + Lasted(sum/int64(seen)) + " down"})
		case worst >= minute:
			out = append(out, Bar{"warn", fmt.Sprintf("%s: some down, the longest %s", label, Lasted(worst))})
		default:
			out = append(out, Bar{"ok", label + ": no downtime"})
		}
	}
	if total == 0 {
		return out, ""
	}
	if down == 0 {
		return out, "100%"
	}
	pct := math.Floor(10000*(1-float64(down)/float64(total))) / 100
	return out, fmt.Sprintf("%.2f%%", pct)
}

func Lasted(ms int64) string {
	m := ms / minute
	switch {
	case m < 1:
		return "less than a minute"
	case m == 1:
		return "1 minute"
	case m < 60:
		return fmt.Sprintf("%d minutes", m)
	}
	return fmt.Sprintf("%d h %d min", m/60, m%60)
}
