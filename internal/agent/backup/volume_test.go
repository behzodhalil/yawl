package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/seal"
)

func TestVolumeSpecs(t *testing.T) {
	d := VolumeDumpSpec("yawl_acme_production_api_uploads")
	if d.Image != "alpine:3" || d.Network != "" || len(d.Mounts) != 1 || d.Mounts[0] != (runtime.Mount{Source: "yawl_acme_production_api_uploads", Target: "/data", Volume: true, ReadOnly: true}) ||
		!slices.Equal(d.Cmd, []string{"tar", "-C", "/data", "-cf", "-", "."}) {
		t.Fatalf("dump %+v", d)
	}
	r := VolumeStageSpec("yawl_acme_production_api_uploads")
	if r.Mounts[0].ReadOnly || r.Network != "" || !slices.Equal(r.Cmd, []string{"sh", "-c", "rm -rf /data/.yawl-restore && mkdir /data/.yawl-restore && tar -C /data/.yawl-restore -xf -"}) {
		t.Fatalf("stage %+v", r)
	}
}

func sumOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func encrypted(t *testing.T, plain []byte) (*bytes.Buffer, *age.X25519Identity) {
	pub, priv, _ := seal.GenerateKey()
	r, _ := Recipient(pub)
	var enc bytes.Buffer
	w, _ := age.Encrypt(&enc, r)
	w.Write(plain)
	w.Close()
	id, _ := Identity(priv)
	return &enc, id
}

func TestRestoreVolumeTruncatedLeavesData(t *testing.T) {
	enc, id := encrypted(t, bytes.Repeat([]byte("x"), 200_000))
	cut := bytes.NewReader(enc.Bytes()[:enc.Len()/2])
	rt := &runtime.Fake{}
	if err := restoreVolumeStream(context.Background(), rt, "v", cut, id, sumOf(enc.Bytes()[:enc.Len()/2])); err == nil {
		t.Fatal("a truncated backup restored")
	}
	var cmds []string
	for _, r := range rt.Ran {
		cmds = append(cmds, r.Spec.Cmd[2])
	}
	if len(cmds) != 2 || !strings.Contains(cmds[0], "tar -C /data/.yawl-restore") || cmds[1] != "rm -rf /data/.yawl-restore" {
		t.Fatalf("%q", cmds)
	}
}

func TestRestoreVolumeWholeSwaps(t *testing.T) {
	enc, id := encrypted(t, []byte("a tar"))
	rt := &runtime.Fake{}
	if err := restoreVolumeStream(context.Background(), rt, "v", enc, id, sumOf(enc.Bytes())); err != nil {
		t.Fatal(err)
	}
	if len(rt.Ran) != 2 || string(rt.Ran[0].Stdin) != "a tar" || !strings.Contains(rt.Ran[1].Spec.Cmd[2], "find /data -mindepth 1 -maxdepth 1 ! -name .yawl-restore") {
		t.Fatalf("%+v", rt.Ran)
	}
}

func TestRestoreVolumeWrongKeyLeavesData(t *testing.T) {
	pub, _, _ := seal.GenerateKey()
	_, other, _ := seal.GenerateKey()
	r, _ := Recipient(pub)
	var enc bytes.Buffer
	w, _ := age.Encrypt(&enc, r)
	w.Write([]byte("a tar"))
	w.Close()
	wrong, _ := Identity(other)
	rt := &runtime.Fake{}
	if err := restoreVolumeStream(context.Background(), rt, "v", &enc, wrong, sumOf(enc.Bytes())); err == nil {
		t.Fatal("a wrong key restored")
	}
	if len(rt.Ran) != 0 {
		t.Fatalf("ran %+v", rt.Ran)
	}
}

func TestRestoreRefusesSwappedObject(t *testing.T) {
	ctx := context.Background()
	good, id := encrypted(t, []byte("the backup"))
	var planted bytes.Buffer
	w, _ := age.Encrypt(&planted, id.Recipient())
	w.Write([]byte("planted"))
	w.Close()
	load := runtime.ContainerSpec{Cmd: []string{"pg_restore"}}
	for _, sum := range []string{sumOf(good.Bytes()), ""} {
		rt := &runtime.Fake{}
		if err := restoreStream(ctx, rt, load, bytes.NewReader(planted.Bytes()), id, sum); err == nil || !strings.Contains(err.Error(), "nothing was restored") {
			t.Fatalf("database, sum %q: %v", sum, err)
		}
		if err := restoreVolumeStream(ctx, rt, "v", bytes.NewReader(planted.Bytes()), id, sum); err == nil || !strings.Contains(err.Error(), "nothing was restored") {
			t.Fatalf("volume, sum %q: %v", sum, err)
		}
		if len(rt.Ran) != 0 {
			t.Fatalf("sum %q ran %+v", sum, rt.Ran)
		}
	}
	rt := &runtime.Fake{}
	if err := restoreStream(ctx, rt, load, bytes.NewReader(good.Bytes()), id, sumOf(good.Bytes())); err != nil || string(rt.Ran[0].Stdin) != "the backup" {
		t.Fatalf("the recorded backup: %v %+v", err, rt.Ran)
	}
}
