package workers

import (
	"context"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type Prune struct {
	St  *store.Store
	Now func() time.Time
}

const keepEvents = 365 * 24 * time.Hour

func (p *Prune) Tick(ctx context.Context) error {
	now := p.Now().UTC()
	day := now.Format("2006-01-02")
	if now.Hour()*60+now.Minute() < 4*60+30 || setting(ctx, p.St.Q, "prune.day") == day {
		return nil
	}
	cut := now.Add(-keepEvents).UnixMilli()
	if err := p.St.Tx(ctx, func(q *db.Queries) error {
		if err := q.DeleteOldOutbox(ctx, cut); err != nil {
			return err
		}
		if err := q.DeleteExpiredSessions(ctx, now.UnixMilli()); err != nil {
			return err
		}
		return q.DeleteEventsBefore(ctx, cut)
	}); err != nil {
		return err
	}
	return p.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "prune.day", Value: day})
}
