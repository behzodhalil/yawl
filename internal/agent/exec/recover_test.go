package exec

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/behzodhalil/yawl/internal/agent/backup"
	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
)

func recoverWorld(t *testing.T, tail func(starts int) string) (*runtime.Fake, proto.Result) {
	return recoverWorldWith(t, nil, func(c string, starts int) (string, int) {
		if strings.Contains(c, "pg_is_in_recovery") && tail != nil && starts < 3 {
			return "t\n", 0
		}
		if strings.Contains(c, "pg_is_in_recovery") {
			return "f\n", 0
		}
		return "", 0
	}, tail)
}

func recoverWorldWith(t *testing.T, walSums func(sum string) []string, answer func(c string, starts int) (string, int), tail func(starts int) string) (*runtime.Fake, proto.Result) {
	pub, priv, _ := seal.GenerateKey()
	recovery := seal.EncodeRecovery(priv)
	r, _ := backup.Recipient(pub)
	var enc bytes.Buffer
	w, _ := age.Encrypt(&enc, r)
	w.Write(walBatch(t))
	w.Close()
	sum, sums := sumOf(enc.Bytes()), []string{sumOf(enc.Bytes()), sumOf(enc.Bytes())}
	if walSums != nil {
		sums = walSums(sum)
	}
	var mu sync.Mutex
	var rt *runtime.Fake
	rt = &runtime.Fake{OnRun: func(s runtime.ContainerSpec, _ []byte) (string, int, string, error) {
		mu.Lock()
		defer mu.Unlock()
		c := strings.Join(s.Cmd, " ")
		if tail != nil {
			rt.Tails = map[string]string{"yawl-acme-production-acme-db-2": tail(len(rt.Started))}
		}
		out, exit := answer(c, len(rt.Started))
		return out, exit, "", nil
	}}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	ens, _, _ := ensureArgs(t, sealTo("new-pw-123456"))
	ens.Name = "acme-db-2"
	if res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, ens)); !res.OK && !strings.Contains(res.Cause, "did not become ready") {
		t.Fatalf("ensure %+v", res)
	}
	res := d.Handle(context.Background(), cmd(t, proto.CmdRestore, proto.RestoreArgs{Ensure: &ens,
		Project: "acme", Env: "production", Name: "acme-db-2", Version: 17, Password: sealTo("new-pw-123456"), Key: "base", RecoveryKey: sealTo(recovery),
		SHA256: sum, PITR: &proto.PITRRestore{WAL: []string{"w1", "w2"}, WALSHA256: sums, At: "2026-10-21 04:30:00+00", SourcePassword: sealTo("src-pw-123456")},
		Storage: proto.Storage{Endpoint: s3Serving(t, enc.Bytes()), Bucket: "backups", Region: "us-east-1", AccessKey: "ak", SecretKey: "sk", Insecure: true}}))
	return rt, res
}

func TestRecoverToTime(t *testing.T) {
	rt, res := recoverWorld(t, nil)
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	if !slices.Contains(rt.Stopped, "yawl-acme-production-acme-db-2") {
		t.Fatalf("not stopped: %v", rt.Stopped)
	}
	var steps []string
	for _, run := range rt.Ran {
		c := strings.Join(run.Spec.Cmd, " ")
		in := string(run.Stdin)
		switch {
		case strings.Contains(c, ".yawl-restore -xf -") && in == string(walBatch(t)):
			steps = append(steps, "base")
		case strings.Contains(c, "tar -C /data/yawl_restore -xf -") && tarNames(t, run.Stdin) == "000000010000000000000001 000000010000000000000001":
			steps = append(steps, "wal")
		case strings.Contains(c, "recovery.signal"):
			for _, want := range []string{"restore_command = 'cp /var/lib/postgresql/data/yawl_restore/%f %p'\n",
				"recovery_target_time = '2026-10-21 04:30:00+00'\n", "recovery_target_action = 'promote'\n"} {
				if !strings.Contains(in, want) {
					t.Errorf("config lacks %q: %q", want, in)
				}
			}
			steps = append(steps, "config")
		case strings.Contains(c, "pg_is_in_recovery"):
			if !slices.Contains(run.Spec.Env, "PGPASSWORD=src-pw-123456") {
				t.Errorf("probe env %v", run.Spec.Env)
			}
			if steps[len(steps)-1] != "promoted?" {
				steps = append(steps, "promoted?")
			}
		case strings.HasPrefix(c, "psql") && strings.Contains(in, "ALTER ROLE"):
			if in != "ALTER ROLE app PASSWORD 'new-pw-123456';\n" || strings.Contains(c, "new-pw") || !slices.Contains(run.Spec.Env, "PGPASSWORD=src-pw-123456") {
				t.Errorf("password step %v %v %q", run.Spec.Cmd, run.Spec.Env, in)
			}
			steps = append(steps, "password")
		}
	}
	if want := []string{"base", "wal", "config", "promoted?", "password"}; !slices.Equal(steps, want) {
		t.Fatalf("steps %v, want %v", steps, want)
	}
	if len(rt.Started) != 2 || rt.Started[1].Name != "yawl-acme-production-acme-db-2" {
		t.Fatalf("started %+v", rt.Started)
	}
}

func TestRecoverRefusesUnverifiedWAL(t *testing.T) {
	for name, c := range map[string]struct {
		sums func(sum string) []string
		want string
	}{
		"planted":  {func(sum string) []string { return []string{strings.Repeat("0", 64), sum} }, "changed in storage"},
		"unhashed": {func(sum string) []string { return []string{"", sum} }, "no SHA-256 was recorded"},
		"old":      {func(string) []string { return []string{} }, "update the server"},
	} {
		rt, res := recoverWorldWith(t, c.sums, func(c string, _ int) (string, int) {
			if strings.Contains(c, "pg_is_in_recovery") {
				return "f\n", 0
			}
			return "", 0
		}, nil)
		if res.OK || !strings.Contains(res.Cause, c.want) {
			t.Fatalf("%s: %+v", name, res)
		}
		for _, run := range rt.Ran {
			if c := strings.Join(run.Spec.Cmd, " "); strings.Contains(c, "recovery.signal") ||
				strings.Contains(c, "tar -C /data/yawl_restore -xf -") && len(run.Stdin) != 0 {
				t.Fatalf("%s ran %q with %d bytes", name, c, len(run.Stdin))
			}
		}
		if len(rt.Started) != 1 {
			t.Fatalf("%s started %+v", name, rt.Started)
		}
	}
}

func TestRecoverTargetNotReached(t *testing.T) {
	rt, res := recoverWorld(t, func(starts int) string {
		if starts == 2 {
			return "FATAL:  recovery ended before configured recovery target was reached"
		}
		return ""
	})
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	cleared := false
	for _, run := range rt.Ran {
		if string(run.Stdin) == "recovery_target_time = ''\n" {
			cleared = true
		}
	}
	if !cleared || len(rt.Started) != 3 {
		t.Fatalf("cleared %v, starts %d", cleared, len(rt.Started))
	}
}

func TestRecoverSlowToOpen(t *testing.T) {
	defer func(old time.Duration) { dbReady = old }(dbReady)
	dbReady = 0
	rt, res := recoverWorldWith(t, nil, func(c string, starts int) (string, int) {
		switch {
		case strings.HasPrefix(c, "pg_isready"):
			return "", 1
		case strings.Contains(c, "pg_is_in_recovery") && starts < 3:
			return "", 2
		}
		return "f\n", 0
	}, func(starts int) string {
		if starts == 2 {
			return "FATAL:  " + notReached
		}
		return ""
	})
	if !res.OK || len(rt.Started) != 3 {
		t.Fatalf("%+v, starts %d", res, len(rt.Started))
	}
}

func walBatch(t *testing.T) []byte {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "000000010000000000000001", Mode: 0o600, Size: 3}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("wal"))
	tw.Close()
	gz.Close()
	return b.Bytes()
}

func tarNames(t *testing.T, b []byte) string {
	var names []string
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return strings.Join(names, " ")
		}
		if err != nil {
			return "bad tar: " + err.Error()
		}
		names = append(names, h.Name)
	}
}
