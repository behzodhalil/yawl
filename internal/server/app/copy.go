package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

type CopyPlan struct {
	Lines          []string    `json:"lines"`
	ToPubkey       string      `json:"to_pubkey,omitempty"`
	RecoveryPubkey string      `json:"recovery_pubkey,omitempty"`
	Vars           []SealedRow `json:"vars"`
}

type copyFacts struct {
	svc      db.GetServiceRow
	toEnv    string
	toServer string
	same     bool
	vars     []db.EnvVar
	label    string
}

func (a *App) copyFacts(ctx context.Context, serviceID, project, env string) (copyFacts, error) {
	var f copyFacts
	svc, err := a.St.Q.GetService(ctx, serviceID)
	if err != nil {
		return f, err
	}
	to, err := a.St.Q.GetEnvironmentByName(ctx, db.GetEnvironmentByNameParams{Project: project, Env: env})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && to.DeletedAt.Valid) {
		return f, fmt.Errorf("there is no environment %s/%s", project, env)
	} else if err != nil {
		return f, err
	}
	f.label = project + "/" + env
	for _, e := range []string{to.ID, svc.EnvironmentID} {
		if _, err := a.St.Q.ActiveMove(ctx, e); err == nil {
			return f, errors.New("an environment is moving between servers; copy when it has arrived")
		} else if !errors.Is(err, sql.ErrNoRows) {
			return f, err
		}
	}
	if to.ID == svc.EnvironmentID {
		return f, errors.New("that is the environment it is in")
	}
	if to.PreviewOf != "" {
		return f, fmt.Errorf("%s is a preview environment; copy to the environment it previews", f.label)
	}
	if old, err := a.St.Q.GetServiceInEnv(ctx, db.GetServiceInEnvParams{EnvironmentID: to.ID, Name: svc.Name}); err == nil && old.DeletedAt.Valid {
		return f, fmt.Errorf("%s has a service %s in the trash; restore it, or copy once it is purged", f.label, svc.Name)
	} else if err == nil {
		return f, fmt.Errorf("%s has a service %s already", f.label, svc.Name)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return f, err
	}
	if svc.Kind == string(spec.KindWeb) {
		if err := CheckFreeHost(ctx, a.St.Q, to.ServerID, project, env, svc.Name); err != nil {
			return f, err
		}
	}
	f.svc, f.toEnv, f.toServer, f.same = svc, to.ID, to.ServerID, to.ServerID == svc.ServerID
	if !spec.IsDatabase(spec.Kind(svc.Kind)) {
		if f.vars, err = a.St.Q.ServiceOwnVars(ctx, svc.ID); err != nil {
			return f, err
		}
	}
	return f, nil
}

func (a *App) PlanCopy(ctx context.Context, serviceID, project, env string) (CopyPlan, error) {
	f, err := a.copyFacts(ctx, serviceID, project, env)
	if err != nil {
		return CopyPlan{}, err
	}
	p := CopyPlan{Lines: []string{fmt.Sprintf("copy %s to %s, not deployed until you deploy it", f.svc.Name, f.label)}, Vars: []SealedRow{}}
	if spec.IsDatabase(spec.Kind(f.svc.Kind)) {
		p.Lines = append(p.Lines, f.svc.Name+" starts empty, with its own password; restore a backup into it for its data")
	}
	for _, v := range f.vars {
		if strings.Contains(v.Ref, "/") {
			p.Lines = append(p.Lines, fmt.Sprintf("%s refers to another environment's database and is not copied; set it again", v.Key))
			continue
		}
		if v.Ref != "" {
			target, _, _ := strings.Cut(v.Ref, ".")
			if _, err := a.St.Q.GetServiceInEnv(ctx, db.GetServiceInEnvParams{EnvironmentID: f.toEnv, Name: target}); err != nil {
				p.Lines = append(p.Lines, fmt.Sprintf("%s refers to %s, which %s does not have", v.Key, target, f.label))
			}
			continue
		}
		if !f.same {
			p.Vars = append(p.Vars, SealedRow{v.ID, v.Key, f.svc.Name, v.RecoveryBox, v.UpdatedAt})
		}
	}
	if !f.same {
		srv, err := a.St.Q.GetServer(ctx, f.toServer)
		if err != nil {
			return CopyPlan{}, err
		}
		p.ToPubkey = srv.AgentPubkey
		p.RecoveryPubkey, _ = a.St.Q.GetSetting(ctx, "recovery.pubkey")
		if len(p.Vars) > 0 {
			p.Lines = append(p.Lines, fmt.Sprintf("%d variable(s) are sealed again in your browser for %s", len(p.Vars), srv.Name))
		}
	}
	return p, nil
}

func (a *App) CopyService(ctx context.Context, serviceID, project, env string, boxes map[string]Reseal, toPubkey string, move bool, actor string) (string, error) {
	f, err := a.copyFacts(ctx, serviceID, project, env)
	if err != nil {
		return "", err
	}
	if len(boxes) > 0 {
		srv, err := a.St.Q.GetServer(ctx, f.toServer)
		if err != nil {
			return "", err
		}
		for _, v := range f.vars {
			if b, ok := boxes[v.ID]; ok && (b.UpdatedAt != v.UpdatedAt || toPubkey != srv.AgentPubkey) {
				return "", fmt.Errorf("%s or %s's key changed since the plan; reload the page and try again", v.Key, srv.Name)
			}
		}
	}
	if move {
		ds, err := a.St.Q.ListDomains(ctx, serviceID)
		if err != nil {
			return "", err
		}
		if len(ds) > 0 {
			return "", fmt.Errorf("%s serves %s: remove its domains, move it, then add them to the copy", f.svc.Name, ds[0].Host)
		}
	}
	s, now, id := f.svc, a.Now().UnixMilli(), ""
	err = a.St.Tx(ctx, func(q *db.Queries) error {
		if !move {
			before, err := q.CountLiveServices(ctx)
			if err != nil {
				return err
			}
			if a.Limits.Services > 0 && before >= int64(a.Limits.Services) {
				return OverLimit(a.Limits.Services, "services")
			}
		}
		var err error
		if id, err = q.UpsertService(ctx, db.UpsertServiceParams{ID: store.NewID("svc_"), EnvironmentID: f.toEnv, Name: s.Name, Kind: s.Kind, Repo: s.Repo, Branch: s.Branch,
			Port: s.Port, HealthPath: s.HealthPath, HealthTimeoutMs: s.HealthTimeoutMs, DrainTimeoutMs: s.DrainTimeoutMs, AutoDeploy: s.AutoDeploy,
			Version: s.Version, BackupAt: s.BackupAt, ReleaseJson: s.ReleaseJson, ReleaseTimeoutMs: s.ReleaseTimeoutMs, OutsidePort: 0,
			AllowJson: s.AllowJson, Schedule: s.Schedule, Timezone: s.Timezone, CommandJson: s.CommandJson, TimeoutMs: s.TimeoutMs,
			VolumesJson: s.VolumesJson, Cpu: s.Cpu, Memory: s.Memory, Image: s.Image, Backup: s.Backup, Pitr: s.Pitr, Builder: s.Builder,
			Noindex: s.Noindex, NoPreview: s.NoPreview, Www: s.Www, Root: s.Root, WatchJson: s.WatchJson, Replicas: s.Replicas, TagsJson: s.TagsJson, KeepJson: s.KeepJson,
			CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if s.BasicAuth != "" {
			if err := q.SetBasicAuth(ctx, db.SetBasicAuthParams{BasicAuth: s.BasicAuth, UpdatedAt: now, ID: id}); err != nil {
				return err
			}
		}
		for _, v := range f.vars {
			if strings.Contains(v.Ref, "/") {
				continue
			}
			agent := v.AgentBox
			if v.Ref == "" && !f.same {
				if agent = boxes[v.ID].AgentBox; agent == "" {
					return fmt.Errorf("%s is sealed for this server only; open it with the recovery key in the dashboard to copy it", v.Key)
				}
			}
			if err := q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: store.NewID("var_"), EnvironmentID: f.toEnv, ServiceID: id, Key: v.Key,
				AgentBox: agent, RecoveryBox: v.RecoveryBox, Build: v.Build, Ref: v.Ref, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
		}
		return Event(ctx, q, now, "service.copied", id, actor, map[string]any{"from": s.ID, "to": f.label, "move": move})
	})
	if err != nil {
		return "", err
	}
	if move {
		if err := a.Delete(ctx, "service", serviceID, actor); err != nil {
			return id, fmt.Errorf("copied, but the original stays: %w", err)
		}
	}
	return id, nil
}
