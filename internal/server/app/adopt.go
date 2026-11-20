package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

type adoptItem struct {
	r       proto.Running
	svcID   string
	current string
}

func (a *App) adoptItems(ctx context.Context, q *db.Queries, reports map[string]proto.ReportData) ([]adoptItem, error) {
	var out []adoptItem
	for _, rep := range reports {
		for _, r := range rep.Running {
			it := adoptItem{r: r}
			svc, err := q.GetServiceByName(ctx, db.GetServiceByNameParams{Project: r.Project, Env: r.Env, Service: r.Service})
			if err == nil {
				it.svcID, it.current = svc.ID, svc.CurrentDeployID.String
			} else if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			out = append(out, it)
		}
	}
	return out, nil
}

func (a *App) AdoptView(ctx context.Context, reports map[string]proto.ReportData, missing []string) ([]string, error) {
	items, err := a.adoptItems(ctx, a.St.Q, reports)
	if err != nil {
		return nil, err
	}
	var differ, unknown []string
	for _, it := range items {
		switch {
		case it.svcID == "":
			unknown = append(unknown, fmt.Sprintf("%s runs %s-%s-%s, which this backup does not know. It is left alone.", it.r.Container, it.r.Project, it.r.Env, it.r.Service))
		case it.current != it.r.Deploy:
			last := it.current
			if last == "" {
				last = "nothing"
			}
			differ = append(differ, fmt.Sprintf("%s-%s runs deploy %s, the server last knew %s.", it.r.Project, it.r.Service, it.r.Deploy, last))
		}
	}
	slices.Sort(differ)
	slices.Sort(unknown)
	missing = slices.Clone(missing)
	slices.Sort(missing)
	lines := append(differ, unknown...)
	for _, m := range missing {
		lines = append(lines, m+" has not connected yet.")
	}
	return lines, nil
}

func (a *App) Adopt(ctx context.Context, reports map[string]proto.ReportData, actor string) error {
	return a.St.Tx(ctx, func(q *db.Queries) error {
		items, err := a.adoptItems(ctx, q, reports)
		if err != nil {
			return err
		}
		now := a.Now().UnixMilli()
		at := sql.NullInt64{Int64: a.Now().Add(Grace).UnixMilli(), Valid: true}
		if err := q.ExtendTrashServices(ctx, at); err != nil {
			return err
		}
		if err := q.ExtendTrashEnvironments(ctx, at); err != nil {
			return err
		}
		if err := q.ExtendTrashProjects(ctx, at); err != nil {
			return err
		}
		if since, err := q.GetSetting(ctx, "adopt.since"); err == nil {
			at, _ := strconv.ParseInt(since, 10, 64)
			if _, err := q.SupersedeUnfinishedBefore(ctx, db.SupersedeUnfinishedBeforeParams{UpdatedAt: now, CreatedAt: at}); err != nil {
				return err
			}
		}
		var took []string
		for _, it := range items {
			if it.svcID == "" || it.current == it.r.Deploy {
				continue
			}
			if _, err := q.GetDeploy(ctx, it.r.Deploy); errors.Is(err, sql.ErrNoRows) {
				err = q.InsertAdoptedDeploy(ctx, db.InsertAdoptedDeployParams{ID: it.r.Deploy, ServiceID: it.svcID, Image: it.r.Image,
					Container: it.r.Container, StartedAt: sql.NullInt64{Int64: now, Valid: true}, FinishedAt: sql.NullInt64{Int64: now, Valid: true},
					CreatedAt: now, UpdatedAt: now})
				if err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else if err := q.MarkDeployLive(ctx, db.MarkDeployLiveParams{Image: it.r.Image, Container: it.r.Container, UpdatedAt: now, ID: it.r.Deploy}); err != nil {
				return err
			}
			if err := q.SetCurrentDeploy(ctx, db.SetCurrentDeployParams{CurrentDeployID: nullStr(it.r.Deploy), UpdatedAt: now, ID: it.svcID}); err != nil {
				return err
			}
			took = append(took, fmt.Sprintf("%s-%s: %s", it.r.Project, it.r.Service, it.r.Deploy))
		}
		for _, k := range []string{"adopt.pending", "adopt.since"} {
			if err := q.DeleteSettingKey(ctx, k); err != nil {
				return err
			}
		}
		return Event(ctx, q, now, "panel.adopted", "panel", actor, map[string]any{"current": took})
	})
}
