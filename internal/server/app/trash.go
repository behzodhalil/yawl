package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/behzodhalil/yawl/internal/server/notify"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

const Grace = 48 * time.Hour

type TrashKind string

const (
	TrashProject     TrashKind = "project"
	TrashEnvironment TrashKind = "environment"
	TrashService     TrashKind = "service"
)

type TrashItem struct {
	Kind      TrashKind `json:"kind"`
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	DeletedBy string    `json:"deleted_by"`
	DeletedAt int64     `json:"deleted_at"`
	PurgeAt   int64     `json:"purge_at"`
	Waiting   string    `json:"waiting,omitempty"`
}

func Stamp(ms int64) string { return time.UnixMilli(ms).UTC().Format("2 Jan 15:04") + " UTC" }

func (a *App) Delete(ctx context.Context, kind, id, actor string) error {
	now := a.Now()
	at, purge := now.UnixMilli(), now.Add(Grace).UnixMilli()
	null := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
	return a.St.Tx(ctx, func(q *db.Queries) error {
		var n int64
		var err error
		var label string
		switch kind {
		case "service":
			n, err = q.MarkServiceDeleted(ctx, db.MarkServiceDeletedParams{DeletedAt: null(at), PurgeAt: null(purge), DeletedBy: actor, ID: id})
			if s, e := q.GetService(ctx, id); e == nil {
				label = s.Project + "/" + s.Env + "/" + s.Name
			}
		case "environment":
			n, err = q.MarkEnvironmentDeleted(ctx, db.MarkEnvironmentDeletedParams{DeletedAt: null(at), PurgeAt: null(purge), DeletedBy: actor, ID: id})
			if err == nil && n > 0 {
				err = q.MarkEnvServicesDeleted(ctx, db.MarkEnvServicesDeletedParams{DeletedAt: null(at), PurgeAt: null(purge), DeletedBy: actor, EnvironmentID: id})
			}
			if e, er := q.GetEnvironmentNames(ctx, id); er == nil {
				label = e.Project + "/" + e.Name
			}
		case "project":
			n, err = q.MarkProjectDeleted(ctx, db.MarkProjectDeletedParams{DeletedAt: null(at), PurgeAt: null(purge), DeletedBy: actor, ID: id})
			if err == nil && n > 0 {
				if err = q.MarkProjectEnvsDeleted(ctx, db.MarkProjectEnvsDeletedParams{DeletedAt: null(at), PurgeAt: null(purge), DeletedBy: actor, ProjectID: id}); err == nil {
					err = q.MarkProjectServicesDeleted(ctx, db.MarkProjectServicesDeletedParams{DeletedAt: null(at), PurgeAt: null(purge), DeletedBy: actor, ProjectID: id})
				}
			}
			label, _ = q.GetProjectName(ctx, id)
		default:
			return fmt.Errorf("cannot delete a %s", kind)
		}
		if err != nil || n == 0 {
			return err
		}
		if err := q.SupersedeQueuedOfDeleted(ctx, at); err != nil {
			return err
		}
		return Announce(ctx, q, at, "trash.deleted", id, actor, Trashed(label, actor, LastGood(purge),
			fmt.Sprintf("%s was deleted by %s; it is purged on %s unless restored.", label, actor, Stamp(purge))))
	})
}

func (a *App) RestoreFromTrash(ctx context.Context, kind, id, actor string) error {
	now := a.Now().UnixMilli()
	return a.St.Tx(ctx, func(q *db.Queries) error {
		before, err := q.CountLiveServices(ctx)
		if err != nil {
			return err
		}
		at, err := q.DeletedAtOf(ctx, db.DeletedAtOfParams{Kind: kind, ID: id})
		if err != nil {
			return err
		}
		if at == 0 {
			return ErrNotInTrash
		}
		if purge, err := q.PurgeAtOf(ctx, id); err == nil && purge <= now {
			return ErrPurging
		}
		if err := heldByParent(ctx, q, kind, id); err != nil {
			return err
		}
		svcs, err := q.ServicesMarkedWith(ctx, db.ServicesMarkedWithParams{At: sql.NullInt64{Int64: at, Valid: true}, ID: id})
		if err != nil {
			return err
		}
		for _, s := range svcs {
			if s.Kind != string(spec.KindWeb) {
				continue
			}
			r, err := q.GetService(ctx, s.ID)
			if err != nil {
				return err
			}
			if err := CheckFreeHost(ctx, q, r.ServerID, r.Project, r.Env, r.Name); err != nil {
				return err
			}
		}
		for _, s := range svcs {
			if err := q.UnmarkService(ctx, s.ID); err != nil {
				return err
			}
			if !s.CurrentDeployID.Valid || s.UserStopped == 1 {
				continue
			}
			d, err := q.GetDeploy(ctx, s.CurrentDeployID.String)
			if err != nil {
				return err
			}
			if err := q.SupersedeQueued(ctx, db.SupersedeQueuedParams{UpdatedAt: now, ServiceID: s.ID}); err != nil {
				return err
			}
			if err := q.CreateRollbackDeploy(ctx, db.CreateRollbackDeployParams{ID: store.NewID("dep_"), ServiceID: s.ID, CommitSha: d.CommitSha,
				Subject: d.Subject, Image: d.Image, Digest: d.Digest, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
		}
		if err := q.UnmarkEnvironments(ctx, db.UnmarkEnvironmentsParams{At: sql.NullInt64{Int64: at, Valid: true}, ID: id}); err != nil {
			return err
		}
		if kind == "project" {
			if err := q.UnmarkProject(ctx, id); err != nil {
				return err
			}
		}
		label := id
		switch kind {
		case "service":
			if s, e := q.GetService(ctx, id); e == nil {
				label = s.Project + "/" + s.Env + "/" + s.Name
			}
		case "environment":
			if e, er := q.GetEnvironmentNames(ctx, id); er == nil {
				label = e.Project + "/" + e.Name
			}
		case "project":
			label, _ = q.GetProjectName(ctx, id)
		}
		if err := a.checkServices(ctx, q, before); err != nil {
			return err
		}
		return Announce(ctx, q, now, "trash.restored", id, actor, Restored(label, actor, fmt.Sprintf("%s was restored by %s.", label, actor)))
	})
}

var (
	ErrNotInTrash = errors.New("This is not in the trash.")
	ErrPurging    = errors.New("This is being purged and can no longer be restored.")
)

type HeldError string

func (e HeldError) Error() string { return string(e) }

func heldByParent(ctx context.Context, q *db.Queries, kind, id string) error {
	switch kind {
	case "service":
		p, err := q.ServiceParents(ctx, id)
		if err != nil {
			return err
		}
		if p.ProjectDeleted.Valid {
			return HeldError(fmt.Sprintf("Restore %s first.", p.Project))
		}
		if p.EnvDeleted.Valid {
			return HeldError(fmt.Sprintf("Restore %s/%s first.", p.Project, p.Env))
		}
	case "environment":
		p, err := q.EnvironmentParent(ctx, id)
		if err != nil {
			return err
		}
		if p.ProjectDeleted.Valid {
			return HeldError(fmt.Sprintf("Restore %s first.", p.Project))
		}
	}
	return nil
}

func (a *App) Trash(ctx context.Context) ([]TrashItem, error) {
	q := a.St.Q
	out := []TrashItem{}
	ps, err := q.TrashedProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		out = append(out, TrashItem{Kind: TrashProject, ID: p.ID, Label: p.Name, DeletedBy: p.DeletedBy, DeletedAt: p.DeletedAt.Int64, PurgeAt: p.PurgeAt.Int64, Waiting: waitingFor(p.OfflineServer)})
	}
	es, err := q.TrashedEnvironments(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range es {
		out = append(out, TrashItem{Kind: TrashEnvironment, ID: e.ID, Label: e.Project + "/" + e.Name, DeletedBy: e.DeletedBy, DeletedAt: e.DeletedAt.Int64, PurgeAt: e.PurgeAt.Int64,
			Waiting: waitingFor(e.OfflineServer)})
	}
	ss, err := q.TrashedServices(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range ss {
		w, _ := q.GetSetting(ctx, "trash.waiting."+s.ID)
		out = append(out, TrashItem{Kind: TrashService, ID: s.ID, Label: s.Project + "/" + s.Env + "/" + s.Name, DeletedBy: s.DeletedBy,
			DeletedAt: s.DeletedAt.Int64, PurgeAt: s.PurgeAt.Int64, Waiting: w})
	}
	return out, nil
}

func waitingFor(server string) string {
	if server == "" {
		return ""
	}
	return "waiting for " + server + " to be online to remove its containers"
}

func Announce(ctx context.Context, q *db.Queries, now int64, kind, subject, actor string, m notify.Message) error {
	evID := store.NewID("evt_")
	body, _ := json.Marshal(map[string]string{"text": m.Text})
	if err := q.InsertEvent(ctx, db.InsertEventParams{ID: evID, Kind: kind, SubjectID: subject, Actor: actor, BodyJson: string(body), CreatedAt: now, UpdatedAt: now}); err != nil {
		return err
	}
	return q.InsertOutbox(ctx, db.InsertOutboxParams{ID: store.NewID("obx_"), EventID: evID, Channel: "telegram", Body: m.Text, MessageJson: m.JSON(now),
		NextAttemptAt: now, CreatedAt: now, UpdatedAt: now})
}
