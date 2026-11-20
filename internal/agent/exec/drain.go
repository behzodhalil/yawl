package exec

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/agent/secrets"
	"github.com/behzodhalil/yawl/internal/proto"
)

const (
	drainBatch   = 1000
	drainLineMax = 256 << 10 // Loki's own limit for one line
)

var drainKeepBytes = 16 << 20

var drainClient = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

type drainConfig struct {
	URL, User, TokenBox, Server string
	token                       string
}

type drainLine struct {
	labels map[string]string
	at     time.Time
	text   string
	stream string
	seq    uint64
}

type drainer struct {
	mu        sync.Mutex
	cfg       *drainConfig
	stop      context.CancelFunc
	fctx      context.Context
	following map[string]bool
	cursor    map[string]time.Time
	buf       []drainLine
	bytes     int
	next      uint64
}

func (r *drainer) holdLocked(l drainLine) {
	if len(l.text) > drainLineMax {
		l.text = l.text[:drainLineMax]
	}
	r.next++
	l.seq = r.next
	r.buf = append(r.buf, l)
	r.bytes += len(l.text)
	k := 0
	for r.bytes > drainKeepBytes && k < len(r.buf) {
		r.bytes -= len(r.buf[k].text)
		k++
	}
	r.buf = r.buf[k:]
}

func (r *drainer) sent(seq uint64) {
	k := 0
	for k < len(r.buf) && r.buf[k].seq <= seq {
		r.bytes -= len(r.buf[k].text)
		k++
	}
	r.buf = r.buf[k:]
}

func (r *drainer) config() *drainConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg
}

func (r *drainer) set(c *drainConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stop != nil {
		r.stop()
	}
	r.cfg, r.buf, r.bytes, r.following = c, nil, 0, map[string]bool{}
	r.fctx, r.stop = context.WithCancel(context.Background())
	if r.cursor == nil {
		r.cursor = map[string]time.Time{}
	}
}

func (d *Dispatcher) setDrain(a proto.LogDrainArgs) proto.Result {
	if a.Off {
		d.drain.set(nil)
		if d.DrainFile != "" {
			os.Remove(d.DrainFile)
		}
		return proto.Result{OK: true}
	}
	c, cause := d.openDrain(drainConfig{URL: strings.TrimRight(a.URL, "/"), User: a.User, TokenBox: a.TokenBox, Server: a.Server})
	if cause != "" {
		return proto.Result{Cause: cause}
	}
	if d.DrainFile != "" {
		js, _ := json.Marshal(c)
		if err := os.WriteFile(d.DrainFile, js, 0o600); err != nil {
			return proto.Result{Cause: err.Error()}
		}
	}
	d.drain.set(c)
	return proto.Result{OK: true}
}

func (d *Dispatcher) openDrain(c drainConfig) (*drainConfig, string) {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, "the drain's address is a Loki URL, like https://logs-prod-012.grafana.net"
	}
	if u.User != nil {
		return nil, "put the user and token in their own fields, not in the drain's address"
	}
	if c.TokenBox != "" {
		tok, cause := d.open(proto.SealedVar{Key: "drain token", Box: c.TokenBox})
		if cause != "" {
			return nil, cause
		}
		c.token = string(tok)
		if d.Keys != nil {
			d.Keys.Masker().Add(c.token)
		}
	}
	return &c, ""
}

func (d *Dispatcher) LoadDrain() {
	b, err := os.ReadFile(d.DrainFile)
	if err != nil {
		return
	}
	var c drainConfig
	if json.Unmarshal(b, &c) == nil {
		if cfg, cause := d.openDrain(c); cause == "" {
			d.drain.set(cfg)
		}
	}
}

func (d *Dispatcher) RunDrain(ctx context.Context) {
	start := time.Now()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			d.drain.set(nil)
			return
		case <-t.C:
		}
		if i%5 == 0 {
			d.superviseDrain(ctx, start, nil)
		}
		d.flushDrain(ctx)
	}
}

var keepEnv = map[string]bool{"PATH": true, "HOME": true, "HOSTNAME": true, "LANG": true, "LC_ALL": true, "TERM": true, "PWD": true, "TZ": true,
	"PORT": true, "NODE_ENV": true, "RAILS_ENV": true, "RACK_ENV": true, "APP_ENV": true}

func envMasker(st runtime.State) *secrets.Masker {
	m := &secrets.Masker{}
	for _, kv := range st.Env {
		if k, v, ok := strings.Cut(kv, "="); ok && !keepEnv[k] && !strings.HasSuffix(k, "_VERSION") {
			m.Add(v)
		}
	}
	return m
}

func (d *Dispatcher) superviseDrain(ctx context.Context, start time.Time, wg *sync.WaitGroup) {
	r := &d.drain
	if r.config() == nil {
		return
	}
	cs, err := d.rt.List(ctx, map[string]string{})
	if err != nil {
		return
	}
	listed := map[string]bool{}
	for _, c := range cs {
		listed[c.ID] = true
	}
	r.mu.Lock()
	for id := range r.cursor {
		if !listed[id] {
			delete(r.cursor, id)
		}
	}
	r.mu.Unlock()
	for _, c := range cs {
		if c.Labels["yawl.deploy"] == "" || c.Labels["yawl.release"] != "" || c.Labels["yawl.project"] == "" {
			continue
		}
		r.mu.Lock()
		if _, read := r.cursor[c.ID]; r.following[c.ID] || (read && !c.Running) {
			r.mu.Unlock()
			continue
		}
		cfg, fctx := r.cfg, r.fctx
		if cfg == nil {
			r.mu.Unlock()
			return
		}
		r.following[c.ID] = true
		since, seen := r.cursor[c.ID]
		if !seen {
			since = start
			r.cursor[c.ID] = start
		}
		r.mu.Unlock()
		st, err := d.rt.Inspect(ctx, c.ID)
		if err != nil {
			r.mu.Lock()
			delete(r.following, c.ID)
			if !seen {
				delete(r.cursor, c.ID)
			}
			r.mu.Unlock()
			continue
		}
		m := envMasker(st)
		labels := map[string]string{"yawl_project": c.Labels["yawl.project"], "yawl_env": c.Labels["yawl.env"], "yawl_service": c.Labels["yawl.service"], "yawl_server": cfg.Server}
		if wg != nil {
			wg.Add(1)
		}
		go func(id string) {
			if wg != nil {
				defer wg.Done()
			}
			d.rt.Follow(fctx, id, since, func(l runtime.LogLine) {
				text := m.Mask(l.Text)
				if d.Keys != nil {
					text = d.Keys.Masker().Mask(text)
				}
				r.mu.Lock()
				if fctx.Err() == nil {
					r.holdLocked(drainLine{labels: labels, at: l.Time, text: text, stream: l.Stream})
					r.cursor[id] = l.Time
				}
				r.mu.Unlock()
			})
			r.mu.Lock()
			delete(r.following, id)
			r.mu.Unlock()
		}(c.ID)
	}
}

func (d *Dispatcher) flushDrain(ctx context.Context) {
	r := &d.drain
	for {
		r.mu.Lock()
		cfg, n := r.cfg, min(len(r.buf), drainBatch)
		batch := slices.Clone(r.buf[:n])
		r.mu.Unlock()
		if cfg == nil || n == 0 {
			return
		}
		err := lokiPush(ctx, cfg, batch)
		var le *lokiError
		if err != nil && !(errors.As(err, &le) && le.status/100 == 4 && le.status != 429) {
			return
		}
		r.mu.Lock()
		if r.cfg == cfg {
			r.sent(batch[n-1].seq)
		}
		r.mu.Unlock()
	}
}

func lokiPush(ctx context.Context, cfg *drainConfig, lines []drainLine) error {
	type stream struct {
		Stream map[string]string `json:"stream"`
		Values [][2]string       `json:"values"`
	}
	by := map[string]*stream{}
	var order []string
	for _, l := range lines {
		std := cmp.Or(l.stream, "stdout")
		k := l.labels["yawl_project"] + "/" + l.labels["yawl_env"] + "/" + l.labels["yawl_service"] + "/" + std
		if by[k] == nil {
			labels := maps.Clone(l.labels)
			labels["yawl_stream"] = std
			by[k] = &stream{Stream: labels}
			order = append(order, k)
		}
		by[k].Values = append(by[k].Values, [2]string{strconv.FormatInt(l.at.UnixNano(), 10), l.text})
	}
	var body struct {
		Streams []*stream `json:"streams"`
	}
	for _, k := range order {
		body.Streams = append(body.Streams, by[k])
	}
	js, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.URL+"/loki/api/v1/push", bytes.NewReader(js))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = lokiDo(req, cfg)
	return err
}

func lokiDo(req *http.Request, cfg *drainConfig) ([]byte, error) {
	switch {
	case cfg.User != "":
		req.SetBasicAuth(cfg.User, cfg.token)
	case cfg.token != "":
		req.Header.Set("Authorization", "Bearer "+cfg.token)
	}
	res, err := drainClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("Loki at %s: %v", req.URL.Host, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode/100 != 2 {
		return nil, &lokiError{res.StatusCode, fmt.Sprintf("Loki at %s answered %s: %.200s", req.URL.Host, res.Status, strings.TrimSpace(string(b)))}
	}
	return b, nil
}

type lokiError struct {
	status int
	msg    string
}

func (e *lokiError) Error() string { return e.msg }

func (d *Dispatcher) queryDrain(ctx context.Context, a proto.LogsArgs, limit int) proto.Result {
	cfg := d.drain.config()
	if cfg == nil {
		return proto.Result{Cause: "this server sends its logs to no drain"}
	}
	q := fmt.Sprintf(`yawl_project=%s,yawl_env=%s,yawl_service=%s`, strconv.Quote(a.Project), strconv.Quote(a.Env), strconv.Quote(a.Service))
	if a.Stream != "" {
		q += ",yawl_stream=" + strconv.Quote(a.Stream)
	}
	q = "{" + q + "}"
	if a.Query != "" {
		q += " |= " + strconv.Quote(a.Query)
	}
	since := a.Since
	if since.IsZero() {
		since = time.Now().Add(-24 * time.Hour)
	}
	v := url.Values{"query": {q}, "start": {strconv.FormatInt(since.UnixNano(), 10)}, "end": {strconv.FormatInt(time.Now().UnixNano(), 10)},
		"limit": {strconv.Itoa(limit)}, "direction": {"backward"}}
	req, err := http.NewRequestWithContext(ctx, "GET", cfg.URL+"/loki/api/v1/query_range?"+v.Encode(), nil)
	if err != nil {
		return proto.Result{Cause: err.Error()}
	}
	b, err := lokiDo(req, cfg)
	if err != nil {
		return proto.Result{Cause: err.Error()}
	}
	var r struct {
		Data struct {
			Result []struct {
				Stream map[string]string `json:"stream"`
				Values [][2]string       `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return proto.Result{Cause: "Loki's answer: " + err.Error()}
	}
	var lines []proto.LogsLine
	for _, s := range r.Data.Result {
		std := s.Stream["yawl_stream"]
		if std != "stderr" {
			std = ""
		}
		for _, x := range s.Values {
			ns, _ := strconv.ParseInt(x[0], 10, 64)
			text := x[1]
			if d.Keys != nil {
				text = d.Keys.Masker().Mask(text)
			}
			lines = append(lines, proto.LogsLine{Time: time.Unix(0, ns), Text: text, Stream: std})
		}
	}
	slices.SortStableFunc(lines, func(x, y proto.LogsLine) int { return x.Time.Compare(y.Time) })
	return ok(proto.LogsData{Lines: lines, Drain: true})
}
