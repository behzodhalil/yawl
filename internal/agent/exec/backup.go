package exec

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/backup"
	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
)

func target(s proto.Storage) backup.Target {
	return backup.Target{Endpoint: s.Endpoint, Bucket: s.Bucket, Region: s.Region, AccessKey: s.AccessKey, SecretKey: s.SecretKey, Insecure: s.Insecure}
}

func (d *Dispatcher) backup(ctx context.Context, a proto.BackupArgs) proto.Result {
	if d.Keys != nil {
		d.Keys.Masker().Add(a.Storage.SecretKey)
	}
	pw, cause := d.open(proto.SealedVar{Key: "PASSWORD", Box: a.Password})
	if cause != "" {
		return proto.Result{Cause: cause}
	}
	rec, err := seal.ParsePublicKey(a.RecoveryPubkey)
	if err != nil {
		return proto.Result{Cause: "bad recovery key: " + err.Error()}
	}
	_, volume, network, image := dbNames(a.Kind, a.Project, a.Env, a.Name, a.Version)
	var size int64
	var sum string
	if a.Base && isPostgres(a.Kind) {
		// -X none: fetching the WAL fails once a busy database recycles it mid backup
		spec := runtime.ContainerSpec{Image: image, Network: network, Env: []string{"PGPASSWORD=" + string(pw)},
			Cmd: []string{"pg_basebackup", "-h", a.Name, "-U", "app", "-D", "-", "-Ft", "-X", "none"}}
		size, sum, err = backup.DumpSpec(ctx, d.rt, spec, rec, target(a.Storage), a.Key)
	} else if e, found := engines[a.Kind]; found && e.dump != nil {
		size, sum, err = backup.DumpSpec(ctx, d.rt, clientSpec(e, image, network, a.Name, string(pw), e.dump(a.Name)), rec, target(a.Storage), a.Key)
	} else if isPostgres(a.Kind) {
		db := backup.DB{Image: image, Network: network, Host: a.Name, Password: string(pw)}
		size, sum, err = backup.Dump(ctx, d.rt, db, rec, target(a.Storage), a.Key)
	} else {
		resume, cause := d.pauseRewrites(ctx, image, network, a.Name, string(pw))
		if cause != "" {
			return proto.Result{Cause: cause}
		}
		size, sum, err = backup.DumpVolume(ctx, d.rt, volume, rec, target(a.Storage), a.Key)
		resume()
	}
	if err != nil {
		return proto.Result{Cause: err.Error()}
	}
	return ok(proto.BackupData{Size: size, SHA256: sum})
}

func (d *Dispatcher) copyBackup(ctx context.Context, a proto.CopyBackupArgs) proto.Result {
	if d.Keys != nil {
		d.Keys.Masker().Add(a.From.SecretKey)
		d.Keys.Masker().Add(a.To.SecretKey)
	}
	n, err := backup.Copy(ctx, target(a.From), target(a.To), a.Key)
	if err != nil {
		return proto.Result{Cause: err.Error()}
	}
	return proto.Result{OK: true, Output: fmt.Sprintf("Copied %d bytes to %s.", n, a.To.Bucket)}
}

func (d *Dispatcher) deleteBackups(ctx context.Context, a proto.DeleteBackupsArgs) proto.Result {
	if d.Keys != nil {
		d.Keys.Masker().Add(a.Storage.SecretKey)
	}
	gone, err := backup.Delete(ctx, target(a.Storage), a.Keys)
	res := ok(proto.DeleteBackupsData{Deleted: gone})
	if err != nil {
		res.Output = err.Error()
	}
	return res
}

// pauseRewrites stops Valkey rewriting its append only files and waits for
// a rewrite under way to end, so a copy of the volume is whole: the manifest
// and the files it names. A tail cut mid command is dropped at load
// (aof-load-truncated). resume puts the setting back; it runs also when the
// backup fails. (A snapshot alone, dump.rdb, is not loaded when the append
// only file is on: the e2e test found it restoring empty.)
func (d *Dispatcher) pauseRewrites(ctx context.Context, image, network, host, pw string) (resume func(), cause string) {
	cli := func(args ...string) (string, string) {
		var out bytes.Buffer
		spec := runtime.ContainerSpec{Image: image, Network: network,
			Cmd: append([]string{"valkey-cli", "-h", host, "-a", pw, "--no-auth-warning"}, args...)}
		exit, stderr, err := d.rt.RunOnce(context.WithoutCancel(ctx), spec, nil, &out)
		if err != nil {
			return "", err.Error()
		}
		if exit != 0 {
			return "", "valkey-cli exited " + strconv.Itoa(exit) + ": " + stderr
		}
		return out.String(), ""
	}
	was := "100"
	if out, c := cli("CONFIG", "GET", "auto-aof-rewrite-percentage"); c == "" {
		if f := strings.Fields(out); len(f) == 2 {
			was = f[1]
		}
	}
	if _, c := cli("CONFIG", "SET", "auto-aof-rewrite-percentage", "0"); c != "" {
		return nil, c
	}
	resume = func() { cli("CONFIG", "SET", "auto-aof-rewrite-percentage", was) }
	deadline := time.Now().Add(30 * time.Minute)
	for {
		info, c := cli("INFO", "persistence")
		if c != "" {
			resume()
			return nil, c
		}
		if strings.Contains(info, "aof_rewrite_in_progress:0") && strings.Contains(info, "aof_rewrite_scheduled:0") {
			return resume, ""
		}
		if time.Now().After(deadline) {
			resume()
			return nil, host + ": a rewrite of its append only file did not end in 30 minutes"
		}
		select {
		case <-ctx.Done():
			resume()
			return nil, ctx.Err().Error()
		case <-time.After(time.Second):
		}
	}
}

func clientSpec(e engine, image, network, host, pw string, cmd []string) runtime.ContainerSpec {
	s := runtime.ContainerSpec{Image: image, Network: network, Cmd: cmd}
	if e.clientEnv != nil {
		s.Env = e.clientEnv(host, pw)
	}
	return s
}
