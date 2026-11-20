package analytics

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
)

var bot = regexp.MustCompile(`(?i)bot|crawl|spider|slurp|preview|monitor|curl|wget|python-requests|go-http-client`)

var visitorsExactBelow = 1 << 20

type entry struct {
	Logger  string `json:"logger"`
	Request struct {
		ClientIP string              `json:"client_ip"`
		Method   string              `json:"method"`
		Host     string              `json:"host"`
		URI      string              `json:"uri"`
		Headers  map[string][]string `json:"headers"`
		TLS      *struct{}           `json:"tls"`
	} `json:"request"`
	Size        int64               `json:"size"`
	Status      int                 `json:"status"`
	RespHeaders map[string][]string `json:"resp_headers"`
}

type Counter struct {
	mu   sync.Mutex
	day  string
	salt []byte
	seen map[string]map[[8]byte]bool
	cur  map[string]*proto.VisitSample
	done []proto.VisitSample
}

func New() *Counter { return &Counter{cur: map[string]*proto.VisitSample{}} }

func first(h map[string][]string, k string) string {
	if v := h[k]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func (c *Counter) Line(line string, now time.Time) {
	var e entry
	if json.Unmarshal([]byte(line), &e) != nil || !strings.HasPrefix(e.Logger, "http.log.access") || e.Request.Host == "" {
		return
	}
	if e.Request.TLS == nil && e.Status/100 == 3 && strings.HasPrefix(first(e.RespHeaders, "Location"), "https://") {
		return
	}
	host, _, _ := strings.Cut(e.Request.Host, ":")
	c.mu.Lock()
	defer c.mu.Unlock()
	c.today(now)
	v := c.cur[host]
	if v == nil {
		v = &proto.VisitSample{Host: host, Paths: map[string]int64{}, Refs: map[string]int64{}}
		c.cur[host] = v
	}
	switch e.Status / 100 {
	case 2:
		v.S2++
	case 3:
		v.S3++
	case 4:
		v.S4++
	case 5:
		v.S5++
	}
	v.Bytes += e.Size
	ua := first(e.Request.Headers, "User-Agent")
	html := strings.HasPrefix(first(e.RespHeaders, "Content-Type"), "text/html")
	if e.Request.Method != "GET" || e.Status/100 != 2 || !html || ua == "" || bot.MatchString(ua) {
		return
	}
	v.Views++
	path, _, _ := strings.Cut(e.Request.URI, "?")
	v.Paths[path]++
	if r, err := url.Parse(first(e.Request.Headers, "Referer")); err == nil && r.Hostname() != "" && r.Hostname() != host {
		v.Refs[r.Hostname()]++
	}
	sum := sha256.Sum256(append(append(append([]byte{}, c.salt...), e.Request.ClientIP+"\x00"...), ua...))
	if c.seen[host] == nil {
		c.seen[host] = map[[8]byte]bool{}
	}
	if len(c.seen[host]) < visitorsExactBelow {
		c.seen[host][[8]byte(sum[:8])] = true
	}
}

func (c *Counter) today(now time.Time) {
	if d := now.UTC().Format("2006-01-02"); d != c.day {
		c.done = append(c.done, c.samples()...)
		c.day, c.salt, c.seen = d, make([]byte, 32), map[string]map[[8]byte]bool{}
		rand.Read(c.salt)
	}
}

func (c *Counter) samples() []proto.VisitSample {
	var out []proto.VisitSample
	for _, h := range slices.Sorted(maps.Keys(c.cur)) {
		v := c.cur[h]
		v.Visitors, v.Day = int64(len(c.seen[h])), c.day
		v.Paths, v.Refs = top(v.Paths), top(v.Refs)
		out = append(out, *v)
	}
	c.cur = map[string]*proto.VisitSample{}
	return out
}

func (c *Counter) Take(now time.Time) []proto.VisitSample {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.today(now)
	out := append(c.done, c.samples()...)
	c.done = nil
	return out
}

func Keep(ctx context.Context, setup func(context.Context) error, wait time.Duration) {
	for i := 0; ; i++ {
		err := setup(ctx)
		if err == nil {
			return
		}
		if i == 0 {
			slog.Warn("caddy access log: analytics off until it is set up", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func top(m map[string]int64) map[string]int64 {
	if len(m) <= 10 {
		return m
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int { return int(m[b] - m[a]) })
	out := map[string]int64{}
	for _, k := range keys[:10] {
		out[k] = m[k]
	}
	return out
}

func Follow(ctx context.Context, path string, poll time.Duration, fn func(line string)) {
	var f *os.File
	var rest string
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	buf := make([]byte, 64<<10)
	drain := func() {
		for {
			n, err := f.Read(buf)
			if n > 0 {
				lines := strings.Split(rest+string(buf[:n]), "\n")
				rest = lines[len(lines)-1]
				for _, l := range lines[:len(lines)-1] {
					fn(l)
				}
			}
			if err != nil || n == 0 {
				return
			}
		}
	}
	for {
		if f == nil {
			if g, err := os.Open(path); err == nil {
				f = g
				f.Seek(0, io.SeekEnd)
			}
		} else {
			drain()
			cur, err1 := os.Stat(path)
			open, err2 := f.Stat()
			switch {
			case err1 == nil && err2 == nil && !os.SameFile(cur, open):
				if g, err := os.Open(path); err == nil {
					f.Close()
					f, rest = g, ""
					drain()
				}
			case err2 == nil && open.Size() < mustOffset(f):
				f.Seek(0, io.SeekStart)
				rest = ""
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(poll):
		}
	}
}

func mustOffset(f *os.File) int64 {
	off, _ := f.Seek(0, io.SeekCurrent)
	return off
}
