package workers

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/behzodhalil/yawl/internal/cron"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/deploy"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/spec"
)

type Sender interface {
	Send(ctx context.Context, serverID string, cmd proto.Command) (proto.Result, error)
}

type TokenSource interface {
	Token(ctx context.Context, repo string) (string, error)
}

type Runner struct {
	St          *store.Store
	Tokens      TokenSource
	RestoreKeys interface {
		TakeRestoreKey(deployID string) (string, bool)
	}
	V         *vault.Vault
	GW        Sender
	Log       *slog.Logger
	PublicURL string
	ImportDir string
	Now       func() time.Time
	holder    string

	mu      sync.Mutex
	running map[string]bool
	wg      sync.WaitGroup
}

const leaseTTL = time.Hour

const (
	pullStepTimeout           = 15 * time.Minute
	firstDatabaseStartTimeout = 3 * time.Minute
)

func NewRunner(st *store.Store, gw Sender, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{St: st, GW: gw, Log: log, Now: time.Now, holder: st.InstanceID(context.Background()), running: map[string]bool{}}
}

func (r *Runner) Tick(ctx context.Context) error {
	if app.Adopting(ctx, r.St.Q) {
		return nil
	}
	ds, err := r.St.Q.RunnableDeploys(ctx)
	if err != nil {
		return err
	}
	for _, d := range ds {
		r.mu.Lock()
		busy := r.running[d.ID]
		r.mu.Unlock()
		if busy {
			continue
		}
		ok, err := r.St.TakeLease(ctx, "deploy:"+d.ServiceID, r.holder, r.Now(), leaseTTL)
		if err != nil || !ok {
			continue
		}
		r.mu.Lock()
		r.running[d.ID] = true
		r.mu.Unlock()
		r.wg.Add(1)
		go func(id, svc string) {
			defer r.wg.Done()
			if err := r.Run(ctx, id); err != nil {
				r.Log.Error("deploy run", "deploy", id, "err", err)
			}
			r.St.Q.ReleaseLease(context.WithoutCancel(ctx), db.ReleaseLeaseParams{Name: "deploy:" + svc, Holder: r.holder})
			r.mu.Lock()
			delete(r.running, id)
			r.mu.Unlock()
		}(d.ID, d.ServiceID)
	}
	return nil
}

func (r *Runner) Wait() { r.wg.Wait() }

type job struct {
	d    db.Deploy
	svc  db.GetServiceRow
	ip   netip.Addr
	kind spec.Kind
	prev *db.Deploy
	done map[deploy.Step]bool

	release []string
	linked  []db.Service

	switched []byte
}

func (j *job) opts() deploy.Opts {
	rollback := j.d.Rollback == 1
	release := len(j.release) > 0 && !rollback
	return deploy.Opts{Restore: j.d.RestoreFrom != "", Import: j.d.ImportID != "", Release: release, Snapshot: release && len(j.linked) > 0, Rollback: rollback, Pull: j.svc.Image != "",
		Volumes: len(j.volumes()) > 0 && (j.kind == spec.KindWeb || j.kind == spec.KindWorker)}
}

func (j *job) volumes() []proto.Volume {
	var vs []proto.Volume
	json.Unmarshal([]byte(j.svc.VolumesJson), &vs)
	return vs
}

func (j *job) command() []string {
	var c []string
	json.Unmarshal([]byte(j.svc.CommandJson), &c)
	if len(c) == 0 {
		return nil
	}
	return c
}

func (j *job) repo() string {
	return fmt.Sprintf("yawl/%s-%s-%s", j.svc.Project, j.svc.Env, j.svc.Name)
}

func (r *Runner) Run(ctx context.Context, deployID string) error {
	j, err := r.load(ctx, deployID)
	if err != nil {
		return err
	}
	if deploy.Terminal(deploy.State(j.d.State)) {
		return nil
	}
	if j.d.State == string(deploy.Queued) {
		prevID := ""
		if j.prev != nil {
			prevID = j.prev.ID
		}
		now := r.Now().UnixMilli()
		if n, err := r.St.Q.StartDeploy(ctx, db.StartDeployParams{State: string(deploy.Building), StartedAt: sql.NullInt64{Int64: now, Valid: true}, PrevDeployID: prevID, UpdatedAt: now, ID: deployID}); err != nil {
			return err
		} else if n == 0 {
			return nil
		}
	}
	for _, step := range deploy.StepsFor(j.kind, j.opts()) {
		if j.done[step] {
			continue
		}
		cancelable := step == deploy.StepBuild || step == deploy.StepPull
		if cancelable && r.cancelAsked(ctx, deployID) {
			return r.cancel(ctx, deployID, step)
		}
		sctx, stop := ctx, func() {}
		if cancelable {
			sctx, stop = r.watchCancel(ctx, deployID)
		}
		res, err := r.step(sctx, j, step)
		stop()
		if errors.Is(err, errCanceled) || cancelable && ctx.Err() == nil && r.cancelAsked(ctx, deployID) {
			return r.cancel(ctx, deployID, step)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			res = proto.Result{Cause: "the agent did not answer: " + err.Error()}
		}
		if !res.OK {
			return r.fail(ctx, j, step, res.Cause)
		}
		if err := r.record(ctx, j, step, res); err != nil {
			return err
		}
	}
	return r.finish(ctx, j)
}

func (r *Runner) load(ctx context.Context, id string) (*job, error) {
	d, err := r.St.Q.GetDeploy(ctx, id)
	if err != nil {
		return nil, err
	}
	svc, err := r.St.Q.GetService(ctx, d.ServiceID)
	if err != nil {
		return nil, err
	}
	srv, err := r.St.Q.GetServer(ctx, svc.ServerID)
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(srv.PublicIp)
	if err != nil {
		return nil, err
	}
	j := &job{d: d, svc: svc, ip: ip, kind: spec.Kind(svc.Kind), done: map[deploy.Step]bool{}}
	json.Unmarshal([]byte(svc.ReleaseJson), &j.release)
	if len(j.release) > 0 {
		if j.linked, err = r.linkedDB(ctx, svc); err != nil {
			return nil, err
		}
	}
	prevID := d.PrevDeployID
	if d.State == string(deploy.Queued) && svc.CurrentDeployID.Valid && svc.CurrentDeployID.String != id {
		prevID = svc.CurrentDeployID.String
	}
	if d.MoveID != "" {
		prevID = ""
	}
	if d.State == string(deploy.Queued) && svc.Stopped == 1 {
		prevID = ""
	}
	if prevID != "" {
		p, err := r.St.Q.GetDeploy(ctx, prevID)
		if err == nil {
			j.prev = &p
		}
	}
	steps, err := r.St.Q.DeploySteps(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, s := range steps {
		if s.State == "ok" {
			j.done[deploy.Step(s.Step)] = true
		}
	}
	return j, nil
}

func (r *Runner) step(ctx context.Context, j *job, step deploy.Step) (proto.Result, error) {
	now := r.Now().UnixMilli()
	if n, err := r.St.Q.AdvanceDeploy(ctx, db.AdvanceDeployParams{State: string(deploy.After(step)), UpdatedAt: now, ID: j.d.ID}); err != nil {
		return proto.Result{}, err
	} else if n == 0 {
		return proto.Result{}, errCanceled
	}
	if err := r.St.Q.AddDeployStep(ctx, db.AddDeployStepParams{ID: store.NewID("stp_"), DeployID: j.d.ID, Step: string(step), StartedAt: now, CreatedAt: now, UpdatedAt: now}); err != nil {
		return proto.Result{}, err
	}
	prevContainer := ""
	if j.prev != nil {
		prevContainer = j.prev.Container
	}
	timeout := 2 * time.Minute
	var name string
	var args any
	lost := ""
	switch step {
	case deploy.StepBuild:
		vars, err := r.vars(ctx, j)
		if c, ok := err.(errCause); ok {
			return proto.Result{Cause: string(c)}, nil
		} else if err != nil {
			return proto.Result{}, err
		}
		build, keys := []proto.SealedVar{}, []string{}
		for _, v := range vars {
			keys = append(keys, v.Key)
			if v.Build {
				build = append(build, v)
			}
		}
		token := ""
		if r.Tokens != nil {
			if token, err = r.Tokens.Token(ctx, j.svc.Repo); err != nil {
				return proto.Result{Cause: err.Error()}, nil
			}
		}
		ba := proto.BuildArgs{Builder: j.svc.Builder, Root: j.svc.Root, NoCache: j.d.NoCache == 1, Repo: j.svc.Repo, Branch: j.svc.Branch, Commit: j.d.CommitSha, Image: j.repo() + ":" + j.d.ID, Vars: build, Token: token,
			Keys: keys, Env: j.svc.Env}
		bs, home, err := r.buildServer(ctx, j, build)
		if err != nil {
			return proto.Result{}, err
		}
		if cause := r.rootUnknown(ctx, j.svc.Root, cmp.Or(bs, j.svc.ServerID)); cause != "" {
			return proto.Result{Cause: cause}, nil
		}
		if bs != "" {
			return r.buildElsewhere(ctx, j, bs, ba)
		} else if home != "" {
			res, err := r.send(ctx, j, string(step), proto.CmdBuild, ba, 30*time.Minute)
			return withWarning(res, home), err
		}
		name, timeout, args = proto.CmdBuild, 30*time.Minute, ba
	case deploy.StepPull:
		pa := proto.PullArgs{Ref: j.svc.Image, Tag: j.repo() + ":" + j.d.ID}
		if j.d.MoveID != "" && j.svc.CurrentDeployID.Valid {
			if cur, err := r.St.Q.GetDeploy(ctx, j.svc.CurrentDeployID.String); err == nil && cur.Digest != "" {
				pa.Ref = spec.ImageName(j.svc.Image) + "@" + cur.Digest
			}
		}
		if r.V != nil {
			auth, err := app.RegistryAuth(ctx, r.St.Q, r.V, spec.RegistryHost(j.svc.Image))
			if err != nil {
				return proto.Result{}, err
			}
			pa.Auth = auth
		}
		name, timeout, args = proto.CmdPull, pullStepTimeout, pa
	case deploy.StepEnsure:
		if j.d.Restart == 1 {
			stop := proto.StopArgs{Container: fmt.Sprintf("yawl-%s-%s-%s", j.svc.Project, j.svc.Env, j.svc.Name)}
			if res, err := r.send(ctx, j, "restart-stop", proto.CmdStop, stop, 2*time.Minute); err != nil || !res.OK {
				return res, err
			}
		}
		rec, err := r.St.Q.GetSetting(ctx, "recovery.pubkey")
		if err != nil {
			return proto.Result{Cause: "no recovery key yet; run yawl recovery-key init first"}, nil
		}
		pw, _ := app.DatabaseVar(ctx, r.St.Q, j.svc.EnvironmentID, j.svc.ID, "PASSWORD")
		name, timeout = proto.CmdEnsureDatabase, pullStepTimeout+firstDatabaseStartTimeout
		var allow []string
		json.Unmarshal([]byte(j.svc.AllowJson), &allow)
		ea := proto.EnsureDatabaseArgs{Kind: j.svc.Kind, PITR: j.svc.Pitr == 1, CPU: j.svc.Cpu, Memory: j.svc.Memory, Project: j.svc.Project, Env: j.svc.Env, Name: j.svc.Name, Version: int(j.svc.Version), Password: pw, RecoveryPubkey: rec,
			OutsidePort: int(j.svc.OutsidePort), Allow: allow, PublicIP: j.ip.String()}
		if j.svc.Kind == string(spec.KindPostgres) {
			var with map[string]string
			if ea.PrivateIP, with, ea.ShareHere, err = r.shares(ctx, j); err != nil {
				return proto.Result{}, err
			}
			if j.svc.PrivatePort != 0 {
				ea.PrivatePort, ea.ShareWith = int(j.svc.PrivatePort), with
			}
		} else {
			ea.PublicIP = ""
		}
		args = ea
	case deploy.StepRestore:
		key, ok := "", false
		if r.RestoreKeys != nil {
			key, ok = r.RestoreKeys.TakeRestoreKey(j.d.ID)
		}
		if !ok {
			lost = "the recovery key was not kept across a server restart; run yawl db restore again"
			if j.d.MoveID != "" {
				lost = "the recovery key was not kept across a server restart; retry the move and enter it again"
			}
		}
		b, err := r.St.Q.GetBackup(ctx, j.d.RestoreFrom)
		if err != nil {
			return proto.Result{}, err
		}
		storage, err := r.restoreStorage(ctx, j)
		if err != nil {
			return proto.Result{Cause: err.Error()}, nil
		}
		pw, err := app.DatabaseVar(ctx, r.St.Q, j.svc.EnvironmentID, j.svc.ID, "PASSWORD")
		if err != nil {
			return proto.Result{Cause: j.svc.Name + " has no password yet"}, nil
		}
		name, timeout = proto.CmdRestore, 2*time.Hour
		ra := proto.RestoreArgs{Kind: j.svc.Kind, Project: j.svc.Project, Env: j.svc.Env, Name: j.svc.Name, Version: int(j.svc.Version),
			Password: pw, Storage: storage, Key: b.ObjectKey, SHA256: b.Sha256, RecoveryKey: key}
		if b.Kind == "base" {
			src, err := r.St.Q.GetService(ctx, b.ServiceID)
			if err != nil {
				return proto.Result{}, err
			}
			batches, err := r.St.Q.ListWALBatches(ctx, src.ID)
			if err != nil {
				return proto.Result{}, err
			}
			spw, err := app.DatabaseVar(ctx, r.St.Q, src.EnvironmentID, src.ID, "PASSWORD")
			if err != nil {
				return proto.Result{Cause: src.Name + " has no password on this server"}, nil
			}
			at := time.UnixMilli(j.d.RestoreAt).UTC()
			wal, sums, _ := app.WALFor(batches, b, at)
			ra.PITR = &proto.PITRRestore{WAL: wal, WALSHA256: sums, At: at.Format("2006-01-02 15:04:05+00"), SourcePassword: spw}
		}
		if j.svc.Kind != string(spec.KindPostgres) || ra.PITR != nil {
			rec, _ := r.St.Q.GetSetting(ctx, "recovery.pubkey")
			ra.Ensure = &proto.EnsureDatabaseArgs{Kind: j.svc.Kind, CPU: j.svc.Cpu, Memory: j.svc.Memory, Project: j.svc.Project, Env: j.svc.Env,
				Name: j.svc.Name, Version: int(j.svc.Version), Password: pw, RecoveryPubkey: rec}
		}
		args = ra
	case deploy.StepRestoreVolume:
		key, ok := "", false
		if r.RestoreKeys != nil {
			key, ok = r.RestoreKeys.TakeRestoreKey(j.d.ID)
		}
		if !ok {
			lost = "the recovery key was not kept across a server restart; start the restore again"
		}
		b, err := r.St.Q.GetBackup(ctx, j.d.RestoreFrom)
		if err != nil {
			return proto.Result{}, err
		}
		storage, err := r.restoreStorage(ctx, j)
		if err != nil {
			return proto.Result{Cause: err.Error()}, nil
		}
		name, timeout = proto.CmdRestoreVolume, 2*time.Hour
		args = proto.RestoreVolumeArgs{Project: j.svc.Project, Env: j.svc.Env, Service: j.svc.Name, Name: j.d.Volume,
			Storage: storage, Key: b.ObjectKey, SHA256: b.Sha256, RecoveryKey: key}
	case deploy.StepSnapshot:
		type target struct {
			id, name, kind string
			version        int64
		}
		var targets []target
		for _, l := range j.linked {
			targets = append(targets, target{l.ID, l.Name, l.Kind, l.Version})
		}
		if j.d.ImportID != "" {
			targets = []target{{j.svc.ID, j.svc.Name, j.svc.Kind, j.svc.Version}}
		}
		var paths []string
		for _, l := range targets {
			pw, err := app.DatabaseVar(ctx, r.St.Q, j.svc.EnvironmentID, l.id, "PASSWORD")
			if errors.Is(err, sql.ErrNoRows) {
				return proto.Result{Cause: l.name + " has not been created yet. Deploy " + l.name + " first."}, nil
			} else if err != nil {
				return proto.Result{}, err
			}
			res, err := r.send(ctx, j, "snapshot:"+l.name, proto.CmdSnapshot,
				proto.SnapshotArgs{Kind: l.kind, Project: j.svc.Project, Env: j.svc.Env, Name: l.name, Version: int(l.version), Password: pw}, time.Hour)
			if err != nil || !res.OK {
				return res, err
			}
			var sd proto.SnapshotData
			if err := json.Unmarshal(res.Data, &sd); err != nil {
				return proto.Result{}, err
			}
			paths = append(paths, sd.Path)
		}
		return proto.Result{OK: true, Output: strings.Join(paths, "\n")}, nil
	case deploy.StepImport:
		imp, err := r.St.Q.GetImport(ctx, j.d.ImportID)
		if err != nil {
			return proto.Result{}, err
		}
		pw, err := app.DatabaseVar(ctx, r.St.Q, j.svc.EnvironmentID, j.svc.ID, "PASSWORD")
		if err != nil {
			return proto.Result{}, err
		}
		a := proto.ImportArgs{Kind: j.svc.Kind, Project: j.svc.Project, Env: j.svc.Env, Name: j.svc.Name, Version: int(j.svc.Version), Password: pw}
		switch imp.Source {
		case "upload":
			a.URL = strings.TrimSuffix(r.PublicURL, "/") + "/agent/imports/" + imp.ID + "?t=" + imp.Token
		case "url":
			a.URL = imp.Ref
		default:
			a.Path = imp.Ref
		}
		name, args, timeout = proto.CmdImport, a, 12*time.Hour
	case deploy.StepRelease:
		vars, err := r.vars(ctx, j)
		if c, ok := err.(errCause); ok {
			return proto.Result{Cause: string(c)}, nil
		} else if err != nil {
			return proto.Result{}, err
		}
		rt := time.Duration(j.svc.ReleaseTimeoutMs) * time.Millisecond
		name, timeout = proto.CmdRelease, rt+time.Minute
		nets, err := RefNetworks(ctx, r.St.Q, j.svc.EnvironmentID, j.svc.ID)
		if err != nil {
			return proto.Result{}, err
		}
		args = proto.ReleaseArgs{CPU: j.svc.Cpu, Memory: j.svc.Memory, Project: j.svc.Project, Env: j.svc.Env, Service: j.svc.Name, Image: j.d.Image, Kind: j.svc.Kind, Deploy: j.d.ID,
			Port: int(j.svc.Port), Cmd: j.release, Vars: vars, Timeout: rt, Volumes: j.volumes(), Networks: nets}
	case deploy.StepSchedule:
		vars, err := r.vars(ctx, j)
		if c, ok := err.(errCause); ok {
			return proto.Result{Cause: string(c)}, nil
		} else if err != nil {
			return proto.Result{}, err
		}
		var cmd []string
		json.Unmarshal([]byte(j.svc.CommandJson), &cmd)
		tz := j.svc.Timezone
		if tz == "" {
			tz = cron.PanelZone()
		}
		name = proto.CmdCronSchedule
		nets, err := RefNetworks(ctx, r.St.Q, j.svc.EnvironmentID, j.svc.ID)
		if err != nil {
			return proto.Result{}, err
		}
		args = proto.CronJob{CPU: j.svc.Cpu, Memory: j.svc.Memory, ID: j.svc.ID, Project: j.svc.Project, Env: j.svc.Env, Service: j.svc.Name, Image: j.d.Image,
			Schedule: j.svc.Schedule, Timezone: tz, Cmd: cmd, Vars: vars, Timeout: time.Duration(j.svc.TimeoutMs) * time.Millisecond,
			Label: j.svc.Project + "/" + j.svc.Env + "/" + j.svc.Name, Networks: nets}
	case deploy.StepStopOld:
		name, args = proto.CmdStop, proto.StopArgs{Container: prevContainer}
	case deploy.StepStart:
		vars, err := r.vars(ctx, j)
		if c, ok := err.(errCause); ok {
			return proto.Result{Cause: string(c)}, nil
		} else if err != nil {
			return proto.Result{}, err
		}
		nets, err := RefNetworks(ctx, r.St.Q, j.svc.EnvironmentID, j.svc.ID)
		if err != nil {
			return proto.Result{}, err
		}
		name = proto.CmdStart
		if err := r.St.Q.SetDeployLimits(ctx, db.SetDeployLimitsParams{Cpu: j.svc.Cpu, Memory: j.svc.Memory, UpdatedAt: r.Now().UnixMilli(), ID: j.d.ID}); err != nil {
			return proto.Result{}, err
		}
		args = proto.StartArgs{CPU: j.svc.Cpu, Memory: j.svc.Memory, Project: j.svc.Project, Env: j.svc.Env, Service: j.svc.Name, Deploy: j.d.ID, Image: j.d.Image, Kind: j.svc.Kind, Port: int(j.svc.Port), Vars: vars, Replicas: int(j.svc.Replicas),
			Volumes: j.volumes(), Networks: nets, Cmd: j.command()}
	case deploy.StepCheck:
		ht := time.Duration(j.svc.HealthTimeoutMs) * time.Millisecond
		name, timeout = proto.CmdHealthCheck, ht+time.Minute
		args = proto.HealthArgs{Container: j.d.Container, Kind: j.svc.Kind, Path: j.svc.HealthPath, Port: int(j.svc.Port), Timeout: ht}
	case deploy.StepSwitch:
		name = proto.CmdSwitchRoute
		hosts, err := r.hosts(ctx, j)
		if err != nil {
			return proto.Result{}, err
		}
		opts, err := RouteOptions(ctx, r.St.Q, j.svc.ID, hosts)
		if err != nil {
			return proto.Result{}, err
		}
		j.switched, _ = json.Marshal(opts)
		args = proto.SwitchArgs{Hosts: hosts, Container: j.d.Container, Port: int(j.svc.Port), Options: opts}
	case deploy.StepDrain:
		dt := time.Duration(j.svc.DrainTimeoutMs) * time.Millisecond
		name, timeout = proto.CmdDrain, dt+time.Minute
		args = proto.DrainArgs{Container: prevContainer, Timeout: dt}
	default:
		return proto.Result{}, fmt.Errorf("no command for step %s", step)
	}
	res, err := r.send(ctx, j, string(step), name, args, timeout)
	if lost != "" && err == nil && !res.OK && strings.HasPrefix(res.Cause, "Cannot open the recovery key") {
		res.Cause = lost
	}
	return res, err
}

func (r *Runner) send(ctx context.Context, j *job, key, name string, args any, timeout time.Duration) (proto.Result, error) {
	cmd, err := proto.NewCommand(j.d.ID+":"+key, name, args)
	if err != nil {
		return proto.Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return r.GW.Send(ctx, j.svc.ServerID, cmd)
}

func (r *Runner) record(ctx context.Context, j *job, step deploy.Step, res proto.Result) error {
	now := r.Now().UnixMilli()
	return r.St.Tx(ctx, func(q *db.Queries) error {
		switch step {
		case deploy.StepPull:
			var p proto.PullData
			if err := json.Unmarshal(res.Data, &p); err != nil {
				return err
			}
			j.d.Image = j.repo() + ":" + j.d.ID
			if err := q.SetDeployImage(ctx, db.SetDeployImageParams{Image: j.d.Image, UpdatedAt: now, ID: j.d.ID}); err != nil {
				return err
			}
			if err := q.SetDeploySubject(ctx, db.SetDeploySubjectParams{Subject: j.svc.Image, UpdatedAt: now, ID: j.d.ID}); err != nil {
				return err
			}
			if err := q.SetDeployDigest(ctx, db.SetDeployDigestParams{Digest: p.Digest, UpdatedAt: now, ID: j.d.ID}); err != nil {
				return err
			}
		case deploy.StepBuild:
			var b proto.BuildData
			if err := json.Unmarshal(res.Data, &b); err != nil {
				return err
			}
			j.d.Image = b.Image
			if err := q.SetDeployImage(ctx, db.SetDeployImageParams{Image: b.Image, UpdatedAt: now, ID: j.d.ID}); err != nil {
				return err
			}
			if j.d.CommitSha == "" && b.Commit != "" {
				j.d.CommitSha = b.Commit
				if err := q.SetDeployCommit(ctx, db.SetDeployCommitParams{CommitSha: b.Commit, UpdatedAt: now, ID: j.d.ID}); err != nil {
					return err
				}
			}
			if b.Subject != "" {
				if err := q.SetDeploySubject(ctx, db.SetDeploySubjectParams{Subject: b.Subject, UpdatedAt: now, ID: j.d.ID}); err != nil {
					return err
				}
			}
			var lines []string
			for _, w := range b.Warnings {
				lines = append(lines, "warning: "+w)
				if err := app.Event(ctx, q, now, "deploy.warning", j.d.ID, "system", map[string]string{"service": j.svc.ID, "text": w}); err != nil {
					return err
				}
			}
			if len(lines) > 0 {
				res.Output = strings.Join(lines, "\n")
			}
		case deploy.StepSchedule:
			tz := j.svc.Timezone
			if tz == "" {
				tz = cron.PanelZone()
			}
			b, _ := json.Marshal(app.Scheduled{Schedule: j.svc.Schedule, Timezone: tz, TimeoutMs: j.svc.TimeoutMs})
			if err := q.SetSetting(ctx, db.SetSettingParams{Key: "cron.scheduled." + j.svc.ID, Value: string(b)}); err != nil {
				return err
			}
		case deploy.StepStart:
			var s proto.StartData
			if err := json.Unmarshal(res.Data, &s); err != nil {
				return err
			}
			j.d.Container = s.Container
			if err := q.SetDeployContainer(ctx, db.SetDeployContainerParams{Container: s.Container, UpdatedAt: now, ID: j.d.ID}); err != nil {
				return err
			}
			if kept, _ := json.Marshal(app.AllVolumes(j.svc.VolumesJson, j.svc.VolumesKept)); string(kept) != j.svc.VolumesKept && len(j.volumes()) > 0 {
				if err := q.SetVolumesKept(ctx, db.SetVolumesKeptParams{VolumesKept: string(kept), UpdatedAt: now, ID: j.svc.ID}); err != nil {
					return err
				}
			}
		case deploy.StepEnsure:
			var e proto.EnsureDatabaseData
			if err := json.Unmarshal(res.Data, &e); err != nil {
				return err
			}
			if e.Here != "" {
				if e.Shares == nil {
					e.Shares = map[string]string{}
				}
				e.Shares[j.svc.ServerID] = e.Here
			}
			for server, box := range e.Shares {
				if err := q.UpsertShare(ctx, db.UpsertShareParams{ServiceID: j.svc.ID, ServerID: server, UrlBox: box, UpdatedAt: now}); err != nil {
					return err
				}
			}
			if e.PasswordBox != "" {
				for _, v := range [][3]string{{"PASSWORD", e.PasswordBox, e.PasswordRecoveryBox}, {"URL", e.URLBox, e.URLRecoveryBox}} {
					if err := q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: store.NewID("var_"), EnvironmentID: j.svc.EnvironmentID,
						ServiceID: j.svc.ID, Key: v[0], AgentBox: v[1], RecoveryBox: v[2], CreatedAt: now, UpdatedAt: now}); err != nil {
						return err
					}
				}
			}
			if e.OutsideURLBox != "" {
				if err := q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: store.NewID("var_"), EnvironmentID: j.svc.EnvironmentID,
					ServiceID: j.svc.ID, Key: "OUTSIDE_URL", AgentBox: e.OutsideURLBox, RecoveryBox: e.OutsideURLRecoveryBox, CreatedAt: now, UpdatedAt: now}); err != nil {
					return err
				}
			} else if _, err := q.DeleteEnvVar(ctx, db.DeleteEnvVarParams{EnvironmentID: j.svc.EnvironmentID, ServiceID: j.svc.ID, Key: "OUTSIDE_URL"}); err != nil {
				return err
			}
		case deploy.StepSnapshot:
			if err := app.Event(ctx, q, now, "deploy.snapshot", j.d.ID, "system", map[string]any{"service": j.svc.ID, "paths": strings.Split(res.Output, "\n")}); err != nil {
				return err
			}
		case deploy.StepSwitch:
			if err := q.SetCurrentDeploy(ctx, db.SetCurrentDeployParams{CurrentDeployID: sql.NullString{String: j.d.ID, Valid: true}, UpdatedAt: now, ID: j.svc.ID}); err != nil {
				return err
			}
		}
		return q.FinishDeployStep(ctx, db.FinishDeployStepParams{State: "ok", Output: res.Output, FinishedAt: sql.NullInt64{Int64: now, Valid: true}, UpdatedAt: now, DeployID: j.d.ID, Step: string(step)})
	})
}

func (r *Runner) finish(ctx context.Context, j *job) error {
	defer r.resendRoutes(ctx, j)
	defer r.dropUpload(ctx, j)
	if !spec.IsDatabase(j.kind) {
		if keep, err := r.keepImages(ctx, j); err == nil {
			r.send(ctx, j, "prune", proto.CmdPrune, proto.PruneArgs{Repo: j.repo(), Keep: keep}, 2*time.Minute)
		}
	}
	now := r.Now().UnixMilli()
	label := j.svc.Project + "/" + j.svc.Env + "/" + j.svc.Name
	site, took := "", ""
	if j.kind == spec.KindWeb && j.svc.PreviewOf == "" {
		if hosts, err := r.hosts(ctx, j); err == nil {
			site = "https://" + hosts[len(hosts)-1]
		}
	}
	if j.d.StartedAt.Valid {
		took = Took(time.Duration(now-j.d.StartedAt.Int64) * time.Millisecond)
	}
	live := app.DeployLive(label, j.d.ID, j.d.CommitSha, took, site, j.svc.Project+"-"+j.svc.Name+" deployed.")
	return r.St.Tx(ctx, func(q *db.Queries) error {
		if j.kind != spec.KindWeb {
			if err := q.SetCurrentDeploy(ctx, db.SetCurrentDeployParams{CurrentDeployID: sql.NullString{String: j.d.ID, Valid: true}, UpdatedAt: now, ID: j.svc.ID}); err != nil {
				return err
			}
		}
		if err := q.SetDeployState(ctx, db.SetDeployStateParams{State: string(deploy.Live), FinishedAt: sql.NullInt64{Int64: now, Valid: true}, UpdatedAt: now, ID: j.d.ID}); err != nil {
			return err
		}
		if err := q.ClearStopDone(ctx, j.svc.ID); err != nil {
			return err
		}
		if err := app.Event(ctx, q, now, "deploy.live", j.d.ID, "system", map[string]string{"service": j.svc.ID}); err != nil {
			return err
		}
		if j.svc.PreviewOf != "" {
			return nil
		}
		return announceIncident(ctx, q, now, "deploy.recovered", j.d.ID, nil, live, "deploy:"+j.svc.ID, "close", label, true)
	})
}

func (r *Runner) restoreStorage(ctx context.Context, j *job) (proto.Storage, error) {
	if j.d.RestoreStorage == "" {
		return storageFrom(ctx, r.St.Q, r.V)
	}
	for _, c := range copyStorages(ctx, r.St.Q, r.V) {
		if c.name == j.d.RestoreStorage {
			return c.storage, nil
		}
	}
	return proto.Storage{}, fmt.Errorf("the copy %s is no longer in Settings > Backup storage", j.d.RestoreStorage)
}

func (r *Runner) SweepUploads(ctx context.Context, now time.Time) {
	if ended, err := r.St.Q.EndedUploads(ctx); err == nil {
		for _, u := range ended {
			os.Remove(u.File)
			r.St.Q.ClearImportFile(ctx, u.ID)
		}
	}
	if r.ImportDir == "" {
		return
	}
	named, err := r.St.Q.ImportFiles(ctx)
	if err != nil {
		return
	}
	ents, _ := os.ReadDir(r.ImportDir)
	for _, e := range ents {
		path := filepath.Join(r.ImportDir, e.Name())
		info, err := e.Info()
		if err != nil || !strings.HasPrefix(e.Name(), "upl_") || slices.Contains(named, path) || now.Sub(info.ModTime()) < 24*time.Hour {
			continue
		}
		os.Remove(path)
	}
}

func (r *Runner) dropUpload(ctx context.Context, j *job) {
	if j.d.ImportID == "" {
		return
	}
	if imp, err := r.St.Q.GetImport(ctx, j.d.ImportID); err == nil && imp.File != "" {
		os.Remove(imp.File)
	}
}

func (r *Runner) fail(ctx context.Context, j *job, step deploy.Step, cause string) error {
	defer r.dropUpload(ctx, j)
	if j.d.Rollback == 1 && step == deploy.StepStart && strings.Contains(cause, "No such image") {
		cause = fmt.Sprintf("The image of %s is no longer kept (Yawl keeps the last 3).", imageDeploy(j.d.Image))
	}
	end := deploy.OnFailWith(j.kind, j.opts(), step)
	if end == deploy.RolledBack && j.prev == nil {
		end = deploy.Failed
	}
	back := end == deploy.RolledBack || step == deploy.StepSwitch && j.prev != nil
	serving := false
	if back {
		if err := r.rollback(ctx, j); err != nil {
			cause += "; rollback also failed: " + err.Error()
			end, serving = deploy.Failed, true
		}
	}
	if !serving && end == deploy.Failed && j.d.Container != "" {
		r.send(ctx, j, "cleanup", proto.CmdStop, proto.StopArgs{Container: j.d.Container}, 2*time.Minute)
	}
	now := r.Now().UnixMilli()
	text := message(j, end, cause, serving)
	label := j.svc.Project + "/" + j.svc.Env + "/" + j.svc.Name
	msg := app.DeployFailed(label, j.d.ID, j.d.CommitSha, string(step), cause, stillServing(j, end, serving), end == deploy.RolledBack, text)
	return r.St.Tx(ctx, func(q *db.Queries) error {
		fin := sql.NullInt64{Int64: now, Valid: true}
		if err := q.SetDeployState(ctx, db.SetDeployStateParams{State: string(end), Cause: cause, FinishedAt: fin, UpdatedAt: now, ID: j.d.ID}); err != nil {
			return err
		}
		if err := q.FinishDeployStep(ctx, db.FinishDeployStepParams{State: "failed", Output: cause, FinishedAt: fin, UpdatedAt: now, DeployID: j.d.ID, Step: string(step)}); err != nil {
			return err
		}
		if err := q.ClearStopDone(ctx, j.svc.ID); err != nil {
			return err
		}
		evID := store.NewID("evt_")
		b, _ := json.Marshal(map[string]string{"service": j.svc.ID, "step": string(step), "cause": cause})
		if err := q.InsertEvent(ctx, db.InsertEventParams{ID: evID, Kind: "deploy." + string(end), SubjectID: j.d.ID, Actor: "system", BodyJson: string(b), CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if j.svc.PreviewOf != "" {
			return nil
		}
		return q.InsertOutbox(ctx, db.InsertOutboxParams{ID: store.NewID("obx_"), EventID: evID, Channel: "telegram", Body: text, MessageJson: msg.JSON(now), NextAttemptAt: now,
			Incident: "deploy:" + j.svc.ID, Action: "open", Label: label, CreatedAt: now, UpdatedAt: now})
	})
}

func (r *Runner) rollback(ctx context.Context, j *job) error {
	now := r.Now().UnixMilli()
	if j.kind == spec.KindWorker || j.opts().Volumes {
		vars, err := r.vars(ctx, j)
		if err != nil {
			return err
		}
		if j.d.Container != "" {
			res, err := r.send(ctx, j, "rollback-stop", proto.CmdStop, proto.StopArgs{Container: j.d.Container}, 2*time.Minute)
			if err := failed(res, err); err != nil {
				return fmt.Errorf("the new version did not stop: %w", err)
			}
		}
		res, err := r.send(ctx, j, "rollback-start", proto.CmdStart, proto.StartArgs{CPU: j.prev.Cpu, Memory: j.prev.Memory, Project: j.svc.Project, Env: j.svc.Env,
			Service: j.svc.Name, Deploy: j.prev.ID + "-r-" + j.d.ID, Image: j.prev.Image, Kind: j.svc.Kind, Port: int(j.svc.Port), Vars: vars, Replicas: int(j.svc.Replicas),
			Volumes: j.volumes(), Cmd: j.command()}, 2*time.Minute)
		if err := failed(res, err); err != nil {
			return err
		}
		var s proto.StartData
		json.Unmarshal(res.Data, &s)
		if err := r.St.Q.SetDeployContainer(ctx, db.SetDeployContainerParams{Container: s.Container, UpdatedAt: now, ID: j.prev.ID}); err != nil {
			return err
		}
		if j.kind == spec.KindWorker {
			return nil
		}
		j.prev.Container = s.Container
	}
	hosts, err := r.hosts(ctx, j)
	if err != nil {
		return err
	}
	opts, err := RouteOptions(ctx, r.St.Q, j.svc.ID, hosts)
	if err != nil {
		return err
	}
	res, err := r.send(ctx, j, "rollback", proto.CmdSwitchRoute, proto.SwitchArgs{
		Hosts: hosts, Container: j.prev.Container, Port: int(j.svc.Port), Options: opts}, 2*time.Minute)
	if err := failed(res, err); err != nil {
		return err
	}
	return r.St.Q.SetCurrentDeploy(ctx, db.SetCurrentDeployParams{CurrentDeployID: sql.NullString{String: j.prev.ID, Valid: true}, UpdatedAt: now, ID: j.svc.ID})
}

func failed(res proto.Result, err error) error {
	if err != nil {
		return err
	}
	if !res.OK {
		return errors.New(res.Cause)
	}
	return nil
}

func message(j *job, end deploy.State, cause string, serving bool) string {
	name := j.svc.Project + "-" + j.svc.Name
	cause = strings.TrimSuffix(strings.TrimSpace(cause), ".")
	switch {
	case serving:
		return fmt.Sprintf("%s deploy failed: %s. The new version may still be serving.", name, cause)
	case j.d.MoveID != "":
		return fmt.Sprintf("%s deploy failed: %s. It did not start on the new server.", name, cause)
	case end == deploy.RolledBack:
		return fmt.Sprintf("%s deploy rolled back: %s.", name, cause)
	case j.prev == nil:
		return fmt.Sprintf("%s deploy failed: %s. Nothing was live before this deploy.", name, cause)
	default:
		return fmt.Sprintf("%s deploy failed: %s. The previous version is still live.", name, cause)
	}
}

func stillServing(j *job, end deploy.State, serving bool) string {
	switch {
	case serving:
		return "the new version may still answer"
	case j.d.MoveID != "":
		return "the old server"
	case j.prev == nil:
		return "nothing"
	case j.prev.CommitSha != "":
		return j.prev.CommitSha
	}
	return "the previous version"
}

func (r *Runner) vars(ctx context.Context, j *job) ([]proto.SealedVar, error) {
	return ServiceVars(ctx, r.St.Q, j.svc.EnvironmentID, j.svc.ID)
}

func ServiceVars(ctx context.Context, q *db.Queries, envID, serviceID string) ([]proto.SealedVar, error) {
	rows, err := q.EnvVarsFor(ctx, db.EnvVarsForParams{EnvironmentID: envID, ServiceID: serviceID})
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	var out []proto.SealedVar
	server, err := q.EnvironmentServer(ctx, envID)
	if err != nil {
		return nil, err
	}
	shared, err := q.SharedVarsFor(ctx, server)
	if err != nil {
		return nil, err
	}
	for _, v := range shared {
		if v.SealedTo != "" && v.SealedTo != v.CurrentKey {
			return nil, errCause(fmt.Sprintf("%s is a %s variable sealed before this server joined again; set it again in Shared variables.", v.Key, v.Scope))
		}
		if i, ok := idx[v.Key]; ok {
			out[i].Box, out[i].Build = v.AgentBox, v.Build == 1
			continue
		}
		idx[v.Key] = len(out)
		out = append(out, proto.SealedVar{Key: v.Key, Box: v.AgentBox, Build: v.Build == 1})
	}
	sources, err := q.SourcesForEnv(ctx, envID)
	if err != nil {
		return nil, err
	}
	for _, src := range sources {
		out = append(out, proto.SealedVar{Key: src.Provider, Box: src.AgentBox,
			Source: &proto.SecretSource{Provider: src.Provider, URL: src.Url, Path: src.Path, Workspace: src.Workspace, Env: src.Env}})
	}
	for _, v := range rows {
		box := v.AgentBox
		if v.Ref != "" {
			if box, err = resolve(ctx, q, envID, v.Key, v.Ref); err != nil {
				return nil, err
			}
		}
		if i, ok := idx[v.Key]; ok {
			out[i].Box, out[i].Build = box, v.Build == 1
			continue
		}
		idx[v.Key] = len(out)
		out = append(out, proto.SealedVar{Key: v.Key, Box: box, Build: v.Build == 1})
	}
	return out, nil
}

const keptImages = 3

// keepImages are the images of the service's newest live deploys, this one
// first: chosen by deploy, never by image time, which BuildKit's cache reuses.
func (r *Runner) keepImages(ctx context.Context, j *job) ([]string, error) {
	live, err := r.St.Q.LiveImages(ctx, j.svc.ID)
	if err != nil {
		return nil, err
	}
	var keep []string
	for _, im := range append([]string{j.d.Image}, live...) {
		if im != "" && !slices.Contains(keep, im) && len(keep) < keptImages {
			keep = append(keep, im)
		}
	}
	return keep, nil
}

func (r *Runner) hosts(ctx context.Context, j *job) ([]string, error) {
	out := []string{app.FreeHost(j.svc.Name, j.svc.Env, j.ip)}
	ds, err := r.St.Q.ListDomains(ctx, j.svc.ID)
	for _, d := range ds {
		if d.DnsState == "ok" {
			out = append(out, d.Host)
		}
	}
	return out, err
}

func imageDeploy(image string) string {
	if i := strings.LastIndex(image, ":"); i >= 0 {
		return image[i+1:]
	}
	return image
}

func (r *Runner) linkedDB(ctx context.Context, svc db.GetServiceRow) ([]db.Service, error) {
	rows, err := r.St.Q.EnvVarsFor(ctx, db.EnvVarsForParams{EnvironmentID: svc.EnvironmentID, ServiceID: svc.ID})
	if err != nil {
		return nil, err
	}
	var out []db.Service
	for _, v := range rows {
		if v.Ref == "" {
			continue
		}
		t, err := r.St.Q.GetServiceInEnv(ctx, db.GetServiceInEnvParams{EnvironmentID: svc.EnvironmentID, Name: strings.TrimSuffix(v.Ref, ".url")})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			return nil, err
		}
		if t.Kind == "postgres" && !slices.ContainsFunc(out, func(s db.Service) bool { return s.ID == t.ID }) {
			out = append(out, t)
		}
	}
	return out, nil
}

type errCause string

func (e errCause) Error() string { return string(e) }

func resolve(ctx context.Context, q *db.Queries, envID, key, ref string) (string, error) {
	if strings.Contains(ref, "/") {
		return resolveCross(ctx, q, envID, key, ref)
	}
	name := strings.TrimSuffix(ref, ".url")
	missing := errCause(fmt.Sprintf("%s refers to %s, which has not been created yet. Deploy %s first.", key, name, name))
	target, err := q.GetServiceInEnv(ctx, db.GetServiceInEnvParams{EnvironmentID: envID, Name: name})
	if errors.Is(err, sql.ErrNoRows) {
		return "", missing
	} else if err != nil {
		return "", err
	}
	box, err := app.DatabaseVar(ctx, q, envID, target.ID, "URL")
	if errors.Is(err, sql.ErrNoRows) {
		return "", missing
	}
	return box, err
}

func resolveCross(ctx context.Context, q *db.Queries, envID, key, ref string) (string, error) {
	t, err := app.FindRef(ctx, q, envID, key, ref)
	if err != nil {
		return "", errCause(err.Error())
	}
	own, err := q.EnvProject(ctx, envID)
	if err != nil {
		return "", err
	}
	box, err := q.GetShare(ctx, db.GetShareParams{ServiceID: t.DB.ID, ServerID: own.ServerID})
	if errors.Is(err, sql.ErrNoRows) {
		srv, _ := q.GetServer(ctx, own.ServerID)
		return "", errCause(fmt.Sprintf("%s has not shared its address with %s yet; deploy %s first.", t.DB.Name, srv.Name, t.DB.Name))
	}
	return box, err
}

func RefNetworks(ctx context.Context, q *db.Queries, envID, serviceID string) ([]string, error) {
	rows, err := q.EnvVarsFor(ctx, db.EnvVarsForParams{EnvironmentID: envID, ServiceID: serviceID})
	if err != nil {
		return nil, err
	}
	own, err := q.EnvProject(ctx, envID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range rows {
		if !strings.Contains(v.Ref, "/") {
			continue
		}
		t, err := app.FindRef(ctx, q, envID, v.Key, v.Ref)
		if err != nil || t.ServerID != own.ServerID {
			continue
		}
		if n := "yawl-" + own.Project + "-" + t.EnvName; !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

func (r *Runner) shares(ctx context.Context, j *job) (string, map[string]string, bool, error) {
	srv, err := r.St.Q.GetServer(ctx, j.svc.ServerID)
	if err != nil {
		return "", nil, false, err
	}
	e, err := r.St.Q.EnvProject(ctx, j.svc.EnvironmentID)
	if err != nil {
		return "", nil, false, err
	}
	rows, err := r.St.Q.ReferencingServers(ctx, db.ReferencingServersParams{ProjectID: e.ProjectID, Ref: j.svc.Env + "/" + j.svc.Name + ".url"})
	if err != nil {
		return "", nil, false, err
	}
	with, here := map[string]string{}, false
	for _, row := range rows {
		if row.ServerID == j.svc.ServerID {
			here = true
			continue
		}
		with[row.ServerID] = row.AgentPubkey
	}
	return srv.PrivateIp, with, here, nil
}

func (r *Runner) resendRoutes(ctx context.Context, j *job) {
	if j.kind != spec.KindWeb || j.switched == nil {
		return
	}
	hosts, err := r.hosts(ctx, j)
	if err != nil {
		return
	}
	if now, err := RouteOptions(ctx, r.St.Q, j.svc.ID, hosts); err == nil {
		if b, _ := json.Marshal(now); string(b) != string(j.switched) {
			Reroute(ctx, r.St.Q, r.GW, j.svc.ID)
		}
	}
}

func (r *Runner) cancelAsked(ctx context.Context, id string) bool {
	d, err := r.St.Q.GetDeploy(ctx, id)
	return err == nil && d.CancelRequested == 1
}

func (r *Runner) watchCancel(ctx context.Context, id string) (context.Context, func()) {
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-cctx.Done():
				return
			case <-t.C:
				if r.cancelAsked(ctx, id) {
					cancel()
					return
				}
			}
		}
	}()
	return cctx, func() { close(done); cancel() }
}

var errCanceled = errors.New("canceled")

func (r *Runner) cancel(ctx context.Context, id string, step deploy.Step) error {
	now := r.Now().UnixMilli()
	fin := sql.NullInt64{Int64: now, Valid: true}
	return r.St.Tx(ctx, func(q *db.Queries) error {
		if _, err := q.CancelDeploy(ctx, db.CancelDeployParams{FinishedAt: fin, UpdatedAt: now, ID: id}); err != nil {
			return err
		}
		if err := q.FinishDeployStep(ctx, db.FinishDeployStepParams{State: "canceled", Output: "Canceled.", FinishedAt: fin, UpdatedAt: now, DeployID: id, Step: string(step)}); err != nil {
			return err
		}
		return app.Event(ctx, q, now, "deploy.canceled", id, "system", nil)
	})
}
