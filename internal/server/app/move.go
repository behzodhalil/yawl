package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

type SealedRow struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Service     string `json:"service"`
	RecoveryBox string `json:"recovery_box"`
	UpdatedAt   int64  `json:"updated_at"`
}

type MovePlan struct {
	Lines          []string    `json:"lines"`
	ToPubkey       string      `json:"to_pubkey"`
	RecoveryPubkey string      `json:"recovery_pubkey"`
	Restore        bool        `json:"restore"`
	Vars           []SealedRow `json:"vars"`
}

type moveFacts struct {
	env      db.GetEnvironmentByNameRow
	name     string
	from, to db.Server
	apps     []db.Service
	dbs      []db.Service
	vols     []movedVolume
	last     map[string]db.Backup
}

type movedVolume struct {
	svc  db.Service
	name string
}

func (v movedVolume) key() string   { return v.svc.ID + "/" + v.name }
func (v movedVolume) label() string { return v.svc.Name + " volume " + v.name }

func (f moveFacts) carries() bool { return len(f.dbs) > 0 || len(f.vols) > 0 }

func (f moveFacts) firstCarried() string {
	if len(f.dbs) > 0 {
		return f.dbs[0].Name
	}
	return f.vols[0].label()
}

func (a *App) moveFacts(ctx context.Context, q *db.Queries, project, env, to string) (moveFacts, error) {
	f := moveFacts{name: env, last: map[string]db.Backup{}}
	var err error
	if f.env, err = q.GetEnvironmentByName(ctx, db.GetEnvironmentByNameParams{Project: project, Env: env}); err != nil || f.env.DeletedAt.Valid {
		return f, fmt.Errorf("There is no environment %s in %s.", env, project)
	}
	if t, err := q.TrashedInEnv(ctx, f.env.ID); err == nil {
		return f, fmt.Errorf("%s/%s/%s is in the trash; restore it, or wait until it is purged on %s.", project, env, t.Name, Stamp(t.PurgeAt.Int64))
	}
	if f.to, err = q.GetServerByName(ctx, to); errors.Is(err, sql.ErrNoRows) {
		return f, fmt.Errorf("There is no server named %s.", to)
	} else if err != nil {
		return f, err
	}
	if f.to.ID == f.env.ServerID {
		return f, fmt.Errorf("%s already runs on %s.", env, to)
	}
	if !f.to.LastHeartbeat.Valid {
		return f, fmt.Errorf("%s has never connected; start its agent first.", to)
	}
	if f.to.Offline == 1 {
		return f, fmt.Errorf("%s is offline; a move needs it online.", to)
	}
	if m, err := q.ActiveMove(ctx, f.env.ID); err == nil {
		target, _ := q.GetServer(ctx, m.ToServerID)
		return f, fmt.Errorf("%s is already moving to %s.", env, target.Name)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return f, err
	}
	if n, err := q.UnfinishedDeploysInEnv(ctx, f.env.ID); err != nil {
		return f, err
	} else if n > 0 {
		return f, fmt.Errorf("%s has a deploy in progress; wait for it to finish.", env)
	}
	if f.from, err = q.GetServer(ctx, f.env.ServerID); err != nil {
		return f, err
	}
	svcs, err := q.ListServicesInEnv(ctx, f.env.ID)
	if err != nil {
		return f, err
	}
	for _, s := range svcs {
		if s.Kind == string(spec.KindWeb) {
			if err := CheckFreeHost(ctx, q, f.to.ID, project, env, s.Name); err != nil {
				return f, err
			}
		}
		if !s.CurrentDeployID.Valid {
			continue
		}
		if spec.IsDatabase(spec.Kind(s.Kind)) {
			f.dbs = append(f.dbs, s)
		} else {
			f.apps = append(f.apps, s)
		}
		if s.OutsidePort == 0 {
			continue
		}
		users, err := q.OutsidePortUsers(ctx, db.OutsidePortUsersParams{ServerID: f.to.ID, OutsidePort: s.OutsidePort})
		if err != nil {
			return f, err
		}
		for _, u := range users {
			return f, fmt.Errorf("%s: outside_port %d is already used by %s-%s on %s.", s.Name, s.OutsidePort, u.Project, u.Service, to)
		}
	}
	for _, s := range f.apps {
		for _, v := range AllVolumes(s.VolumesJson, s.VolumesKept) {
			if f.from.Offline == 1 && !hasVolume(s.VolumesJson, v) && !hasVolumeBackup(ctx, q, s.ID, v) {
				continue
			}
			f.vols = append(f.vols, movedVolume{s, v})
		}
	}
	type carried struct {
		key, label string
		last       func() (db.Backup, error)
	}
	var all []carried
	for _, d := range f.dbs {
		all = append(all, carried{d.ID, d.Name, func() (db.Backup, error) { return q.LatestOKBackup(ctx, d.ID) }})
	}
	for _, v := range f.vols {
		all = append(all, carried{v.key(), v.label(), func() (db.Backup, error) {
			return q.LatestOKVolumeBackup(ctx, db.LatestOKVolumeBackupParams{ServiceID: v.svc.ID, Volume: v.name})
		}})
	}
	for _, c := range all {
		if f.from.Offline == 1 {
			b, err := c.last()
			if errors.Is(err, sql.ErrNoRows) {
				return f, fmt.Errorf("%s is offline and %s has no backup to restore from.", f.from.Name, c.label)
			} else if err != nil {
				return f, err
			}
			f.last[c.key] = b
		} else if _, err := q.GetSetting(ctx, "storage.endpoint"); err != nil {
			return f, fmt.Errorf("Set up backup storage first: a move carries %s through a backup.", c.label)
		}
	}
	return f, nil
}

func (a *App) PlanMove(ctx context.Context, project, env, to string) (MovePlan, error) {
	f, err := a.moveFacts(ctx, a.St.Q, project, env, to)
	if err != nil {
		return MovePlan{}, err
	}
	lines, err := a.moveLines(ctx, a.St.Q, f)
	if err != nil {
		return MovePlan{}, err
	}
	rows, err := sealedRows(ctx, a.St.Q, f.env.ID)
	if err != nil {
		return MovePlan{}, err
	}
	p := MovePlan{Lines: lines, ToPubkey: f.to.AgentPubkey, Restore: f.carries(), Vars: rows}
	p.RecoveryPubkey, _ = a.St.Q.GetSetting(ctx, "recovery.pubkey")
	return p, nil
}

func sealedRows(ctx context.Context, q *db.Queries, envID string) ([]SealedRow, error) {
	rows, err := q.SealedVarsInEnv(ctx, envID)
	if err != nil {
		return nil, err
	}
	out := []SealedRow{}
	for _, r := range rows {
		out = append(out, SealedRow{r.ID, r.Key, r.Service, r.RecoveryBox, r.UpdatedAt})
	}
	srcs, err := q.SourcesForEnv(ctx, envID)
	for _, s := range srcs {
		out = append(out, SealedRow{s.ID, s.Provider, "", s.RecoveryBox, s.UpdatedAt})
	}
	return out, err
}

func (a *App) moveLines(ctx context.Context, q *db.Queries, f moveFacts) ([]string, error) {
	from, to, env := f.from.Name, f.to.Name, f.name
	apps, dbs := names(f.apps), names(f.dbs)
	offline := f.from.Offline == 1
	lines := []string{fmt.Sprintf("move %s from %s to %s", env, from, to)}
	if f.carries() && !offline && len(f.apps) > 0 {
		lines = append(lines, fmt.Sprintf("stop %s on %s (%s is down from here until the switch)", apps, from, env))
	}
	backup := func(key, label string) {
		if offline {
			b := f.last[key]
			at := b.CreatedAt
			if b.FinishedAt.Valid {
				at = b.FinishedAt.Int64
			}
			lines = append(lines, fmt.Sprintf("%s is offline: %s is restored from its last backup, %s old; changes after it are lost",
				from, label, age(a.Now().Sub(time.UnixMilli(at)))))
		} else {
			lines = append(lines, fmt.Sprintf("back up %s on %s", label, from))
		}
	}
	for _, d := range f.dbs {
		backup(d.ID, d.Name)
	}
	for _, v := range f.vols {
		backup(v.key(), v.label())
	}
	for _, d := range f.dbs {
		lines = append(lines, fmt.Sprintf("create %s on %s and restore it from that backup", d.Name, to))
	}
	for _, v := range f.vols {
		lines = append(lines, fmt.Sprintf("restore %s on %s from that backup", v.label(), to))
	}
	if len(f.apps) > 0 {
		lines = append(lines, fmt.Sprintf("build and start %s on %s", apps, to))
		here, err := q.SharedVarsFor(ctx, f.from.ID)
		if err != nil {
			return nil, err
		}
		there, err := q.SharedVarsFor(ctx, f.to.ID)
		if err != nil {
			return nil, err
		}
		onTo := map[string]bool{}
		for _, v := range there {
			onTo[v.Scope+"/"+v.Key] = true
		}
		for _, v := range here {
			switch {
			case v.Scope == "team" && !onTo["team/"+v.Key]:
				lines = append(lines, fmt.Sprintf("%s, a team variable, is not set on %s: set it again in Shared variables, or the apps start without it", v.Key, to))
			case v.Scope == "server":
				lines = append(lines, fmt.Sprintf("%s is %s's own variable: the apps on %s do not get it", v.Key, from, to))
			}
		}
	}
	ip, err := netip.ParseAddr(f.to.PublicIp)
	if err != nil {
		return nil, err
	}
	var hosts, custom []string
	for _, s := range f.apps {
		if s.Kind != string(spec.KindWeb) {
			continue
		}
		hosts = append(hosts, FreeHost(s.Name, env, ip))
		ds, err := q.ListDomains(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		for _, d := range ds {
			custom = append(custom, fmt.Sprintf("%s: point its A record to %s; until then it does not answer", d.Host, f.to.PublicIp))
		}
	}
	if len(hosts) > 0 {
		lines = append(lines, "switch "+strings.Join(hosts, ", "))
	}
	ep, err := q.EnvProject(ctx, f.env.ID)
	if err != nil {
		return nil, err
	}
	for _, d := range f.dbs {
		apps, err := q.ReferencingApps(ctx, db.ReferencingAppsParams{ProjectID: ep.ProjectID, Ref: env + "/" + d.Name + ".url"})
		if err != nil {
			return nil, err
		}
		for _, a := range apps {
			lines = append(lines, fmt.Sprintf("redeploy %s/%s, which uses %s over the private network", a.Env, a.Name, d.Name))
		}
	}
	switch {
	case offline:
		lines = append(lines, fmt.Sprintf("stop %s on %s once it is back online", env, from))
	case len(f.dbs) > 0:
		lines = append(lines, fmt.Sprintf("stop %s on %s (its volume is kept)", dbs, from))
	default:
		lines = append(lines, fmt.Sprintf("stop %s on %s", apps, from))
	}
	for _, v := range f.vols {
		if offline {
			lines = append(lines, fmt.Sprintf("keep %s on %s: it may hold changes newer than that backup", v.label(), from))
		} else {
			lines = append(lines, fmt.Sprintf("remove %s from %s", v.label(), from))
		}
	}
	return append(lines, custom...), nil
}

func hasVolumeBackup(ctx context.Context, q *db.Queries, serviceID, volume string) bool {
	_, err := q.LatestOKVolumeBackup(ctx, db.LatestOKVolumeBackupParams{ServiceID: serviceID, Volume: volume})
	return err == nil
}

func names(svcs []db.Service) string {
	var n []string
	for _, s := range svcs {
		n = append(n, s.Name)
	}
	if len(n) < 2 {
		return strings.Join(n, "")
	}
	return strings.Join(n[:len(n)-1], ", ") + " and " + n[len(n)-1]
}

func age(d time.Duration) string {
	plural := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit
		}
		return fmt.Sprintf("%d %ss", n, unit)
	}
	switch {
	case d < time.Hour:
		return "less than an hour"
	case d < 48*time.Hour:
		return plural(int(d/time.Hour), "hour")
	}
	return plural(int(d/(24*time.Hour)), "day")
}

type Reseal struct {
	AgentBox  string `json:"agent_box"`
	UpdatedAt int64  `json:"updated_at"`
}

type MoveRequest struct {
	To          string            `json:"to"`
	Boxes       map[string]Reseal `json:"boxes"`
	RecoveryBox string            `json:"recovery_box"`
}

type MoveView struct {
	ID      string   `json:"id"`
	From    string   `json:"from"`
	To      string   `json:"to"`
	Step    string   `json:"step"`
	State   string   `json:"state"`
	Cause   string   `json:"cause"`
	Cleaned bool     `json:"cleaned"`
	Lines   []string `json:"lines"`
}

func BeforeSwitch(step string) bool {
	return step == "stop" || step == "backup" || step == "switch_server"
}

func (a *App) StartMove(ctx context.Context, project, env string, r MoveRequest, actor string) (string, error) {
	id, now := store.NewID("mv_"), a.Now().UnixMilli()
	err := a.St.Tx(ctx, func(q *db.Queries) error {
		f, err := a.moveFacts(ctx, q, project, env, r.To)
		if err != nil {
			return err
		}
		rows, err := sealedRows(ctx, q, f.env.ID)
		if err != nil {
			return err
		}
		stale := fmt.Errorf("The re-sealed values do not match %s's keys now; reload the page and try again.", env)
		if len(rows) != len(r.Boxes) {
			return stale
		}
		boxes := map[string]string{}
		for _, row := range rows {
			b, ok := r.Boxes[row.ID]
			if !ok || b.UpdatedAt != row.UpdatedAt || b.AgentBox == "" || len(b.AgentBox) > 90<<10 {
				return stale
			}
			boxes[row.ID] = b.AgentBox
		}
		if f.carries() && r.RecoveryBox == "" {
			return fmt.Errorf("The recovery key is needed to restore %s on %s.", f.firstCarried(), r.To)
		}
		lines, err := a.moveLines(ctx, q, f)
		if err != nil {
			return err
		}
		step := "switch_server"
		if f.carries() && f.from.Offline == 0 {
			step = "backup"
			if len(f.apps) > 0 {
				step = "stop"
			}
		}
		bj, _ := json.Marshal(boxes)
		lj, _ := json.Marshal(lines)
		if err := q.CreateMove(ctx, db.CreateMoveParams{ID: id, EnvironmentID: f.env.ID, FromServerID: f.from.ID, ToServerID: f.to.ID,
			Step: step, BoxesJson: string(bj), LinesJson: string(lj), CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		return Event(ctx, q, now, "env.move.started", id, actor, map[string]string{"environment": f.env.ID, "from": f.from.Name, "to": f.to.Name})
	})
	if err != nil {
		return "", err
	}
	if r.RecoveryBox != "" {
		a.KeepMoveKey(id, r.RecoveryBox)
	}
	return id, nil
}

func (a *App) RetryMove(ctx context.Context, moveID, recoveryBox, actor string) error {
	now := a.Now().UnixMilli()
	err := a.St.Tx(ctx, func(q *db.Queries) error {
		m, err := q.GetMove(ctx, moveID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("There is no move %s.", moveID)
		} else if err != nil {
			return err
		}
		if m.State != "failed" {
			return errors.New("This move is not stopped.")
		}
		if BeforeSwitch(m.Step) {
			return errors.New("This move was undone; start a new one.")
		}
		if err := q.RetryMoveState(ctx, db.RetryMoveStateParams{UpdatedAt: now, ID: m.ID}); err != nil {
			return err
		}
		return Event(ctx, q, now, "env.move.retried", m.ID, actor, map[string]string{"step": m.Step})
	})
	if err == nil && recoveryBox != "" {
		a.KeepMoveKey(moveID, recoveryBox)
	}
	return err
}

func (a *App) KeepMoveKey(moveID, box string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.moveKeys == nil {
		a.moveKeys = map[string]string{}
	}
	a.moveKeys[moveID] = box
}

func (a *App) TakeMoveKey(moveID string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	k, ok := a.moveKeys[moveID]
	delete(a.moveKeys, moveID)
	return k, ok
}

func (a *App) LatestMove(ctx context.Context, project, env string) (MoveView, error) {
	e, err := a.St.Q.GetEnvironmentByName(ctx, db.GetEnvironmentByNameParams{Project: project, Env: env})
	if err != nil {
		return MoveView{}, err
	}
	m, err := a.St.Q.LatestMove(ctx, e.ID)
	if err != nil {
		return MoveView{}, err
	}
	from, _ := a.St.Q.GetServer(ctx, m.FromServerID)
	to, _ := a.St.Q.GetServer(ctx, m.ToServerID)
	v := MoveView{ID: m.ID, From: from.Name, To: to.Name, Step: m.Step, State: m.State, Cause: m.Cause, Cleaned: m.Cleaned == 1}
	json.Unmarshal([]byte(m.LinesJson), &v.Lines)
	return v, nil
}
