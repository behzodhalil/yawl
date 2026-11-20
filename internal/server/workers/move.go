package workers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/spec"
)

type Move struct {
	St  *store.Store
	App *app.App
	GW  Sender
	V   *vault.Vault
	Now func() time.Time

	beforeDeployTx func()
	cleanFails     map[string]int
	cleanGaveUp    map[string]string
}

const cleanTries = 30

type moveState struct {
	db.EnvMove
	boxes   map[string]string
	stopped map[string]string
	backups map[string]string
	deploys map[string]string
	old     map[string]string
}

func (w *Move) Tick(ctx context.Context) error {
	if app.Adopting(ctx, w.St.Q) {
		return nil
	}
	ms, err := w.St.Q.RunningMoves(ctx)
	if err != nil {
		return err
	}
	for _, m := range ms {
		if err := w.advance(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (w *Move) advance(ctx context.Context, row db.EnvMove) error {
	for {
		m := decodeMove(row)
		var next bool
		var err error
		switch {
		case m.State == "done":
			return w.clean(ctx, m)
		case m.Step == "stop":
			next, err = w.stop(ctx, m)
		case m.Step == "backup":
			next, err = w.backup(ctx, m)
		case m.Step == "switch_server":
			next, err = true, w.switchServer(ctx, row)
		case m.Step == "restore":
			next, err = w.restore(ctx, m)
		case m.Step == "deploy":
			next, err = w.deploy(ctx, m)
		case m.Step == "clean":
			return w.clean(ctx, m)
		default:
			return nil
		}
		if err != nil || !next {
			return err
		}
		if row, err = w.St.Q.GetMove(ctx, row.ID); err != nil || row.State != "running" {
			return err
		}
	}
}

func decodeMove(r db.EnvMove) moveState {
	m := moveState{EnvMove: r, boxes: map[string]string{}, stopped: map[string]string{}, backups: map[string]string{}, deploys: map[string]string{}, old: map[string]string{}}
	json.Unmarshal([]byte(r.BoxesJson), &m.boxes)
	json.Unmarshal([]byte(r.StoppedJson), &m.stopped)
	json.Unmarshal([]byte(r.BackupsJson), &m.backups)
	json.Unmarshal([]byte(r.DeploysJson), &m.deploys)
	json.Unmarshal([]byte(r.OldJson), &m.old)
	return m
}

func (w *Move) save(ctx context.Context, q *db.Queries, m moveState, step, state, cause string) error {
	enc := func(v map[string]string) string { b, _ := json.Marshal(v); return string(b) }
	now := w.Now().UnixMilli()
	var fin sql.NullInt64
	if state == "done" || state == "failed" {
		fin = sql.NullInt64{Int64: now, Valid: true}
		if m.State == state && m.FinishedAt.Valid {
			fin = m.FinishedAt
		}
	}
	return q.SetMoveStep(ctx, db.SetMoveStepParams{ID: m.ID, Step: step, State: state, Cause: cause, StoppedJson: enc(m.stopped),
		BackupsJson: enc(m.backups), DeploysJson: enc(m.deploys), OldJson: enc(m.old), Cleaned: m.Cleaned, FinishedAt: fin, UpdatedAt: now})
}

func (w *Move) parts(ctx context.Context, envID string) (apps, dbs []db.Service, err error) {
	svcs, err := w.St.Q.ListServicesInEnv(ctx, envID)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range svcs {
		switch {
		case !s.CurrentDeployID.Valid:
		case spec.IsDatabase(spec.Kind(s.Kind)):
			dbs = append(dbs, s)
		default:
			apps = append(apps, s)
		}
	}
	return apps, dbs, nil
}

type volume struct {
	svc  db.Service
	name string
}

func (v volume) key() string   { return v.svc.ID + "/" + v.name }
func (v volume) label() string { return v.svc.Name + " volume " + v.name }

func volumes(apps []db.Service) []volume {
	var out []volume
	for _, s := range apps {
		for _, v := range app.AllVolumes(s.VolumesJson, s.VolumesKept) {
			out = append(out, volume{s, v})
		}
	}
	return out
}

func (w *Move) names(ctx context.Context, m moveState) (env, from, to string) {
	e, _ := w.St.Q.GetEnvironmentNames(ctx, m.EnvironmentID)
	f, _ := w.St.Q.GetServer(ctx, m.FromServerID)
	t, _ := w.St.Q.GetServer(ctx, m.ToServerID)
	return e.Name, f.Name, t.Name
}

func (w *Move) envLabel(ctx context.Context, m moveState) string {
	e, _ := w.St.Q.GetEnvironmentNames(ctx, m.EnvironmentID)
	return e.Project + "/" + e.Name
}

func (w *Move) send(ctx context.Context, serverID, key, name string, args any) error {
	cmd, err := proto.NewCommand(key, name, args)
	if err != nil {
		return err
	}
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := w.GW.Send(tctx, serverID, cmd)
	if err != nil {
		return fmt.Errorf("the server did not answer")
	}
	if !res.OK {
		return fmt.Errorf("%s", res.Cause)
	}
	return nil
}

func (w *Move) stop(ctx context.Context, m moveState) (bool, error) {
	apps, _, err := w.parts(ctx, m.EnvironmentID)
	if err != nil {
		return false, err
	}
	_, from, _ := w.names(ctx, m)
	for _, s := range apps {
		if _, done := m.stopped[s.ID]; done {
			continue
		}
		d, err := w.St.Q.GetDeploy(ctx, s.CurrentDeployID.String)
		if err != nil {
			return false, err
		}
		name, args := proto.CmdStop, any(proto.StopArgs{Container: d.Container})
		if s.Kind == "cron" {
			name, args = proto.CmdCronRemove, proto.CronRemoveArgs{ID: s.ID}
		}
		if err := w.send(ctx, m.FromServerID, "move:"+m.ID+":stop:"+s.ID, name, args); err != nil {
			return false, w.failBefore(ctx, m, fmt.Sprintf("stop %s on %s: %v", s.Name, from, err))
		}
		m.stopped[s.ID] = d.Container
		if err := w.save(ctx, w.St.Q, m, "stop", "running", ""); err != nil {
			return false, err
		}
	}
	return true, w.save(ctx, w.St.Q, m, "backup", "running", "")
}

func (w *Move) backup(ctx context.Context, m moveState) (bool, error) {
	apps, dbs, err := w.parts(ctx, m.EnvironmentID)
	if err != nil {
		return false, err
	}
	type part struct {
		key, label string
		take       func() (string, error)
	}
	var parts []part
	for _, s := range dbs {
		parts = append(parts, part{s.ID, s.Name, func() (string, error) { return w.App.Backup(ctx, s.ID, "system") }})
	}
	for _, v := range volumes(apps) {
		parts = append(parts, part{v.key(), v.label(), func() (string, error) { return w.App.CarryVolume(ctx, v.svc.ID, v.name, "system") }})
	}
	for _, p := range parts {
		if m.backups[p.key] != "" {
			continue
		}
		id, err := p.take()
		if err != nil {
			return false, w.failBefore(ctx, m, fmt.Sprintf("back up %s: %v", p.label, err))
		}
		m.backups[p.key] = id
		if err := w.save(ctx, w.St.Q, m, "backup", "running", ""); err != nil {
			return false, err
		}
	}
	for _, p := range parts {
		b, err := w.St.Q.GetBackup(ctx, m.backups[p.key])
		if err != nil {
			return false, err
		}
		switch b.State {
		case "ok":
		case "failed":
			return false, w.failBefore(ctx, m, fmt.Sprintf("back up %s: %s", p.label, b.Cause))
		default:
			return false, nil
		}
	}
	return true, w.save(ctx, w.St.Q, m, "switch_server", "running", "")
}

func (w *Move) switchServer(ctx context.Context, row db.EnvMove) error {
	m := decodeMove(row)
	apps, dbs, err := w.parts(ctx, m.EnvironmentID)
	if err != nil {
		return err
	}
	for _, s := range apps {
		if d, err := w.St.Q.GetDeploy(ctx, s.CurrentDeployID.String); err == nil && d.Container != "" {
			m.old[s.ID] = d.Container
		}
	}
	if len(dbs) > 0 {
		names, err := w.St.Q.GetEnvironmentNames(ctx, m.EnvironmentID)
		if err != nil {
			return err
		}
		for _, s := range dbs {
			m.old[s.ID] = fmt.Sprintf("yawl-%s-%s-%s", names.Project, names.Name, s.Name)
		}
	}
	vols := volumes(apps)
	if len(vols) > 0 {
		names, err := w.St.Q.GetEnvironmentNames(ctx, m.EnvironmentID)
		if err != nil {
			return err
		}
		for _, v := range vols {
			if m.backups[v.key()] == "" {
				continue
			}
			if m.old[v.key()], err = proto.VolumeName(names.Project, names.Name, v.svc.Name, v.name); err != nil {
				return err
			}
		}
	}
	now := w.Now().UnixMilli()
	return w.St.Tx(ctx, func(q *db.Queries) error {
		var given []int64
		for _, s := range dbs {
			if s.PrivatePort == 0 {
				continue
			}
			port, err := app.FreePrivatePort(ctx, q, m.ToServerID, s.PrivatePort, given...)
			if err != nil {
				return err
			}
			given = append(given, port)
			if err := q.SetPrivatePort(ctx, db.SetPrivatePortParams{PrivatePort: port, UpdatedAt: now, ID: s.ID}); err != nil {
				return err
			}
			if err := q.DeleteShares(ctx, s.ID); err != nil {
				return err
			}
		}
		if err := q.SetEnvironmentServer(ctx, db.SetEnvironmentServerParams{ServerID: m.ToServerID, UpdatedAt: now, ID: m.EnvironmentID}); err != nil {
			return err
		}
		for id, box := range m.boxes {
			var err error
			if strings.HasPrefix(id, "src_") {
				_, err = q.SetSourceAgentBox(ctx, db.SetSourceAgentBoxParams{AgentBox: box, UpdatedAt: now, ID: id, EnvironmentID: m.EnvironmentID})
			} else {
				_, err = q.SetEnvVarAgentBox(ctx, db.SetEnvVarAgentBoxParams{AgentBox: box, UpdatedAt: now, ID: id, EnvironmentID: m.EnvironmentID})
			}
			if err != nil {
				return err
			}
		}
		if err := q.ResetEnvDomains(ctx, db.ResetEnvDomainsParams{UpdatedAt: now, EnvironmentID: m.EnvironmentID}); err != nil {
			return err
		}
		next := "deploy"
		if len(dbs) > 0 || len(vols) > 0 {
			next = "restore"
		}
		return w.save(ctx, q, m, next, "running", "")
	})
}

func (w *Move) failBefore(ctx context.Context, m moveState, cause string) error {
	env, _, to := w.names(ctx, m)
	label := w.envLabel(ctx, m)
	now := w.Now().UnixMilli()
	return w.St.Tx(ctx, func(q *db.Queries) error {
		for svcID := range m.stopped {
			s, err := q.GetService(ctx, svcID)
			if err != nil || !s.CurrentDeployID.Valid {
				continue
			}
			d, err := q.GetDeploy(ctx, s.CurrentDeployID.String)
			if err != nil {
				return err
			}
			if err := q.CreateRollbackDeploy(ctx, db.CreateRollbackDeployParams{ID: store.NewID("dep_"), ServiceID: svcID, CommitSha: d.CommitSha,
				Subject: d.Subject, Image: d.Image, Digest: d.Digest, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
		}
		if err := w.save(ctx, q, m, m.Step, "failed", cause); err != nil {
			return err
		}
		return announce(ctx, q, now, "env.move.failed", m.ID, map[string]string{"cause": cause},
			app.MoveFailed(label, to, cause, fmt.Sprintf("Moving %s to %s failed: %s.", env, to, cause)))
	})
}

func (w *Move) failAfter(ctx context.Context, m moveState, svcID, cause string) error {
	env, _, to := w.names(ctx, m)
	label := w.envLabel(ctx, m)
	delete(m.deploys, svcID)
	now := w.Now().UnixMilli()
	return w.St.Tx(ctx, func(q *db.Queries) error {
		if err := w.save(ctx, q, m, m.Step, "failed", cause); err != nil {
			return err
		}
		return announce(ctx, q, now, "env.move.failed", m.ID, map[string]string{"cause": cause},
			app.MoveFailed(label, to, cause, fmt.Sprintf("Moving %s to %s failed: %s.", env, to, cause)))
	})
}

func (w *Move) waitDeploys(ctx context.Context, m moveState, svcs []db.Service, what string) (bool, error) {
	for _, s := range svcs {
		d, err := w.St.Q.GetDeploy(ctx, m.deploys[s.ID])
		if err != nil {
			return false, err
		}
		switch d.State {
		case "live":
		case "failed", "rolled_back", "superseded":
			cause := d.Cause
			if cause == "" {
				cause = "the deploy ended " + d.State
			}
			if what == "deploy" {
				_, _, to := w.names(ctx, m)
				return false, w.failAfter(ctx, m, s.ID, fmt.Sprintf("%s did not start on %s: %s", s.Name, to, cause))
			}
			return false, w.failAfter(ctx, m, s.ID, fmt.Sprintf("%s %s: %s", what, s.Name, cause))
		default:
			return false, nil
		}
	}
	return true, nil
}

func (w *Move) restore(ctx context.Context, m moveState) (bool, error) {
	apps, dbs, err := w.parts(ctx, m.EnvironmentID)
	if err != nil {
		return false, err
	}
	key, haveKey := "", false
	takeKey := func(id, label string) error {
		if !haveKey {
			if key, haveKey = w.App.TakeMoveKey(m.ID); !haveKey {
				return w.failAfter(ctx, m, id, "restore "+label+": the recovery key was not kept across a server restart; retry the move and enter it again")
			}
		}
		return nil
	}
	for _, v := range volumes(apps) {
		if strings.HasPrefix(m.deploys[v.key()], "done:") {
			continue
		}
		if err := takeKey(v.key(), v.label()); err != nil || !haveKey {
			return false, err
		}
		if ok, err := w.restoreVolume(ctx, m, v, key); !ok || err != nil {
			w.App.KeepMoveKey(m.ID, key)
			return false, err
		}
	}
	for _, s := range dbs {
		if m.deploys[s.ID] != "" {
			continue
		}
		if err := takeKey(s.ID, s.Name); err != nil || !haveKey {
			return false, err
		}
		from := m.backups[s.ID]
		if from == "" {
			b, err := w.St.Q.LatestOKBackup(ctx, s.ID)
			if err != nil {
				return false, w.failAfter(ctx, m, s.ID, "restore "+s.Name+": it has no backup to restore from")
			}
			from = b.ID
		}
		id, now := store.NewID("dep_"), w.Now().UnixMilli()
		m.deploys[s.ID] = id
		err := w.St.Tx(ctx, func(q *db.Queries) error {
			if err := q.CreateMoveRestoreDeploy(ctx, db.CreateMoveRestoreDeployParams{ID: id, ServiceID: s.ID, RestoreFrom: from, MoveID: m.ID,
				CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
			return w.save(ctx, q, m, "restore", "running", "")
		})
		if err != nil {
			w.App.KeepMoveKey(m.ID, key)
			return false, err
		}
		w.App.KeepRestoreKey(id, key)
	}
	if haveKey {
		w.App.KeepMoveKey(m.ID, key)
	}
	if ok, err := w.waitDeploys(ctx, m, dbs, "restore"); !ok || err != nil {
		return false, err
	}
	return true, w.save(ctx, w.St.Q, m, "deploy", "running", "")
}

func (w *Move) restoreVolume(ctx context.Context, m moveState, v volume, recovery string) (bool, error) {
	var b db.Backup
	var err error
	if id := m.backups[v.key()]; id != "" {
		b, err = w.St.Q.GetBackup(ctx, id)
	} else {
		b, err = w.St.Q.LatestOKVolumeBackup(ctx, db.LatestOKVolumeBackupParams{ServiceID: v.svc.ID, Volume: v.name})
	}
	if errors.Is(err, sql.ErrNoRows) && !slices.ContainsFunc(app.Volumes(v.svc.VolumesJson), func(s spec.Volume) bool { return s.Name == v.name }) {
		return true, nil
	}
	if err != nil {
		return false, w.failAfter(ctx, m, v.key(), "restore "+v.label()+": it has no backup to restore from")
	}
	storage, err := storageFrom(ctx, w.St.Q, w.V)
	if err != nil {
		return false, w.failAfter(ctx, m, v.key(), "restore "+v.label()+": "+err.Error())
	}
	names, err := w.St.Q.GetEnvironmentNames(ctx, m.EnvironmentID)
	if err != nil {
		return false, err
	}
	try := m.deploys[v.key()]
	if try == "" {
		try = store.NewID("rv_")
		m.deploys[v.key()] = try
		if err := w.save(ctx, w.St.Q, m, "restore", "running", ""); err != nil {
			return false, err
		}
	}
	cmd, err := proto.NewCommand("move:"+m.ID+":restore:"+v.key()+":"+try, proto.CmdRestoreVolume, proto.RestoreVolumeArgs{
		Project: names.Project, Env: names.Name, Service: v.svc.Name, Name: v.name, Storage: storage, Key: b.ObjectKey, SHA256: b.Sha256, RecoveryKey: recovery})
	if err != nil {
		return false, err
	}
	tctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	res, err := w.GW.Send(tctx, m.ToServerID, cmd)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		return false, w.failAfter(ctx, m, v.key(), "restore "+v.label()+": the server did not answer")
	}
	if !res.OK {
		return false, w.failAfter(ctx, m, v.key(), "restore "+v.label()+": "+res.Cause)
	}
	m.deploys[v.key()] = "done:" + try
	return true, w.save(ctx, w.St.Q, m, "restore", "running", "")
}

func (w *Move) deploy(ctx context.Context, m moveState) (bool, error) {
	apps, _, err := w.parts(ctx, m.EnvironmentID)
	if err != nil {
		return false, err
	}
	if ready, err := w.sharesReady(ctx, m, apps); !ready || err != nil {
		return false, err
	}
	for _, s := range apps {
		if m.deploys[s.ID] != "" {
			continue
		}
		cur, err := w.St.Q.GetDeploy(ctx, s.CurrentDeployID.String)
		if err != nil {
			return false, err
		}
		id, now := store.NewID("dep_"), w.Now().UnixMilli()
		if w.beforeDeployTx != nil {
			w.beforeDeployTx()
		}
		err = w.St.Tx(ctx, func(q *db.Queries) error {
			commit := cur.CommitSha
			if p, err := q.NewestQueuedDeploy(ctx, s.ID); err == nil {
				commit = p.CommitSha
			}
			if err := q.SupersedeQueued(ctx, db.SupersedeQueuedParams{UpdatedAt: now, ServiceID: s.ID}); err != nil {
				return err
			}
			if err := q.CreateMoveDeploy(ctx, db.CreateMoveDeployParams{ID: id, ServiceID: s.ID, CommitSha: commit, MoveID: m.ID, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
			m.deploys[s.ID] = id
			return w.save(ctx, q, m, "deploy", "running", "")
		})
		if err != nil {
			return false, err
		}
	}
	if ok, err := w.waitDeploys(ctx, m, apps, "deploy"); !ok || err != nil {
		return false, err
	}
	env, _, to := w.names(ctx, m)
	label := w.envLabel(ctx, m)
	now := w.Now().UnixMilli()
	return true, w.St.Tx(ctx, func(q *db.Queries) error {
		if err := w.save(ctx, q, m, "clean", "done", ""); err != nil {
			return err
		}
		if err := redeployReferencing(ctx, q, m.EnvironmentID, now); err != nil {
			return err
		}
		return announce(ctx, q, now, "env.move.done", m.ID, map[string]string{"to": to}, app.MoveDone(label, to, fmt.Sprintf("%s moved to %s.", env, to)))
	})
}

func (w *Move) clean(ctx context.Context, m moveState) error {
	if m.Cleaned == 1 || m.Cause != "" {
		return nil
	}
	from, err := w.St.Q.GetServer(ctx, m.FromServerID)
	if err != nil || from.Offline == 1 {
		return err
	}
	if w.cleanFails == nil {
		w.cleanFails = map[string]int{}
	}
	if w.cleanGaveUp == nil {
		w.cleanGaveUp = map[string]string{}
	}
	var gaveUp []string
	stuck := map[string]bool{}
	retry := false
	keys := slices.Sorted(maps.Keys(m.old))
	isVol := func(k string) bool { return strings.Contains(k, "/") }
	slices.SortStableFunc(keys, func(a, b string) int { return boolCmp(isVol(a), isVol(b)) })
	for _, svcID := range keys {
		container := m.old[svcID]
		k := m.ID + "/" + container
		if why, ok := w.cleanGaveUp[k]; ok {
			gaveUp, stuck[svcID] = append(gaveUp, why), true
			continue
		}
		key := cleanKeyNotReplayed(m.ID, svcID, w.cleanFails[k])
		name, args := proto.CmdDrain, any(proto.DrainArgs{Container: container})
		if isVol(svcID) {
			if stuck[strings.Split(svcID, "/")[0]] || retry {
				continue
			}
			name, args = proto.CmdRemoveVolume, proto.RemoveVolumeArgs{Name: container}
		}
		err := w.send(ctx, from.ID, key, name, args)
		switch {
		case err == nil:
			delete(w.cleanFails, k)
		case w.cleanFails[k]+1 >= cleanTries:
			delete(w.cleanFails, k)
			w.cleanGaveUp[k] = fmt.Sprintf("could not remove %s from %s: %s", container, from.Name, err)
			gaveUp = append(gaveUp, w.cleanGaveUp[k])
			stuck[svcID] = true
		default:
			w.cleanFails[k]++
			retry = true
		}
	}
	if retry {
		return nil
	}
	ip, err := netip.ParseAddr(from.PublicIp)
	if err != nil {
		return err
	}
	env, _, _ := w.names(ctx, m)
	apps, _, err := w.parts(ctx, m.EnvironmentID)
	if err != nil {
		return err
	}
	err = w.St.Tx(ctx, func(q *db.Queries) error {
		var custom []string
		for _, s := range apps {
			if s.Kind != string(spec.KindWeb) || stuck[s.ID] {
				continue
			}
			free := app.FreeHost(s.Name, env, ip)
			if owner, err := app.FreeHostOwner(ctx, q, from.ID, ip, free, "", "", ""); err != nil {
				return err
			} else if owner == "" {
				if err := app.QueueRemoval(ctx, q, from.ID, free); err != nil {
					return err
				}
			}
			ds, err := q.ListDomains(ctx, s.ID)
			if err != nil {
				return err
			}
			for _, d := range ds {
				custom = append(custom, d.Host)
			}
		}
		for _, h := range custom {
			if err := app.QueueRemoval(ctx, q, from.ID, h); err != nil {
				return err
			}
		}
		if len(gaveUp) > 0 {
			return w.save(ctx, q, m, m.Step, m.State, strings.Join(gaveUp, "; "))
		}
		m.Cleaned = 1
		return w.save(ctx, q, m, m.Step, m.State, m.Cause)
	})
	if err == nil {
		for k := range w.cleanGaveUp {
			if strings.HasPrefix(k, m.ID+"/") {
				delete(w.cleanGaveUp, k)
			}
		}
	}
	return err
}

func boolCmp(a, b bool) int { return int(boolInt(a) - boolInt(b)) }

func (w *Move) sharesReady(ctx context.Context, m moveState, apps []db.Service) (bool, error) {
	to, err := w.St.Q.GetServer(ctx, m.ToServerID)
	if err != nil {
		return false, err
	}
	ready := true
	for _, s := range apps {
		rows, err := w.St.Q.EnvVarsFor(ctx, db.EnvVarsForParams{EnvironmentID: m.EnvironmentID, ServiceID: s.ID})
		if err != nil {
			return false, err
		}
		for _, v := range rows {
			if !strings.Contains(v.Ref, "/") || (v.ServiceID != "" && v.ServiceID != s.ID) {
				continue
			}
			t, err := app.FindRef(ctx, w.St.Q, m.EnvironmentID, v.Key, v.Ref)
			if err != nil {
				continue
			}
			if _, err := w.St.Q.GetShare(ctx, db.GetShareParams{ServiceID: t.DB.ID, ServerID: m.ToServerID}); err == nil {
				continue
			}
			ready = false
			if last, err := w.St.Q.ListDeploysForService(ctx, db.ListDeploysForServiceParams{ServiceID: t.DB.ID, Limit: 1}); err != nil {
				return false, err
			} else if len(last) == 1 && last[0].CreatedAt >= m.UpdatedAt && (last[0].State == "failed" || last[0].State == "rolled_back") {
				return false, w.failAfter(ctx, m, s.ID, fmt.Sprintf("%s could not share its address with %s: %s", t.DB.Name, to.Name, last[0].Cause))
			}
			if _, err := w.App.ShareRef(ctx, t, s.Name, m.ToServerID, to.Name, "system"); err != nil {
				return false, err
			}
		}
	}
	return ready, nil
}

func redeployReferencing(ctx context.Context, q *db.Queries, envID string, now int64) error {
	e, err := q.EnvProject(ctx, envID)
	if err != nil {
		return err
	}
	svcs, err := q.ListServicesInEnv(ctx, envID)
	if err != nil {
		return err
	}
	for _, s := range svcs {
		if !spec.IsDatabase(spec.Kind(s.Kind)) {
			continue
		}
		apps, err := q.ReferencingApps(ctx, db.ReferencingAppsParams{ProjectID: e.ProjectID, Ref: e.Name + "/" + s.Name + ".url"})
		if err != nil {
			return err
		}
		for _, a := range apps {
			cur, err := q.GetDeploy(ctx, a.CurrentDeployID.String)
			if err != nil {
				return err
			}
			if err := q.CreateRollbackDeploy(ctx, db.CreateRollbackDeployParams{ID: store.NewID("dep_"), ServiceID: a.ID, CommitSha: cur.CommitSha,
				Subject: cur.Subject, Image: cur.Image, Digest: cur.Digest, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
		}
	}
	return nil
}

func cleanKeyNotReplayed(moveID, svcID string, failedTries int) string {
	return fmt.Sprintf("move:%s:clean:%s:%d", moveID, svcID, failedTries)
}
