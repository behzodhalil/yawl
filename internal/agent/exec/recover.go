package exec

import (
	"context"
	"fmt"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/behzodhalil/yawl/internal/agent/backup"
	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

const (
	restoreDir = "/var/lib/postgresql/data/yawl_restore"
	restoreIn  = "/data/yawl_restore"
)

// notReached is what Postgres says when the WAL ends before the target time:
// no commit came after it, so the end of the WAL is the state then.
const notReached = "recovery ended before configured recovery target was reached"

func (d *Dispatcher) recoverAt(ctx context.Context, a proto.RestoreArgs, id *age.X25519Identity, hide func(string) proto.Result, pw string) proto.Result {
	if a.Ensure == nil {
		return proto.Result{Cause: "a restore to a time needs the database's settings; update the server"}
	}
	if _, err := time.Parse("2006-01-02 15:04:05-07", a.PITR.At); err != nil {
		return proto.Result{Cause: "bad restore time " + a.PITR.At}
	}
	if strings.ContainsAny(pw, `'\`) {
		return proto.Result{Cause: "the new password cannot be written into SQL"}
	}
	src, cause := d.open(proto.SealedVar{Key: "PASSWORD", Box: a.PITR.SourcePassword})
	if cause != "" {
		return proto.Result{Cause: cause}
	}
	container, volume, network, image := dbNames("", a.Project, a.Env, a.Name, a.Version)
	d.rt.Stop(ctx, container, stopGrace)
	d.rt.Remove(ctx, container)
	if err := backup.RestoreVolume(ctx, d.rt, volume, id, target(a.Storage), a.Key, a.SHA256); err != nil {
		return hide(err.Error())
	}
	if len(a.PITR.WAL) > 0 {
		unpack := walHelper(volume, false, "sh", "-c", "mkdir -p "+restoreIn+" && tar -C "+restoreIn+" -xf -")
		if err := backup.RestoreTarsMany(ctx, d.rt, unpack, id, target(a.Storage), a.PITR.WAL, a.PITR.WALSHA256); err != nil {
			return hide(err.Error())
		}
	}
	conf := fmt.Sprintf("restore_command = 'cp %s/%%f %%p'\nrecovery_target_time = '%s'\nrecovery_target_action = 'promote'\n", restoreDir, a.PITR.At)
	if c := d.appendConf(ctx, volume, "rm -rf "+walIn+" && ", conf, " && touch /data/recovery.signal && chown "+fmt.Sprint(pgUID)+" /data/recovery.signal"); c != "" {
		return hide(c)
	}
	if res := d.ensureDatabase(ctx, *a.Ensure); !res.OK && !strings.Contains(res.Cause, notReady) {
		return hide(res.Cause)
	}
	psql := func(args ...string) runtime.ContainerSpec {
		return runtime.ContainerSpec{Image: image, Network: network, Env: []string{"PGPASSWORD=" + string(src)},
			Cmd: append([]string{"psql", "-h", a.Name, "-U", "app", "-d", "app"}, args...)}
	}
	retried := false
	for {
		var out strings.Builder
		if exit, _, err := d.rt.RunOnce(ctx, psql("-tAc", "SELECT pg_is_in_recovery()"), nil, &out); err == nil && exit == 0 && strings.TrimSpace(out.String()) == "f" {
			break
		}
		if tail, _ := d.rt.Tail(ctx, container, 50); strings.Contains(tail, notReached) && !retried {
			retried = true
			d.rt.Stop(ctx, container, stopGrace)
			d.rt.Remove(ctx, container)
			if c := d.appendConf(ctx, volume, "", "recovery_target_time = ''\n", ""); c != "" {
				return hide(c)
			}
			if res := d.ensureDatabase(ctx, *a.Ensure); !res.OK && !strings.Contains(res.Cause, notReady) {
				return hide(res.Cause)
			}
		}
		select {
		case <-ctx.Done():
			return hide(a.Name + " did not finish replaying its WAL: " + ctx.Err().Error())
		case <-time.After(2 * time.Second):
		}
	}
	set := psql("-v", "ON_ERROR_STOP=1", "-f", "-")
	if exit, stderr, err := d.rt.RunOnce(ctx, set, strings.NewReader("ALTER ROLE app PASSWORD '"+pw+"';\n"), nil); err != nil || exit != 0 {
		return hide(fmt.Sprintf("setting %s's password: exit %d %s %v", a.Name, exit, stderr, err))
	}
	d.rt.RunOnce(ctx, walHelper(volume, false, "rm", "-rf", restoreIn), nil, nil)
	return ok(struct{}{})
}

func (d *Dispatcher) appendConf(ctx context.Context, volume, before, conf, after string) string {
	spec := walHelper(volume, false, "sh", "-c", before+"cat >> /data/postgresql.auto.conf"+after)
	if exit, stderr, err := d.rt.RunOnce(ctx, spec, strings.NewReader(conf), nil); err != nil || exit != 0 {
		return fmt.Sprintf("writing the recovery settings: exit %d %s %v", exit, stderr, err)
	}
	return ""
}
