package exec

import (
	"context"
	"strings"

	"filippo.io/age"

	"github.com/behzodhalil/yawl/internal/agent/backup"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
)

func (d *Dispatcher) restore(ctx context.Context, a proto.RestoreArgs) proto.Result {
	if d.Keys != nil {
		d.Keys.Masker().Add(a.Storage.SecretKey)
	}
	if d.Keys == nil {
		return proto.Result{Cause: "this agent has no secrets key; re-join it"}
	}
	keyText, err := d.Keys.OpenOnceUnmasked(a.RecoveryKey)
	if err != nil {
		return proto.Result{Cause: "Cannot open the recovery key: it was sealed for another server key"}
	}
	key := strings.TrimSpace(string(keyText))
	var priv seal.PrivateKey
	hide := hider(key)
	_, priv, err = seal.DecodeRecovery(key)
	if err != nil {
		return hide("the recovery key does not decode: " + err.Error())
	}
	id, err := backup.Identity(priv)
	if err != nil {
		return hide(err.Error())
	}
	pw, cause := d.open(proto.SealedVar{Key: "PASSWORD", Box: a.Password})
	if cause != "" {
		return proto.Result{Cause: cause}
	}
	if a.PITR != nil && isPostgres(a.Kind) {
		return d.recoverAt(ctx, a, id, hide, string(pw))
	}
	container, volume, network, image := dbNames(a.Kind, a.Project, a.Env, a.Name, a.Version)
	if e, found := engines[a.Kind]; found && e.load != nil {
		if err := backup.RestoreSpec(ctx, d.rt, clientSpec(e, image, network, a.Name, string(pw), e.load(a.Name)), id, target(a.Storage), a.Key, a.SHA256); err != nil {
			return hide(err.Error())
		}
		return ok(struct{}{})
	}
	if !isPostgres(a.Kind) {
		if a.Ensure == nil {
			return proto.Result{Cause: "a restore of " + a.Kind + " needs the database's settings; update the server"}
		}
		d.rt.Stop(ctx, container, stopGrace)
		d.rt.Remove(ctx, container)
		if err := backup.RestoreVolume(ctx, d.rt, volume, id, target(a.Storage), a.Key, a.SHA256); err != nil {
			return hide(err.Error())
		}
		if res := d.ensureDatabase(ctx, *a.Ensure); !res.OK {
			return hide(res.Cause)
		}
		return ok(struct{}{})
	}
	db := backup.DB{Image: image, Network: network, Host: a.Name, Password: string(pw)}
	if err := backup.Restore(ctx, d.rt, db, id, target(a.Storage), a.Key, a.SHA256); err != nil {
		return hide(err.Error())
	}
	return ok(struct{}{})
}

func (d *Dispatcher) recoveryIdentity(sealed string) (*age.X25519Identity, func(string) proto.Result, string) {
	if d.Keys == nil {
		return nil, nil, "this agent has no secrets key; re-join it"
	}
	keyText, err := d.Keys.OpenOnceUnmasked(sealed)
	if err != nil {
		return nil, nil, "Cannot open the recovery key: it was sealed for another server key"
	}
	key := strings.TrimSpace(string(keyText))
	hide := hider(key)
	_, priv, err := seal.DecodeRecovery(key)
	if err != nil {
		return nil, nil, hide("the recovery key does not decode: " + err.Error()).Cause
	}
	id, err := backup.Identity(priv)
	if err != nil {
		return nil, nil, hide(err.Error()).Cause
	}
	return id, hide, ""
}

func (d *Dispatcher) backupVolume(ctx context.Context, a proto.BackupVolumeArgs) proto.Result {
	if d.Keys != nil {
		d.Keys.Masker().Add(a.Storage.SecretKey)
	}
	rec, err := seal.ParsePublicKey(a.RecoveryPubkey)
	if err != nil {
		return proto.Result{Cause: "bad recovery key: " + err.Error()}
	}
	vol, err := proto.VolumeName(a.Project, a.Env, a.Service, a.Name)
	if err != nil {
		return proto.Result{Cause: err.Error()}
	}
	size, sum, err := backup.DumpVolume(ctx, d.rt, vol, rec, target(a.Storage), a.Key)
	if err != nil {
		return proto.Result{Cause: err.Error()}
	}
	return ok(proto.BackupData{Size: size, SHA256: sum})
}

func (d *Dispatcher) restoreVolume(ctx context.Context, a proto.RestoreVolumeArgs) proto.Result {
	if d.Keys != nil {
		d.Keys.Masker().Add(a.Storage.SecretKey)
	}
	id, hide, cause := d.recoveryIdentity(a.RecoveryKey)
	if cause != "" {
		return proto.Result{Cause: cause}
	}
	vol, err := proto.VolumeName(a.Project, a.Env, a.Service, a.Name)
	if err != nil {
		return proto.Result{Cause: err.Error()}
	}
	if err := backup.RestoreVolume(ctx, d.rt, vol, id, target(a.Storage), a.Key, a.SHA256); err != nil {
		return hide(err.Error())
	}
	return proto.Result{OK: true}
}

func hider(key string) func(string) proto.Result {
	return func(s string) proto.Result {
		return proto.Result{Cause: replaceAllSkippingEmptyOld(s, key, "••••")}
	}
}

func replaceAllSkippingEmptyOld(s, old, new string) string {
	if old == "" {
		return s
	}
	return strings.ReplaceAll(s, old, new)
}
