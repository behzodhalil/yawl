package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

var refRe = regexp.MustCompile(`^(?:([a-z][a-z0-9-]{0,30})/)?([a-z][a-z0-9-]{0,30})\.url$`)

type RefTarget struct {
	DB       db.Service
	EnvName  string
	ServerID string
	Cross    bool
}

func FindRef(ctx context.Context, q *db.Queries, envID, key, ref string) (RefTarget, error) {
	m := refRe.FindStringSubmatch(ref)
	if m == nil {
		return RefTarget{}, fmt.Errorf("%s: a reference names a database in this project: <db>.url or <env>/<db>.url", key)
	}
	envName, name := m[1], m[2]
	own, err := q.EnvProject(ctx, envID)
	if err != nil {
		return RefTarget{}, err
	}
	t := RefTarget{EnvName: own.Name, ServerID: own.ServerID}
	targetEnv := envID
	if envName != "" && envName != own.Name {
		e, err := q.EnvInProject(ctx, db.EnvInProjectParams{ProjectID: own.ProjectID, Name: envName})
		if errors.Is(err, sql.ErrNoRows) {
			return RefTarget{}, fmt.Errorf("%s refers to %s/%s: %s has no database %s.", key, envName, name, envName, name)
		} else if err != nil {
			return RefTarget{}, err
		}
		targetEnv, t.EnvName, t.ServerID, t.Cross = e.ID, envName, e.ServerID, true
	}
	t.DB, err = q.GetServiceInEnv(ctx, db.GetServiceInEnvParams{EnvironmentID: targetEnv, Name: name})
	if (err == nil && !spec.IsDatabase(spec.Kind(t.DB.Kind))) || errors.Is(err, sql.ErrNoRows) {
		if t.Cross {
			return RefTarget{}, fmt.Errorf("%s refers to %s/%s: %s has no database %s.", key, envName, name, envName, name)
		}
		return RefTarget{}, fmt.Errorf("%s is not a database in %s", name, own.Name)
	}
	if err == nil && t.Cross && t.DB.Kind != string(spec.KindPostgres) {
		return RefTarget{}, fmt.Errorf("%s refers to %s/%s, a %s: only Postgres is reached from another environment in this version.", key, envName, name, t.DB.Kind)
	}
	return t, err
}

func (a *App) ShareRef(ctx context.Context, t RefTarget, appName, appServer, appServerName, actor string) (string, error) {
	note := fmt.Sprintf("%s uses %s in %s over the private network.", appName, t.DB.Name, t.EnvName)
	q := a.St.Q
	if _, err := q.GetShare(ctx, db.GetShareParams{ServiceID: t.DB.ID, ServerID: appServer}); err == nil {
		return note, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	restart := "no restart"
	if t.DB.PrivatePort == 0 && appServer != t.ServerID {
		port, err := FreePrivatePort(ctx, q, t.ServerID, 0)
		if err != nil {
			return "", err
		}
		if err := q.SetPrivatePort(ctx, db.SetPrivatePortParams{PrivatePort: port, UpdatedAt: a.Now().UnixMilli(), ID: t.DB.ID}); err != nil {
			return "", err
		}
		restart = "it restarts once to listen on the private network"
	}
	if n, err := q.ActiveDeploys(ctx, t.DB.ID); err != nil {
		return "", err
	} else if n == 0 {
		if _, err := a.Deploy(ctx, t.DB.ID, "", actor); err != nil {
			return "", err
		}
	}
	return note + fmt.Sprintf(" %s shares its address with %s (%s).", t.DB.Name, appServerName, restart), nil
}

// FreePrivatePort is the port a database listens on for the private network on
// server: keep when no other database's private or outside port holds it, else
// the lowest free one from 15432. Either kind may be on 0.0.0.0 or
// the mesh address, and Docker refuses two binds of one port across both.
func FreePrivatePort(ctx context.Context, q *db.Queries, server string, keep int64, taken ...int64) (int64, error) {
	rows, err := q.PortsOnServer(ctx, server)
	if err != nil {
		return 0, err
	}
	used := map[int64]bool{}
	for _, p := range taken {
		used[p] = true
	}
	for _, r := range rows {
		used[r.PrivatePort], used[r.OutsidePort] = true, true
	}
	if keep != 0 && !used[keep] {
		return keep, nil
	}
	p := int64(15432)
	for used[p] {
		p++
	}
	return p, nil
}

func OnMesh(ctx context.Context, q *db.Queries, serverIDs ...string) error {
	for _, id := range serverIDs {
		s, err := q.GetServer(ctx, id)
		if err != nil {
			return err
		}
		if s.WgPubkey == "" || s.PrivateIp == "" {
			return fmt.Errorf("%s is not on the private network; rejoin it (yawl agent join) to use it.", s.Name)
		}
	}
	return nil
}
