package app

import (
	"context"
	"fmt"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func (a *App) Restart(ctx context.Context, serviceID, actor string) (string, error) {
	svc, err := a.St.Q.GetService(ctx, serviceID)
	if err != nil {
		return "", err
	}
	if svc.DeletedAt.Valid {
		return "", fmt.Errorf("%s-%s is in the trash; restore it first.", svc.Project, svc.Name)
	}
	if !svc.CurrentDeployID.Valid {
		return "", fmt.Errorf("%s-%s has not been deployed yet; deploy it first.", svc.Project, svc.Name)
	}
	cur, err := a.St.Q.GetDeploy(ctx, svc.CurrentDeployID.String)
	if err != nil {
		return "", err
	}
	id, now := store.NewID("dep_"), a.Now().UnixMilli()
	return id, a.St.Tx(ctx, func(q *db.Queries) error {
		if err := q.SupersedeQueued(ctx, db.SupersedeQueuedParams{UpdatedAt: now, ServiceID: serviceID}); err != nil {
			return err
		}
		if err := q.ClearUserStopped(ctx, db.ClearUserStoppedParams{UpdatedAt: now, ID: serviceID}); err != nil {
			return err
		}
		if err := q.CreateRestartDeploy(ctx, db.CreateRestartDeployParams{ID: id, ServiceID: serviceID, CommitSha: cur.CommitSha,
			Subject: cur.Subject, Actor: actor, Image: cur.Image, Digest: cur.Digest, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		return Event(ctx, q, now, "deploy.queued", id, actor, map[string]string{"service": serviceID, "restart": cur.ID})
	})
}

func (a *App) Stop(ctx context.Context, serviceID, actor string) error {
	svc, err := a.St.Q.GetService(ctx, serviceID)
	if err != nil {
		return err
	}
	if svc.DeletedAt.Valid {
		return fmt.Errorf("%s-%s is in the trash already.", svc.Project, svc.Name)
	}
	now := a.Now().UnixMilli()
	return a.St.Tx(ctx, func(q *db.Queries) error {
		if err := q.SupersedeQueued(ctx, db.SupersedeQueuedParams{UpdatedAt: now, ServiceID: serviceID}); err != nil {
			return err
		}
		if err := q.SetUserStopped(ctx, db.SetUserStoppedParams{UserStopped: 1, UpdatedAt: now, ID: serviceID}); err != nil {
			return err
		}
		return Event(ctx, q, now, "service.stopped", serviceID, actor, map[string]string{"service": serviceID})
	})
}

func (a *App) Start(ctx context.Context, serviceID, actor string) (string, error) {
	return a.Restart(ctx, serviceID, actor)
}
