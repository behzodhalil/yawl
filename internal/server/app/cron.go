package app

import (
	"context"
	"encoding/json"

	"github.com/behzodhalil/yawl/internal/cron"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type Scheduled struct {
	Schedule  string `json:"schedule"`
	Timezone  string `json:"timezone"`
	TimeoutMs int64  `json:"timeout_ms"`
}

type LiveCron struct {
	db.LiveCronServicesRow
	Scheduled
}

func LiveCrons(ctx context.Context, q *db.Queries) ([]LiveCron, error) {
	rows, err := q.LiveCronServices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]LiveCron, 0, len(rows))
	for _, r := range rows {
		s := Scheduled{Schedule: r.Schedule, Timezone: r.Timezone, TimeoutMs: r.TimeoutMs}
		if v, err := q.GetSetting(ctx, "cron.scheduled."+r.ID); err == nil {
			json.Unmarshal([]byte(v), &s)
		}
		if s.Timezone == "" {
			s.Timezone = cron.PanelZone()
		}
		out = append(out, LiveCron{r, s})
	}
	return out, nil
}
