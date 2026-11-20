package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

var ErrNoEarlier = errors.New("no earlier version to roll back to")

func (a *App) Rollback(ctx context.Context, serviceID, to, actor string) (string, string, error) {
	svc, err := a.St.Q.GetService(ctx, serviceID)
	if err != nil {
		return "", "", err
	}
	none := fmt.Errorf("%s-%s has %w.", svc.Project, svc.Name, ErrNoEarlier)
	if !svc.CurrentDeployID.Valid {
		return "", "", none
	}
	cur := svc.CurrentDeployID.String
	since, err := a.St.Q.LatestMoveStart(ctx, svc.EnvironmentID)
	if err != nil {
		return "", "", err
	}
	var target db.Deploy
	if to == "" {
		current, cerr := a.St.Q.GetDeploy(ctx, cur)
		if cerr != nil {
			return "", "", cerr
		}
		builtAt := current.CreatedAt
		if i := strings.LastIndex(current.Image, ":"); i >= 0 {
			if b, err := a.St.Q.GetDeploy(ctx, current.Image[i+1:]); err == nil {
				builtAt = b.CreatedAt
			}
		}
		target, err = a.St.Q.RollbackTarget(ctx, db.RollbackTargetParams{ServiceID: serviceID, CurrentImage: current.Image, BuiltAt: builtAt, Since: since, CurrentDigest: current.Digest})
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", none
		}
	} else {
		target, err = a.St.Q.GetDeploy(ctx, to)
		if err == nil {
			current, cerr := a.St.Q.GetDeploy(ctx, cur)
			if cerr != nil {
				return "", "", cerr
			}
			if target.ServiceID != serviceID || !RollbackOK(target, current, since) {
				return "", "", fmt.Errorf("%s is not an earlier live version of %s-%s.", to, svc.Project, svc.Name)
			}
		}
	}
	if err != nil {
		return "", "", err
	}
	id, now := store.NewID("dep_"), a.Now().UnixMilli()
	note := RollbackNote(ctx, a.St.Q, cur)
	return id, target.ID, a.St.Tx(ctx, func(q *db.Queries) error {
		if err := q.SupersedeQueued(ctx, db.SupersedeQueuedParams{UpdatedAt: now, ServiceID: serviceID}); err != nil {
			return err
		}
		if err := q.CreateRollbackDeploy(ctx, db.CreateRollbackDeployParams{ID: id, ServiceID: serviceID, CommitSha: target.CommitSha,
			Subject: target.Subject, Actor: actor, Image: target.Image, Digest: target.Digest, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		return Event(ctx, q, now, "deploy.queued", id, actor, map[string]string{"service": serviceID, "rollback_to": target.ID, "rollback_from": cur, "note": note})
	})
}

func RollbackNote(ctx context.Context, q *db.Queries, from string) string {
	steps, err := q.DeploySteps(ctx, from)
	if err != nil {
		return ""
	}
	released, snapshot := false, ""
	for _, s := range steps {
		if s.State != "ok" {
			continue
		}
		switch s.Step {
		case "release":
			released = true
		case "snapshot":
			snapshot = s.Output
		}
	}
	if !released {
		return ""
	}
	if snapshot == "" {
		return fmt.Sprintf("The database is unchanged: %s ran a release command.", from)
	}
	server := "the server"
	if d, err := q.GetDeploy(ctx, from); err == nil {
		if svc, err := q.GetService(ctx, d.ServiceID); err == nil {
			if srv, err := q.GetServer(ctx, svc.ServerID); err == nil {
				server = srv.Name
			}
		}
	}
	if paths := strings.Split(snapshot, "\n"); len(paths) > 1 {
		return fmt.Sprintf("The database is unchanged: %s ran a release command. Its snapshots are kept on %s at %s.", from, server, strings.Join(paths, " and "))
	}
	return fmt.Sprintf("The database is unchanged: %s ran a release command. Its snapshot is kept on %s at %s.", from, server, snapshot)
}

func RollbackOK(d, current db.Deploy, since int64) bool {
	return current.ID != "" && d.State == "live" && d.Image != "" && d.AutoRolledBack == 0 && d.ID != current.ID && d.Image != current.Image && d.CreatedAt >= since &&
		!(d.Digest != "" && d.Digest == current.Digest)
}
