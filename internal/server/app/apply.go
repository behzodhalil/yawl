package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/behzodhalil/yawl/internal/server/plan"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

func dbName(project, env, name string) db.GetServiceByNameParams {
	return db.GetServiceByNameParams{Project: project, Env: env, Service: name}
}

func (a *App) Apply(ctx context.Context, f spec.File, actor string, dry bool) ([]plan.Change, error) {
	return a.applyIn(ctx, func(*db.Queries) (spec.File, error) { return f, nil }, actor, dry)
}

func (a *App) ApplyEdit(ctx context.Context, project, env string, edit func(*spec.File) error, actor string, dry bool) ([]plan.Change, error) {
	return a.applyIn(ctx, func(q *db.Queries) (spec.File, error) {
		f, err := currentFile(ctx, q, project, env)
		if err != nil {
			return f, err
		}
		return f, edit(&f)
	}, actor, dry)
}

func (a *App) applyIn(ctx context.Context, file func(*db.Queries) (spec.File, error), actor string, dry bool) ([]plan.Change, error) {
	var changes []plan.Change
	err := a.St.Tx(ctx, func(q *db.Queries) error {
		f, err := file(q)
		if err != nil {
			return err
		}
		srv, err := q.GetServerByName(ctx, f.Server)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("server %q has not joined; run `yawl server join-token` and join it first", f.Server)
		} else if err != nil {
			return err
		}
		if p, err := q.TrashedProjectByName(ctx, f.Project); err == nil {
			return fmt.Errorf("%s is in the trash until %s; restore it, or wait.", f.Project, Stamp(p.Int64))
		}
		for _, s := range f.Services {
			p, err := q.TrashedServiceByName(ctx, db.TrashedServiceByNameParams{Project: f.Project, Env: f.Environment, Service: s.Name})
			if err == nil {
				return fmt.Errorf("%s/%s/%s is in the trash until %s; restore it, or wait.", f.Project, f.Environment, s.Name, Stamp(p.Int64))
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if err := sameDockerNames(ctx, q, f); err != nil {
			return err
		}
		cur, ids := plan.Current{}, map[string]string{}
		env, err := q.GetEnvironmentByName(ctx, db.GetEnvironmentByNameParams{Project: f.Project, Env: f.Environment})
		if err == nil && env.DeletedAt.Valid {
			return fmt.Errorf("%s/%s is in the trash until %s; restore it, or wait.", f.Project, f.Environment, Stamp(env.PurgeAt.Int64))
		}
		switch {
		case err == nil:
			if env.ServerID != srv.ID {
				return fmt.Errorf("environment %s lives on %s; move it from the dashboard first", f.Environment, env.ServerName)
			}
			cur.EnvExists, cur.Server, cur.Preview, cur.PreviewDomain = true, env.ServerName, env.Preview == 1, env.PreviewDomain
			rows, err := q.ListServicesInEnv(ctx, env.ID)
			if err != nil {
				return err
			}
			for _, r := range rows {
				sv, err := ToSpec(q, ctx, r)
				if err != nil {
					return err
				}
				cur.Services = append(cur.Services, sv)
				ids[r.Name] = r.ID
			}
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		for _, h := range cur.Services { // a major upgrade needs dump and restore, not a new image tag
			for _, w := range f.Services {
				if h.Name == w.Name && h.Kind != w.Kind {
					return fmt.Errorf("%s is a %s service; changing a service's kind is not in version 1.", w.Name, h.Kind)
				}
				if h.Name == w.Name && spec.IsDatabase(h.Kind) && w.Version != h.Version {
					return fmt.Errorf("%s: changing a database's version is not in version 1.", w.Name)
				}
			}
		}
		for _, w := range f.Services {
			if w.Kind != spec.KindWeb || slices.ContainsFunc(cur.Services, func(c spec.Service) bool { return c.Name == w.Name }) {
				continue
			}
			if err := CheckFreeHost(ctx, q, srv.ID, f.Project, f.Environment, w.Name); err != nil {
				return err
			}
		}
		panelDomain, _ := q.GetSetting(ctx, "panel.domain")
		for _, w := range f.Services {
			for _, h := range w.Domains {
				host, _ := spec.SplitDomain(h)
				if host == panelDomain {
					return fmt.Errorf("%s is the panel's domain.", host)
				}
				if n, err := q.StatusPagesOnHost(ctx, host); err != nil {
					return err
				} else if n > 0 {
					return fmt.Errorf("%s is a status page's domain.", host)
				}
				o, err := q.DomainOwner(ctx, h)
				if err == nil && o.Project == f.Project && o.Env == f.Environment && givesUp(f, o.Service, h) {
					continue
				}
				if err == nil && (o.Project != f.Project || o.Env != f.Environment || o.Service != w.Name) {
					return fmt.Errorf("%s is already a domain of %s-%s.", h, o.Project, o.Service)
				} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
		}
		inFile := map[string]bool{}
		for _, w := range f.Services {
			inFile[w.Name] = true
		}
		for _, w := range f.Services {
			for _, d := range w.Domains {
				host, path := spec.SplitDomain(d)
				others, err := q.DomainsOnHost(ctx, host)
				if err != nil {
					return err
				}
				for _, o := range others {
					if o.Host == d || (o.Project == f.Project && o.Env == f.Environment && inFile[o.Service]) {
						continue
					}
					if _, op := spec.SplitDomain(o.Host); o.ServerID != srv.ID {
						return fmt.Errorf("%s: %s is served from %s by %s-%s; a host reaches one server, so its paths must be there too.", w.Name, host, o.Server, o.Project, o.Service)
					} else if spec.PathsNest(path, op) {
						return fmt.Errorf("%s: %s overlaps %s of %s-%s; one host cannot have a path inside another.", w.Name, d, o.Host, o.Project, o.Service)
					}
				}
			}
		}
		for _, w := range f.Services {
			if w.OutsidePort == 0 {
				continue
			}
			users, err := q.OutsidePortUsers(ctx, db.OutsidePortUsersParams{ServerID: srv.ID, OutsidePort: int64(w.OutsidePort)})
			if err != nil {
				return err
			}
			for _, u := range users {
				if u.Project != f.Project || u.Env != f.Environment || u.Service != w.Name {
					return fmt.Errorf("%s: outside_port %d is already used by %s-%s on %s.", w.Name, w.OutsidePort, u.Project, u.Service, f.Server)
				}
			}
			priv, err := q.PrivatePortUsers(ctx, db.PrivatePortUsersParams{ServerID: srv.ID, PrivatePort: int64(w.OutsidePort)})
			if err != nil {
				return err
			}
			for _, u := range priv {
				return fmt.Errorf("%s: outside_port %d is the private network port of %s-%s on %s.", w.Name, w.OutsidePort, u.Project, u.Service, f.Server)
			}
		}
		if f.Preview {
			if o, err := q.PreviewEnvInProject(ctx, f.Project); err == nil && o.Name != f.Environment {
				return fmt.Errorf("only one environment of %s has preview: true; %s already has it", f.Project, o.Name)
			} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		changes = plan.Diff(cur, f)
		warns, err := a.keepDeletes(ctx, q, ids, cur.Services, f)
		if err != nil {
			return err
		}
		changes = append(changes, warns...)
		if w, err := overcommit(ctx, q, srv, f, cur.Services); err != nil {
			return err
		} else if w != nil {
			changes = append(changes, *w)
		}
		if dry {
			return nil
		}
		now := a.Now().UnixMilli()
		before, err := q.CountLiveServices(ctx)
		if err != nil {
			return err
		}
		prj, err := q.UpsertProject(ctx, db.UpsertProjectParams{ID: store.NewID("prj_"), Name: f.Project, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return err
		}
		envID, err := q.UpsertEnvironment(ctx, db.UpsertEnvironmentParams{ID: store.NewID("env_"), ProjectID: prj, Name: f.Environment, ServerID: srv.ID, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return err
		}
		if err := q.SetEnvironmentPreview(ctx, db.SetEnvironmentPreviewParams{Preview: boolInt(f.Preview), PreviewDomain: f.PreviewDomain, UpdatedAt: now, ID: envID}); err != nil {
			return err
		}
		for _, s := range f.Services {
			id, err := q.UpsertService(ctx, db.UpsertServiceParams{
				ID: store.NewID("svc_"), EnvironmentID: envID, Name: s.Name, Kind: string(s.Kind), Repo: s.Repo, Branch: s.Branch,
				Port: int64(s.Port), HealthPath: s.HealthPath, HealthTimeoutMs: s.HealthTimeout.Milliseconds(),
				DrainTimeoutMs: s.DrainTimeout.Milliseconds(), AutoDeploy: boolInt(s.AutoDeploy), Version: int64(s.Version), BackupAt: s.BackupAt,
				ReleaseJson: list(s.Release), ReleaseTimeoutMs: s.ReleaseTimeout.Milliseconds(), OutsidePort: int64(s.OutsidePort), AllowJson: list(s.Allow),
				Schedule: s.Schedule, Timezone: s.Timezone, CommandJson: list(s.Command), TimeoutMs: s.Timeout.Milliseconds(), VolumesJson: volumes(s.Volumes),
				Cpu: float64(s.CPU), Memory: int64(s.Memory), Image: s.Image, Backup: boolInt(s.BackupOn()), Pitr: boolInt(s.PITR), Builder: s.Builder, Root: s.Root, Noindex: boolInt(s.NoIndex), NoPreview: boolInt(!s.PreviewOn()), Www: s.WWW, WatchJson: jsonList(s.Watch), Replicas: int64(max(s.Replicas, 1)), TagsJson: jsonList(s.Tags), KeepJson: keepJSON(s.Keep), StepsJson: stepsJSON(s.Steps), CreatedAt: now, UpdatedAt: now,
			})
			if err != nil {
				return err
			}
			if i := slices.IndexFunc(cur.Services, func(c spec.Service) bool { return c.Name == s.Name }); i >= 0 && plan.NeedsDeploy(cur.Services[i], s) {
				if err := q.SetServiceSettingsAt(ctx, db.SetServiceSettingsAtParams{SettingsAt: now, ID: id}); err != nil {
					return err
				}
			}
			if i := slices.IndexFunc(cur.Services, func(c spec.Service) bool { return c.Name == s.Name }); i >= 0 && s.BackupOn() && !cur.Services[i].BackupOn() {
				if err := q.SetSetting(ctx, db.SetSettingParams{Key: "backup.since." + id, Value: strconv.FormatInt(now, 10)}); err != nil {
					return err
				}
			}
			if err := a.syncDomains(ctx, q, id, srv.ID, s.Domains, now); err != nil {
				return err
			}
		}
		if err := a.checkServices(ctx, q, before); err != nil {
			return err
		}
		return Event(ctx, q, now, "spec.applied", envID, actor, changes)
	})
	return changes, err
}

func (a *App) CurrentFile(ctx context.Context, project, env string) (spec.File, error) {
	return currentFile(ctx, a.St.Q, project, env)
}

func currentFile(ctx context.Context, q *db.Queries, project, env string) (spec.File, error) {
	e, err := q.GetEnvironmentByName(ctx, db.GetEnvironmentByNameParams{Project: project, Env: env})
	if err == nil && e.DeletedAt.Valid {
		err = sql.ErrNoRows
	}
	if err != nil {
		return spec.File{}, err
	}
	f := spec.File{Project: project, Environment: env, Server: e.ServerName, Preview: e.Preview == 1, PreviewDomain: e.PreviewDomain, Services: []spec.Service{}}
	rows, err := q.ListServicesInEnv(ctx, e.ID)
	if err != nil {
		return f, err
	}
	for _, r := range rows {
		s, err := ToSpec(q, ctx, r)
		if err != nil {
			return f, err
		}
		f.Services = append(f.Services, s)
	}
	return f, nil
}

func ToSpec(q *db.Queries, ctx context.Context, r db.Service) (spec.Service, error) {
	s := spec.Service{Name: r.Name, Kind: spec.Kind(r.Kind), Repo: r.Repo, Branch: r.Branch, Port: int(r.Port),
		HealthPath: r.HealthPath, HealthTimeout: time.Duration(r.HealthTimeoutMs) * time.Millisecond,
		DrainTimeout: time.Duration(r.DrainTimeoutMs) * time.Millisecond, AutoDeploy: r.AutoDeploy == 1, Version: int(r.Version), BackupAt: r.BackupAt,
		ReleaseTimeout: time.Duration(r.ReleaseTimeoutMs) * time.Millisecond, OutsidePort: int(r.OutsidePort),
		Schedule: r.Schedule, Timezone: r.Timezone, Timeout: time.Duration(r.TimeoutMs) * time.Millisecond,
		CPU: spec.Cores(r.Cpu), Memory: spec.Size(r.Memory), Image: r.Image, PITR: r.Pitr == 1, Builder: r.Builder, Root: r.Root, NoIndex: r.Noindex == 1, Preview: previewOf(r.NoPreview), WWW: r.Www, Watch: listOf(r.WatchJson), Replicas: int(r.Replicas), Tags: listOf(r.TagsJson), Keep: KeepOf(r.KeepJson), Steps: StepsOf(r.StepsJson)}
	json.Unmarshal([]byte(r.ReleaseJson), &s.Release)
	json.Unmarshal([]byte(r.CommandJson), &s.Command)
	json.Unmarshal([]byte(r.VolumesJson), &s.Volumes)
	json.Unmarshal([]byte(r.AllowJson), &s.Allow)
	if spec.BackupChosen(s.Kind) && r.Backup == 1 {
		on := true
		s.Backup = &on
	}
	ds, err := q.ListDomains(ctx, r.ID)
	for _, d := range ds {
		s.Domains = append(s.Domains, d.Host)
	}
	return s, err
}

func list(l []string) string {
	if l == nil {
		l = []string{}
	}
	b, _ := json.Marshal(l)
	return string(b)
}

func (a *App) syncDomains(ctx context.Context, q *db.Queries, serviceID, serverID string, hosts []string, now int64) error {
	have, err := q.ListDomains(ctx, serviceID)
	if err != nil {
		return err
	}
	for _, d := range have {
		if !slices.Contains(hosts, d.Host) {
			if err := q.DeleteDomain(ctx, db.DeleteDomainParams{ServiceID: serviceID, Host: d.Host}); err != nil {
				return err
			}
			if err := QueueRemoval(ctx, q, serverID, d.Host); err != nil {
				return err
			}
		}
	}
	for _, h := range hosts {
		if o, err := q.DomainOwner(ctx, h); err == nil && o.ServiceID == serviceID {
			continue
		} else if err == nil {
			if err := q.DeleteDomain(ctx, db.DeleteDomainParams{ServiceID: o.ServiceID, Host: h}); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := q.InsertDomain(ctx, db.InsertDomainParams{ID: store.NewID("dom_"), ServiceID: serviceID, Host: h, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if err := UnqueueRemoval(ctx, q, serverID, h); err != nil {
			return err
		}
	}
	return nil
}

func sameDockerNames(ctx context.Context, q *db.Queries, f spec.File) error {
	rows, err := q.DockerNames(ctx)
	if err != nil {
		return err
	}
	env := f.Project + "-" + f.Environment
	for _, r := range rows {
		if r.Project == f.Project && r.Env == f.Environment {
			continue
		}
		if r.Project+"-"+r.Env == env {
			return fmt.Errorf("%s/%s would share Docker names with %s/%s; pick another name.", f.Project, f.Environment, r.Project, r.Env)
		}
		for _, s := range f.Services {
			if r.Service != "" && r.Project+"-"+r.Env+"-"+r.Service == env+"-"+s.Name {
				return fmt.Errorf("%s/%s/%s would share Docker names with %s/%s/%s; pick another name.", f.Project, f.Environment, s.Name, r.Project, r.Env, r.Service)
			}
		}
	}
	return nil
}

func givesUp(f spec.File, name, host string) bool {
	i := slices.IndexFunc(f.Services, func(s spec.Service) bool { return s.Name == name })
	return i >= 0 && !slices.Contains(f.Services[i].Domains, host)
}

func previewOf(noPreview int64) *bool {
	if noPreview == 1 {
		return new(bool)
	}
	return nil
}

func UnqueueRemoval(ctx context.Context, q *db.Queries, serverID, host string) error {
	v, err := q.GetSetting(ctx, RemovalKey(serverID))
	if err != nil {
		return nil
	}
	var hosts []string
	json.Unmarshal([]byte(v), &hosts)
	if !slices.Contains(hosts, host) {
		return nil
	}
	b, _ := json.Marshal(slices.DeleteFunc(hosts, func(x string) bool { return x == host }))
	return q.SetSetting(ctx, db.SetSettingParams{Key: RemovalKey(serverID), Value: string(b)})
}

func RemovalKey(serverID string) string { return "domains.remove." + serverID }

func QueueRemoval(ctx context.Context, q *db.Queries, serverID, host string) error {
	var hosts []string
	if v, err := q.GetSetting(ctx, RemovalKey(serverID)); err == nil {
		json.Unmarshal([]byte(v), &hosts)
	}
	if !slices.Contains(hosts, host) {
		hosts = append(hosts, host)
	}
	b, _ := json.Marshal(hosts)
	return q.SetSetting(ctx, db.SetSettingParams{Key: RemovalKey(serverID), Value: string(b)})
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func HashToken(plain string) string {
	s := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(s[:])
}

func (a *App) CreateToken(ctx context.Context, name string) (string, error) {
	owner := ""
	if m, err := a.St.Q.FirstAdmin(ctx); err == nil {
		owner = m.ID
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return a.CreateTokenFor(ctx, name, owner)
}

func volumes(v []spec.Volume) string {
	if len(v) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func overcommit(ctx context.Context, q *db.Queries, srv db.Server, f spec.File, stored []spec.Service) (*plan.Change, error) {
	rows, err := q.LastMachineMinutes(ctx, db.LastMachineMinutesParams{ServerID: srv.ID, Limit: 1})
	if err != nil || len(rows) == 0 || rows[0].MemTotal == 0 {
		return nil, err
	}
	total, err := q.ServerMemoryLimits(ctx, db.ServerMemoryLimitsParams{ServerID: srv.ID, Project: f.Project, Env: f.Environment})
	if err != nil {
		return nil, err
	}
	inFile := map[string]bool{}
	for _, s := range f.Services {
		total += int64(s.Memory)
		inFile[s.Name] = true
	}
	for _, s := range stored {
		if !inFile[s.Name] {
			total += int64(s.Memory)
		}
	}
	if total <= rows[0].MemTotal {
		return nil, nil
	}
	return &plan.Change{Op: plan.OpWarn, Kind: "server", Name: srv.Name,
		Detail: fmt.Sprintf("memory limits add up to %.1f GB; it has %.1f GB", float64(total)/1e9, float64(rows[0].MemTotal)/1e9)}, nil
}

type restrictedKey struct{}

func Restricted(ctx context.Context) context.Context {
	return context.WithValue(ctx, restrictedKey{}, true)
}

func (a *App) keepDeletes(ctx context.Context, q *db.Queries, ids map[string]string, stored []spec.Service, f spec.File) ([]plan.Change, error) {
	var out []plan.Change
	for _, w := range f.Services {
		i := slices.IndexFunc(stored, func(c spec.Service) bool { return c.Name == w.Name })
		if i < 0 || reflect.DeepEqual(stored[i].Keep, w.Keep) {
			continue
		}
		all, err := q.ListBackups(ctx, ids[w.Name])
		if err != nil {
			return nil, err
		}
		byVolume := map[string][]db.Backup{}
		total := 0
		for _, b := range all {
			if b.Kind != "base" && b.State == "ok" {
				byVolume[b.Volume] = append(byVolume[b.Volume], b)
				total++
			}
		}
		n := 0
		for _, bs := range byVolume {
			next := db.Backup{ID: "next", State: "ok", CreatedAt: a.Now().UnixMilli()}
			n += len(KeepWith(append(bs, next), RuleOf(w.Keep)))
		}
		if n > 0 && ctx.Value(restrictedKey{}) != nil {
			return nil, fmt.Errorf("%s: keep %s deletes %d of its %d backups; only an Admin can apply that", w.Name, w.Keep, n, total)
		}
		if n > 0 {
			out = append(out, plan.Change{Op: plan.OpWarn, Kind: "service", Name: w.Name,
				Detail: fmt.Sprintf("keep %s: the next good backup deletes %d of its %d backups", w.Keep, n, total)})
		}
	}
	return out, nil
}

func jsonList(l []string) string {
	if len(l) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(l)
	return string(b)
}

func listOf(js string) []string {
	var l []string
	json.Unmarshal([]byte(js), &l)
	if len(l) == 0 {
		return nil
	}
	return l
}

func keepJSON(k *spec.Keep) string {
	if k == nil {
		return ""
	}
	b, _ := json.Marshal(k)
	return string(b)
}

func stepsJSON(s spec.Steps) string {
	if s == (spec.Steps{}) {
		return ""
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func StepsOf(js string) spec.Steps {
	var s spec.Steps
	json.Unmarshal([]byte(js), &s)
	return s
}

func KeepOf(js string) *spec.Keep {
	if js == "" {
		return nil
	}
	var k spec.Keep
	if json.Unmarshal([]byte(js), &k) != nil {
		return nil
	}
	return &k
}
