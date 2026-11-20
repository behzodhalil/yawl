package exec

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

const logFirstMax = 300

type logWatcher struct {
	mu        sync.Mutex
	rules     map[string][]proto.LogWatch
	following map[string]*logFollow
	cursor    map[string]time.Time
	hits      map[string]*proto.LogHit
}

type logFollow struct {
	key    string
	cancel context.CancelFunc
}

func (d *Dispatcher) setLogWatch(ws []proto.LogWatch) {
	rules := map[string][]proto.LogWatch{}
	for _, w := range ws {
		w.Text = strings.ToLower(w.Text)
		k := w.Project + "/" + w.Env + "/" + w.Service
		rules[k] = append(rules[k], w)
	}
	w := &d.logw
	w.mu.Lock()
	w.rules = rules
	w.mu.Unlock()
}

func (d *Dispatcher) TakeLogHits() []proto.LogHit {
	w := &d.logw
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []proto.LogHit
	for _, h := range w.hits {
		out = append(out, *h)
	}
	w.hits = nil
	slices.SortFunc(out, func(a, b proto.LogHit) int { return cmp.Compare(a.Rule, b.Rule) })
	return out
}

func (d *Dispatcher) RunLogWatch(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		d.superviseLogWatch(ctx, time.Now(), nil)
	}
}

func (d *Dispatcher) superviseLogWatch(ctx context.Context, start time.Time, wg *sync.WaitGroup) {
	w := &d.logw
	w.mu.Lock()
	if w.following == nil {
		w.following, w.cursor = map[string]*logFollow{}, map[string]time.Time{}
	}
	for id, f := range w.following {
		if len(w.rules[f.key]) == 0 {
			f.cancel()
			delete(w.following, id)
		}
	}
	none := len(w.rules) == 0
	w.mu.Unlock()
	if none {
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
	w.mu.Lock()
	for id := range w.cursor {
		if !listed[id] {
			delete(w.cursor, id)
		}
	}
	w.mu.Unlock()
	for _, c := range cs {
		if c.Labels["yawl.deploy"] == "" || c.Labels["yawl.release"] != "" || c.Labels["yawl.project"] == "" {
			continue
		}
		key := c.Labels["yawl.project"] + "/" + c.Labels["yawl.env"] + "/" + c.Labels["yawl.service"]
		w.mu.Lock()
		since, seen := w.cursor[c.ID]
		if w.following[c.ID] != nil || len(w.rules[key]) == 0 || (seen && !c.Running) {
			w.mu.Unlock()
			continue
		}
		if !seen {
			since = start
		}
		w.cursor[c.ID] = since
		fctx, cancel := context.WithCancel(ctx)
		f := &logFollow{key: key, cancel: cancel}
		w.following[c.ID] = f
		w.mu.Unlock()
		st, err := d.rt.Inspect(ctx, c.ID)
		if err != nil {
			w.mu.Lock()
			delete(w.following, c.ID)
			w.mu.Unlock()
			cancel()
			continue
		}
		m := envMasker(st)
		if wg != nil {
			wg.Add(1)
		}
		go func(id string) {
			if wg != nil {
				defer wg.Done()
			}
			defer cancel()
			d.rt.Follow(fctx, id, since, func(l runtime.LogLine) {
				text := m.Mask(l.Text)
				if d.Keys != nil {
					text = d.Keys.Masker().Mask(text)
				}
				w.mu.Lock()
				defer w.mu.Unlock()
				if fctx.Err() == nil {
					w.cursor[id] = l.Time
					w.countLocked(key, l.Stream, text)
				}
			})
			w.mu.Lock()
			if w.following[id] == f {
				delete(w.following, id)
			}
			w.mu.Unlock()
		}(c.ID)
	}
}

func (w *logWatcher) countLocked(key, stream, text string) {
	low := ""
	for _, r := range w.rules[key] {
		if r.Text == "" && stream != "stderr" {
			continue
		}
		if r.Text != "" {
			if low == "" {
				low = strings.ToLower(text)
			}
			if !strings.Contains(low, r.Text) {
				continue
			}
		}
		if w.hits == nil {
			w.hits = map[string]*proto.LogHit{}
		}
		h := w.hits[r.Rule]
		if h == nil {
			h = &proto.LogHit{Rule: r.Rule}
			w.hits[r.Rule] = h
		}
		h.Lines++
		if h.First == "" {
			h.First = strings.ToValidUTF8(text[:min(len(text), logFirstMax)], "")
		}
	}
}
