package cron

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // a server without zoneinfo still knows every zone
)

type Schedule struct {
	min, hour, dom, mon, dow uint64
	domStar, dowStar         bool
}

var fields = []struct {
	name   string
	lo, hi int
}{{"minute", 0, 59}, {"hour", 0, 23}, {"day of month", 1, 31}, {"month", 1, 12}, {"day of week", 0, 7}}

func Parse(expr string) (Schedule, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return Schedule{}, fmt.Errorf("must be five fields (minute hour day month weekday), got %q", expr)
	}
	var bits [5]uint64
	for i, p := range parts {
		for _, item := range strings.Split(p, ",") {
			b, ok := field(item, fields[i].lo, fields[i].hi)
			if !ok {
				return Schedule{}, fmt.Errorf("%s %q is not a number from %d to %d", fields[i].name, item, fields[i].lo, fields[i].hi)
			}
			bits[i] |= b
		}
	}
	if bits[4]&(1<<7) != 0 { // 7 is Sunday too
		bits[4] |= 1
	}
	// plan 13 minor (M3): as in Vixie cron, a field starting with * (like */2) is a star for the day rule
	return Schedule{min: bits[0], hour: bits[1], dom: bits[2], mon: bits[3], dow: bits[4],
		domStar: strings.HasPrefix(parts[2], "*"), dowStar: strings.HasPrefix(parts[4], "*")}, nil
}

func field(item string, lo, hi int) (uint64, bool) {
	rng, step := item, 1
	if i := strings.IndexByte(item, '/'); i >= 0 {
		s, err := num(item[i+1:])
		if err != nil || s < 1 {
			return 0, false
		}
		rng, step = item[:i], s
	}
	a, b := lo, hi
	switch {
	case rng == "*":
	case strings.Contains(rng, "-"):
		x, y, _ := strings.Cut(rng, "-")
		var e1, e2 error
		a, e1 = num(x)
		b, e2 = num(y)
		if e1 != nil || e2 != nil || a > b {
			return 0, false
		}
	default:
		n, err := num(rng)
		if err != nil {
			return 0, false
		}
		a = n
		if step == 1 {
			b = n
		}
	}
	if a < lo || b > hi {
		return 0, false
	}
	var bits uint64
	for v := a; v <= b; v += step {
		bits |= 1 << v
	}
	return bits, true
}

// num reads digits only: Atoi takes "+5", which the Worker's parser refuses
// and so would never judge (plan 13 minor, M2).
func num(s string) (int, error) {
	if strings.TrimLeft(s, "0123456789") != "" {
		return 0, strconv.ErrSyntax
	}
	return strconv.Atoi(s)
}

func (s Schedule) Matches(t time.Time) bool {
	if s.min&(1<<t.Minute()) == 0 || s.hour&(1<<t.Hour()) == 0 || s.mon&(1<<int(t.Month())) == 0 {
		return false
	}
	dom, dow := s.dom&(1<<t.Day()) != 0, s.dow&(1<<int(t.Weekday())) != 0
	if !s.domStar && !s.dowStar {
		return dom || dow // both restricted: either day will do (Vixie cron)
	}
	return dom && dow
}

func (s Schedule) Next(after time.Time) time.Time {
	t := after.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ {
		if s.Matches(t) {
			return t
		}
		t = t.Add(time.Minute) // absolute minutes: a skipped hour has none, a repeated one has two
	}
	return time.Time{}
}

func Zone(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, errors.New("no time zone")
	}
	return time.LoadLocation(name)
}

// PanelZone is the zone a job without one runs in: the panel's own, or UTC
// when Go knows it only as "Local".
func PanelZone() string {
	if n := time.Local.String(); n != "" && n != "Local" {
		return n
	}
	return "UTC"
}
