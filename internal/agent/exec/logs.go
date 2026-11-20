package exec

import (
	"context"
	"slices"
	"strings"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

const (
	logsDefault = 200
	logsMax     = 1000
)

func (d *Dispatcher) logs(ctx context.Context, a proto.LogsArgs) proto.Result {
	limit := a.Limit
	if limit <= 0 {
		limit = logsDefault
	}
	limit = min(limit, logsMax)
	if a.Drain {
		return d.queryDrain(ctx, a, limit)
	}
	mask := func(s string) string { return s }
	if d.Keys != nil {
		for _, v := range a.Vars {
			d.Keys.Open(v.Box)
		}
		mask = d.Keys.Masker().Mask
	}
	cs, err := d.rt.List(ctx, map[string]string{"yawl.project": a.Project, "yawl.env": a.Env, "yawl.service": a.Service})
	if err != nil {
		return proto.Result{Cause: err.Error()}
	}
	q := strings.ToLower(a.Query)
	oldest := !a.Since.IsZero()
	var lines []proto.LogsLine
	for _, c := range cs {
		if c.Labels["yawl.release"] != "" {
			continue
		}
		st, err := d.rt.Inspect(ctx, c.ID)
		if err != nil {
			continue
		}
		own := envMasker(st)
		var kept []proto.LogsLine
		err = d.rt.Logs(ctx, c.ID, a.Since, func(l runtime.LogLine) {
			if a.Stream != "" && l.Stream != a.Stream {
				return
			}
			text := mask(own.Mask(l.Text))
			if q != "" && !strings.Contains(strings.ToLower(text), q) {
				return
			}
			if oldest && len(kept) >= limit {
				return
			}
			kept = append(kept, proto.LogsLine{Time: l.Time, Deploy: c.Labels["yawl.deploy"], Text: text, Stream: l.Stream})
			if len(kept) > 2*limit {
				kept = slices.Delete(kept, 0, len(kept)-limit)
			}
		})
		if err != nil {
			return proto.Result{Cause: err.Error()}
		}
		lines = append(lines, kept...)
	}
	slices.SortStableFunc(lines, func(x, y proto.LogsLine) int { return x.Time.Compare(y.Time) })
	if len(lines) > limit && oldest {
		lines = lines[:limit]
	} else if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return ok(proto.LogsData{Lines: lines, Drain: d.drain.config() != nil})
}
