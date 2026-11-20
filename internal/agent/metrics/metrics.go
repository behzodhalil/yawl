package metrics

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/behzodhalil/yawl/internal/proto"
)

type Ring struct {
	N  int
	mu sync.Mutex
	s  []proto.Sample
}

func (r *Ring) Add(s proto.Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.s = append(r.s, s)
	if len(r.s) > r.N {
		r.s = r.s[len(r.s)-r.N:]
	}
}

func (r *Ring) Since(at int64) []proto.Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []proto.Sample
	for _, s := range r.s {
		if s.At > at {
			out = append(out, s)
		}
	}
	return out
}

type CPU struct{ Total, Idle uint64 }

const (
	statIdleField   = 3
	statIOWaitField = 4
)

func ParseStat(text string) CPU {
	for _, l := range strings.Split(text, "\n") {
		f := strings.Fields(l)
		if len(f) < 6 || f[0] != "cpu" {
			continue
		}
		var c CPU
		for i, v := range f[1:] {
			n, _ := strconv.ParseUint(v, 10, 64)
			c.Total += n
			if i == statIdleField || i == statIOWaitField {
				c.Idle += n
			}
		}
		return c
	}
	return CPU{}
}

func CPUBusy(a, b CPU) float64 {
	dt := float64(b.Total) - float64(a.Total)
	if dt <= 0 {
		return 0
	}
	return 100 * (dt - (float64(b.Idle) - float64(a.Idle))) / dt
}

func ParseMeminfo(text string) (used, total uint64) {
	var avail uint64
	for _, l := range strings.Split(text, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		n, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = n * 1024
		case "MemAvailable:":
			avail = n * 1024
		}
	}
	if avail > total {
		return 0, total
	}
	return total - avail, total
}

type Caddy struct {
	prev map[string]float64
}

const histo = "caddy_http_request_duration_seconds"

type series struct {
	host, code, le, server string
	value                  float64
	count                  bool
}

func parse(text string) []series {
	var out []series
	for _, l := range strings.Split(text, "\n") {
		if !strings.HasPrefix(l, histo+"_bucket{") && !strings.HasPrefix(l, histo+"_count{") {
			continue
		}
		open, close := strings.IndexByte(l, '{'), strings.LastIndexByte(l, '}')
		if open < 0 || close < open {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(l[close+1:]), 64)
		if err != nil {
			continue
		}
		s := series{value: v, count: strings.HasPrefix(l, histo+"_count")}
		for _, kv := range strings.Split(l[open+1:close], ",") {
			k, val, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			val = strings.Trim(val, `"`)
			switch k {
			case "host":
				s.host = val
			case "code":
				s.code = val
			case "le":
				s.le = val
			case "server":
				s.server = val
			}
		}
		if s.server == "http" && s.code == "308" {
			continue
		}
		if s.host != "" {
			out = append(out, s)
		}
	}
	return out
}

func key(s series) string {
	return s.host + "|" + s.code + "|" + s.le + "|" + strconv.FormatBool(s.count)
}

func (c *Caddy) Primed() bool { return c.prev != nil }

func (c *Caddy) Forget() { c.prev = nil }

func (c *Caddy) Hosts(text string) []proto.HostSample {
	now := map[string]float64{}
	sums := map[string]series{} // one per key: Caddy splits a host's series by method and handler
	for _, s := range parse(text) {
		now[key(s)] += s.value
		s.value = now[key(s)]
		sums[key(s)] = s
	}
	prev := c.prev
	c.prev = now
	if prev == nil {
		return nil
	}
	type agg struct {
		req, err5 float64
		buckets   map[string]float64
		reset     bool
	}
	hosts := map[string]*agg{}
	for _, s := range sums {
		a := hosts[s.host]
		if a == nil {
			a = &agg{buckets: map[string]float64{}}
			hosts[s.host] = a
		}
		d := s.value - prev[key(s)]
		if d < 0 {
			a.reset = true
			continue
		}
		switch {
		case s.count:
			a.req += d
			if strings.HasPrefix(s.code, "5") {
				a.err5 += d
			}
		default:
			a.buckets[s.le] += d
		}
	}
	var out []proto.HostSample
	for h, a := range hosts {
		if a.reset {
			out = append(out, proto.HostSample{Host: h, Gap: true})
			continue
		}
		out = append(out, proto.HostSample{Host: h, Req: int64(a.req), Err5: int64(a.err5), P95ms: p95(a.buckets, a.req)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

func p95(buckets map[string]float64, req float64) int64 {
	if req == 0 {
		return 0
	}
	type b struct {
		le  float64
		cum float64
	}
	var bs []b
	for le, cum := range buckets {
		v, err := strconv.ParseFloat(le, 64)
		if le == "+Inf" {
			v, err = math.Inf(1), nil
		}
		if err == nil {
			bs = append(bs, b{v, cum})
		}
	}
	sort.Slice(bs, func(i, j int) bool { return bs[i].le < bs[j].le })
	last := 0.0
	for _, x := range bs {
		if math.IsInf(x.le, 1) {
			break
		}
		last = x.le
		if x.cum*100 >= 95*req {
			return int64(math.Round(x.le * 1000))
		}
	}
	return int64(math.Round(last * 1000))
}
