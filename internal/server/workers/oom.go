package workers

import (
	"context"
	"strconv"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

const oomKey, oomQuiet = "metrics.oom.", time.Hour

func recordOOMs(ctx context.Context, q *db.Queries, s proto.Sample, byName map[string]string, now time.Time) error {
	for _, o := range s.OOMs {
		label := o.Project + "/" + o.Env + "/" + o.Service
		id := byName[label]
		if id == "" || o.Kills == 0 {
			continue
		}
		open := setting(ctx, q, oomKey+id) != ""
		if err := q.SetSetting(ctx, db.SetSettingParams{Key: oomKey + id, Value: strconv.FormatInt(s.At, 10)}); err != nil {
			return err
		}
		if open {
			continue
		}
		text, limit, kind := label+" ran out of memory and was restarted.", "", spec.Kind("")
		if svc, err := q.GetService(ctx, id); err == nil {
			kind = spec.Kind(svc.Kind)
			if svc.Memory > 0 {
				limit = sizeWords(svc.Memory)
				text = label + " ran out of its " + limit + " memory limit and was restarted."
			}
		}
		if err := announceIncident(ctx, q, now.UnixMilli(), "service.oom", id, map[string]int{"kills": o.Kills}, app.ServiceOOM(label, kind, limit, o.Kills, text), "oom:"+id, "open", label, false); err != nil {
			return err
		}
	}
	return nil
}

func (w *Metrics) closeOOMs(ctx context.Context, label map[string]string, now time.Time) error {
	open, err := w.St.Q.SettingsWithPrefix(ctx, oomKey)
	if err != nil {
		return err
	}
	for _, o := range open {
		last, _ := strconv.ParseInt(o.Value, 10, 64)
		if now.UnixMilli()-last < oomQuiet.Milliseconds() {
			continue
		}
		id := o.Key[len(oomKey):]
		name := label[id]
		if name == "" {
			name = id
		}
		if err := w.St.Tx(ctx, func(q *db.Queries) error {
			if err := q.DeleteSettingKey(ctx, o.Key); err != nil {
				return err
			}
			return announceIncident(ctx, q, now.UnixMilli(), "service.oom", id, nil, app.ServiceOOMOver(name, name+" has not run out of memory for an hour."), "oom:"+id, "close", name, false)
		}); err != nil {
			return err
		}
	}
	return nil
}

func sizeWords(b int64) string {
	if b >= 1e9 {
		return strconv.FormatFloat(float64(b)/1e9, 'f', -1, 64) + " GB"
	}
	return strconv.FormatFloat(float64(b)/1e6, 'f', -1, 64) + " MB"
}
