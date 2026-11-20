package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/behzodhalil/yawl/internal/agent/metrics"
	"github.com/behzodhalil/yawl/internal/agent/state"
	"io"
	"maps"
	"net/netip"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/fw"
	"github.com/behzodhalil/yawl/internal/agent/proxy"
	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/agent/secrets"
	"github.com/behzodhalil/yawl/internal/agent/wg"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
)

type Builder interface {
	Build(ctx context.Context, a proto.BuildArgs, secrets map[string][]byte) (proto.BuildData, error)
	Inspect(ctx context.Context, a proto.InspectArgs) (proto.InspectData, error)
}

type Dispatcher struct {
	termOnce sync.Once
	terms    *Terminals

	Keys        *secrets.Keyring
	rt          runtime.Runtime
	px          proxy.Proxy
	b           Builder
	Get         Getter
	Clock       Clock
	DBDir       string
	WatchFile   string
	CronFile    string
	St          *state.State
	Report      func(ctx context.Context, event string, j proto.CronJob, r proto.CronRun)
	cron        cronState
	Ring        *metrics.Ring
	Net         *wg.Mesh
	FW          *fw.Firewall
	SnapDir     string
	ImportDir   string
	TailnetAddr func() string
	HostRoot    string
	Units       Units
	StatusDir   string
	DrainFile   string
	drain       drainer
	logw        logWatcher
	DopplerURL  string
	SnapFile    func(path string) (io.WriteCloser, error)
	Resolver    Resolver
	CertCheck   func(ctx context.Context, host string) bool
	OwnAddrs    func() []netip.Addr
}

func NewDispatcher(rt runtime.Runtime, px proxy.Proxy, b Builder) *Dispatcher {
	return &Dispatcher{rt: rt, px: px, b: b, Get: HTTPGetter(), Clock: RealClock}
}

const stopGrace = 10 * time.Second

func (d *Dispatcher) Handle(ctx context.Context, c proto.Command) proto.Result {
	res := d.handle(ctx, c)
	res.Key = c.Key
	if d.Keys != nil {
		m := d.Keys.Masker()
		res.Cause, res.Output = m.Mask(res.Cause), m.Mask(res.Output)
	}
	return res
}

func (d *Dispatcher) handle(ctx context.Context, c proto.Command) proto.Result {
	switch c.Name {
	case proto.CmdBuild:
		var a proto.BuildArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		secrets := map[string][]byte{}
		for _, v := range a.Vars {
			val, cause := d.open(v)
			if cause != "" {
				return proto.Result{Cause: cause}
			}
			secrets[v.Key] = val
		}
		b, err := d.b.Build(ctx, a, secrets)
		if err != nil {
			return proto.Result{Cause: err.Error()}
		}
		if d.Keys != nil {
			for i := range b.Warnings {
				b.Warnings[i] = d.Keys.Masker().Mask(b.Warnings[i])
			}
			b.Subject = d.Keys.Masker().Mask(b.Subject)
		}
		if r := []rune(b.Subject); len(r) > 200 {
			b.Subject = string(r[:200])
		}
		b.Image = a.Image
		if a.Push != nil {
			var user, token string
			if a.Push.Auth != nil {
				user, token = a.Push.Auth.Username, a.Push.Auth.Token
			}
			if err := d.rt.PushAndUntag(ctx, a.Image, a.Push.Ref, user, token); err != nil {
				return proto.Result{Cause: pushCause(a.Push.Ref, err)}
			}
			b.Image = a.Push.Ref
		}
		return ok(b)

	case proto.CmdPull:
		var a proto.PullArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		var user, token string
		if a.Auth != nil {
			user, token = a.Auth.Username, a.Auth.Token
		}
		digest, err := d.rt.Pull(ctx, a.Ref, a.Tag, user, token)
		if err != nil {
			return proto.Result{Cause: pullCause(a.Ref, err, goruntime.GOARCH)}
		}
		return ok(proto.PullData{Digest: digest})

	case proto.CmdReport:
		return d.report(ctx)

	case proto.CmdHash:
		var a proto.HashArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.hash(a)

	case proto.CmdInspect:
		var a proto.InspectArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		data, err := d.b.Inspect(ctx, a)
		if err != nil {
			return proto.Result{Cause: err.Error()}
		}
		return ok(data)

	case proto.CmdRestore:
		var a proto.RestoreArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.restore(ctx, a)

	case proto.CmdTailscaleUp:
		var a proto.TailscaleUpArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.tailscaleUp(ctx, a)
	case proto.CmdTailscaleDown:
		return d.tailscaleDown(ctx)
	case proto.CmdTailscaleStatus:
		return d.tailscaleStatus(ctx)
	case proto.CmdOSStatus:
		return ok(d.osStatus(ctx))
	case proto.CmdOSUpgrade:
		return d.osUpgrade(ctx)
	case proto.CmdLogDrain:
		var a proto.LogDrainArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return proto.Result{Cause: err.Error()}
		}
		return d.setDrain(a)

	case proto.CmdCleanup:
		var a proto.CleanupArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		r, err := d.rt.Cleanup(ctx, runtime.CleanupOpts{StoppedFor: time.Hour, CacheFor: 7 * 24 * time.Hour, AllCache: a.AllCache})
		if err != nil {
			return proto.Result{Cause: err.Error()}
		}
		return ok(proto.CleanupData{Containers: r.Containers, Images: r.Images, Freed: r.Freed})

	case proto.CmdUnusedVolumes:
		names, err := d.rt.UnusedVolumes(ctx)
		if err != nil {
			return proto.Result{Cause: err.Error()}
		}
		return ok(proto.UnusedVolumesData{Names: names})

	case proto.CmdCopyBackup:
		var a proto.CopyBackupArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.copyBackup(ctx, a)

	case proto.CmdDeleteBackups:
		var a proto.DeleteBackupsArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.deleteBackups(ctx, a)

	case proto.CmdBackup:
		var a proto.BackupArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.backup(ctx, a)

	case proto.CmdImport:
		var a proto.ImportArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.importDump(ctx, a)

	case proto.CmdQuery:
		var a proto.QueryArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.query(ctx, a)

	case proto.CmdSnapshot:
		var a proto.SnapshotArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.snapshot(ctx, c.Key, a)

	case proto.CmdRelease:
		var a proto.ReleaseArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.release(ctx, a)

	case proto.CmdEnsureDatabase:
		var a proto.EnsureDatabaseArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.ensureDatabase(ctx, a)

	case proto.CmdStart:
		var a proto.StartArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		netName := "yawl-" + a.Project + "-" + a.Env
		if err := d.rt.EnsureNetwork(ctx, netName); err != nil {
			return proto.Result{Cause: err.Error()}
		}
		env, cause := d.env(a.Vars)
		if cause != "" {
			return proto.Result{Cause: cause}
		}
		if a.Kind == "web" {
			env = append(env, "PORT="+strconv.Itoa(a.Port))
		}
		var mounts []runtime.Mount
		for _, v := range a.Volumes {
			vol, err := proto.VolumeName(a.Project, a.Env, a.Service, v.Name)
			if err != nil {
				return proto.Result{Cause: err.Error()}
			}
			mounts = append(mounts, runtime.Mount{Source: vol, Target: v.Path, Volume: true})
		}
		if cause := yawlNetworks(a.Networks); cause != "" {
			return proto.Result{Cause: cause}
		}
		if cause := checkCPU(a.Service, a.CPU, goruntime.NumCPU()); cause != "" {
			return proto.Result{Cause: cause}
		}
		id, err := d.startCopies(ctx, runtime.ContainerSpec{
			Name: fmt.Sprintf("yawl-%s-%s-%s-%s", a.Project, a.Env, a.Service, a.Deploy), Image: a.Image,
			Network: netName, Env: env, Mounts: mounts, Networks: a.Networks, CPU: a.CPU, Memory: a.Memory, Cmd: a.Cmd,
			Aliases: []string{a.Service},
			Labels:  map[string]string{"yawl.project": a.Project, "yawl.env": a.Env, "yawl.service": a.Service, "yawl.deploy": a.Deploy},
		}, a.Replicas)
		if err != nil {
			return proto.Result{Cause: err.Error()}
		}
		return ok(proto.StartData{Container: id})

	case proto.CmdHealthCheck:
		var a proto.HealthArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.eachCopy(ctx, a.Container, func(id string) proto.Result {
			b := a
			b.Container = id
			return CheckHealth(ctx, d.rt, b, d.Get, d.Clock)
		})

	case proto.CmdSwitchRoute:
		var a proto.SwitchArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		ups, err := d.upstreams(ctx, a.Container, a.Port)
		if err != nil {
			return proto.Result{Cause: err.Error()}
		}
		for _, h := range a.Hosts {
			if err := d.px.SetRoute(ctx, h, ups, proxy.Options(a.Options)); err != nil {
				return proto.Result{Cause: err.Error()}
			}
		}
		return proto.Result{OK: true}

	case proto.CmdProbe:
		var a proto.ProbeArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.probe(ctx, a)

	case proto.CmdLogs:
		var a proto.LogsArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.logs(ctx, a)

	case proto.CmdEnsureDomain:
		var a proto.EnsureDomainArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.ensureDomain(ctx, a)

	case proto.CmdStatusPage:
		var a proto.StatusPageArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.statusPage(a)

	case proto.CmdRemoveRoute:
		var a proto.RemoveRouteArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		if err := d.px.RemoveRoute(ctx, a.Host); err != nil {
			return proto.Result{Cause: err.Error()}
		}
		return proto.Result{OK: true}

	case proto.CmdDrain:
		var a proto.DrainArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		if a.Container == "" {
			return proto.Result{OK: true}
		}
		select {
		case <-ctx.Done():
			return proto.Result{Cause: ctx.Err().Error()}
		case <-d.Clock.After(a.Timeout):
		}
		return d.eachCopy(ctx, a.Container, func(id string) proto.Result {
			if err := d.rt.Stop(ctx, id, stopGrace); errors.Is(err, runtime.ErrNotFound) {
				return proto.Result{OK: true}
			} else if err != nil {
				return proto.Result{Cause: err.Error()}
			}
			d.rt.Remove(ctx, id)
			return proto.Result{OK: true}
		})

	case proto.CmdStop:
		var a proto.StopArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		if a.Container == "" {
			return proto.Result{OK: true}
		}
		return d.eachCopy(ctx, a.Container, func(id string) proto.Result {
			if err := d.rt.Stop(ctx, id, stopGrace); err != nil && !errors.Is(err, runtime.ErrNotFound) {
				return proto.Result{Cause: err.Error()}
			}
			return proto.Result{OK: true}
		})

	case proto.CmdPrune:
		var a proto.PruneArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		if err := d.rt.PruneImages(ctx, a.Repo, a.Keep); err != nil {
			return proto.Result{Cause: err.Error()}
		}
		return proto.Result{OK: true}
	}
	if c.Name == proto.CmdReveal {
		var a proto.RevealArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		to, err := seal.ParsePublicKey(a.To)
		if err != nil || d.Keys == nil {
			return proto.Result{Cause: "cannot reveal: bad key or no secrets key on this agent"}
		}
		box, err := d.Keys.Reseal(a.Box, to)
		if err != nil {
			return proto.Result{Cause: "cannot reveal: " + err.Error()}
		}
		return ok(proto.RevealData{Box: box})
	}
	if c.Name == proto.CmdBackupVolume {
		var a proto.BackupVolumeArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.backupVolume(ctx, a)
	}
	if c.Name == proto.CmdRestoreVolume {
		var a proto.RestoreVolumeArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.restoreVolume(ctx, a)
	}
	if c.Name == proto.CmdRemoveVolume {
		var a proto.RemoveVolumeArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		if !strings.HasPrefix(a.Name, "yawl-") && !strings.HasPrefix(a.Name, "yawl_") {
			return proto.Result{Cause: "not a Yawl volume: " + a.Name}
		}
		if err := d.rt.RemoveVolume(ctx, a.Name); err != nil {
			return proto.Result{Cause: err.Error()}
		}
		return proto.Result{OK: true}
	}
	if c.Name == proto.CmdShipWAL {
		var a proto.ShipWALArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.shipWAL(ctx, a)
	}
	if strings.HasPrefix(c.Name, "Firewall") {
		return d.firewall(c)
	}
	if c.Name == proto.CmdNetwork {
		var a proto.NetworkArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		if d.Net == nil {
			return proto.Result{Cause: "this agent has no WireGuard key; rejoin it to use the private network"}
		}
		data, err := d.Net.Apply(a)
		if err != nil {
			return proto.Result{Cause: err.Error()}
		}
		return ok(data)
	}
	if c.Name == proto.CmdMetrics {
		var a proto.MetricsArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		d.setLogWatch(a.LogWatch)
		if d.Ring == nil {
			return ok(proto.MetricsData{})
		}
		return ok(proto.MetricsData{Samples: d.Ring.Since(a.Since)})
	}
	switch c.Name {
	case proto.CmdCronSchedule, proto.CmdCronRemove, proto.CmdCronRuns, proto.CmdCronRunNow:
		return d.cronCommand(ctx, c)
	}
	if c.Name == proto.CmdWatch {
		var a proto.WatchArgs
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return bad(err)
		}
		return d.watch(a)
	}
	return proto.Result{Cause: "unknown command " + c.Name}
}

func (d *Dispatcher) env(vars []proto.SealedVar) ([]string, string) {
	vals, order := map[string]string{}, []string{}
	set := func(k, v string) {
		if _, seen := vals[k]; !seen {
			order = append(order, k)
		}
		vals[k] = v
	}
	for _, v := range vars {
		val, cause := d.open(v)
		if cause != "" {
			return nil, cause
		}
		if v.Source != nil {
			got, err := d.fetchSecrets(*v.Source, string(val))
			if err != nil {
				return nil, v.Key + ": " + err.Error()
			}
			for _, k := range slices.Sorted(maps.Keys(got)) {
				set(k, got[k])
			}
			continue
		}
		set(v.Key, string(val))
	}
	env := make([]string, 0, len(order))
	for _, k := range order {
		env = append(env, k+"="+vals[k])
	}
	return env, ""
}

func (d *Dispatcher) open(v proto.SealedVar) ([]byte, string) {
	if d.Keys == nil {
		return nil, "Cannot open " + v.Key + ": this agent has no secrets key; re-join it"
	}
	val, err := d.Keys.Open(v.Box)
	if err != nil {
		return nil, "Cannot open " + v.Key + ": it was sealed for another server key; set it again"
	}
	return val, ""
}

func ok(data any) proto.Result {
	b, _ := json.Marshal(data)
	return proto.Result{OK: true, Data: b}
}

func bad(err error) proto.Result { return proto.Result{Cause: "bad arguments: " + err.Error()} }

func yawlNetworks(ns []string) string {
	for _, n := range ns {
		if !strings.HasPrefix(n, "yawl-") || strings.ContainsAny(n, "/ ") {
			return "not a Yawl network: " + n
		}
	}
	return ""
}

func (d *Dispatcher) firewall(c proto.Command) proto.Result {
	if d.FW == nil {
		return proto.Result{Cause: "this agent has no firewall"}
	}
	var err error
	switch c.Name {
	case proto.CmdFirewall:
		var a proto.FirewallArgs
		if err = json.Unmarshal(c.Args, &a); err == nil {
			err = d.FW.Apply(a)
		}
	case proto.CmdFirewallKeep:
		var a proto.FirewallKeepArgs
		if err = json.Unmarshal(c.Args, &a); err == nil {
			err = d.FW.Keep(a.Version)
		}
	case proto.CmdFirewallUnban:
		var a proto.FirewallUnbanArgs
		if err = json.Unmarshal(c.Args, &a); err == nil {
			err = d.FW.Unban(a.IP)
		}
	case proto.CmdFirewallStatus:
		return ok(d.FW.Status())
	default:
		return proto.Result{Cause: "unknown command " + c.Name}
	}
	if err != nil {
		return proto.Result{Cause: err.Error()}
	}
	return proto.Result{OK: true}
}
