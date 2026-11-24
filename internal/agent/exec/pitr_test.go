package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
)

func TestEnsurePITRFlags(t *testing.T) {
	specOf := func(pitr bool) runtime.ContainerSpec {
		rt := &runtime.Fake{}
		kr, _ := keyring(t)
		d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
		d.Keys = kr
		a, _, _ := ensureArgs(t, "")
		a.PITR = pitr
		if res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, a)); !res.OK {
			t.Fatalf("%+v", res)
		}
		return rt.Started[0]
	}
	on, off := specOf(true), specOf(false)
	c := strings.Join(on.Cmd, " ")
	for _, want := range []string{"-c archive_mode=on", "-c archive_timeout=60", "-c archive_command=mkdir -p " + walDir + " && test ! -f " + walDir + "/%f && cp %p " + walDir + "/%f.tmp && mv " + walDir + "/%f.tmp " + walDir + "/%f"} {
		if !strings.Contains(c, want) {
			t.Errorf("lacks %q: %s", want, c)
		}
	}
	if strings.Contains(strings.Join(off.Cmd, " "), "archive") || on.Labels["yawl.pgconf"] == off.Labels["yawl.pgconf"] {
		t.Fatalf("off: %v; labels %s %s", off.Cmd, on.Labels["yawl.pgconf"], off.Labels["yawl.pgconf"])
	}
}

func TestWALList(t *testing.T) {
	now := time.Unix(1_800_000_100, 0)
	out := "000000010000000000000003 1800000000 16777216\n" +
		"000000010000000000000004 1800000080 16777216\n" +
		"000000010000000000000005 1800000095 16777216\n" +
		"00000002.history 1800000000 41\n" +
		"000000010000000000000002.00000028.backup 1800000000 300\n" +
		"foo.partial 1800000000 9\n"
	files, pending, ready := walList(out, now)
	want := []string{"000000010000000000000002.00000028.backup", "000000010000000000000003", "000000010000000000000004", "00000002.history"}
	if !slices.Equal(files, want) || pending != 3*16777216+41+300 || ready != 2*16777216+41+300 {
		t.Fatalf("%v %d %d", files, pending, ready)
	}
}

func s3Complete(t *testing.T, fail bool) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		q := r.URL.Query()
		switch {
		case r.Method == "POST" && q.Has("uploads"):
			io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>b</Bucket><Key>k</Key><UploadId>u1</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == "PUT":
			w.Header().Set("ETag", `"e1"`)
		case r.Method == "POST" && q.Has("uploadId"):
			io.WriteString(w, `<CompleteMultipartUploadResult><Bucket>b</Bucket><Key>k</Key><ETag>"e"</ETag></CompleteMultipartUploadResult>`)
		}
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestShipWAL(t *testing.T) {
	for _, fail := range []bool{false, true} {
		now := time.Now().Unix()
		var ran []string
		rt := &runtime.Fake{OnRun: func(s runtime.ContainerSpec, _ []byte) (string, int, string, error) {
			c := strings.Join(s.Cmd, " ")
			ran = append(ran, c)
			switch {
			case strings.Contains(c, "stat -c"):
				return fmt.Sprintf("000000010000000000000003 %d 16777216\n000000010000000000000004 %d 16777216\n", now-60, now-1), 0, "", nil
			case strings.HasPrefix(c, "tar"):
				return "a tar of the wal", 0, "", nil
			}
			return "", 0, "", nil
		}}
		kr, _ := keyring(t)
		d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
		d.Keys = kr
		pub, _, _ := seal.GenerateKey()
		res := d.Handle(context.Background(), cmd(t, proto.CmdShipWAL, proto.ShipWALArgs{Project: "acme", Env: "production", Name: "acme-db",
			KeyPrefix: "yawl/acme/production/acme-db/wal/", RecoveryPubkey: pub.String(), Remove: []string{"000000010000000000000002", "../etc/passwd"},
			Storage: proto.Storage{Endpoint: s3Complete(t, fail), Bucket: "backups", Region: "us-east-1", AccessKey: "a", SecretKey: "s", Insecure: true}}))
		var data proto.ShipWALData
		json.Unmarshal(res.Data, &data)
		if ran[0] != "rm -f -- /data/yawl_wal/000000010000000000000002" {
			t.Fatalf("first %q", ran[0])
		}
		for _, c := range ran[1:] {
			if strings.HasPrefix(c, "rm") {
				t.Fatalf("removed what the server has not recorded: %q", c)
			}
		}
		if fail {
			if res.OK || data.Pending != 2*16777216 {
				t.Fatalf("failed upload: %+v %+v", res, data)
			}
			continue
		}
		if !res.OK || data.Batch == nil || data.Batch.First != "000000010000000000000003" || data.Batch.Last != "000000010000000000000003" ||
			!slices.Equal(data.Batch.Files, []string{"000000010000000000000003"}) || data.Pending != 16777216 ||
			data.Batch.Key != "yawl/acme/production/acme-db/wal/000000010000000000000003.tar.gz.age" {
			t.Fatalf("%+v %+v", res, data)
		}
	}
}

func TestShipWALCapped(t *testing.T) {
	var out strings.Builder
	for i := range 100 {
		fmt.Fprintf(&out, "0000000100000000%08X 1 16777216\n", i)
	}
	files, _, _ := walList(out.String(), time.Now())
	if len(files) != proto.WALBatchMax || files[0] != "000000010000000000000000" {
		t.Fatalf("%d %v", len(files), files[:1])
	}
}

func TestEnsurePITRAllowsReplication(t *testing.T) {
	hba := func(pitr bool) string {
		rt := &runtime.Fake{}
		kr, _ := keyring(t)
		d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
		d.Keys = kr
		a, _, _ := ensureArgs(t, "")
		a.PITR = pitr
		if res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, a)); !res.OK {
			t.Fatalf("%+v", res)
		}
		for _, f := range rt.Started[0].Files {
			if strings.HasSuffix(f.Path, "pg_hba.conf") {
				return string(f.Data)
			}
		}
		t.Fatal("no pg_hba.conf")
		return ""
	}
	if on := hba(true); !strings.Contains(on, "host replication app ") || !strings.Contains(on, " scram-sha-256\n") {
		t.Fatalf("on:\n%s", on)
	}
	if off := hba(false); strings.Contains(off, "replication") {
		t.Fatalf("off:\n%s", off)
	}
}

func TestBackupBase(t *testing.T) {
	rt := &runtime.Fake{OnRun: func(s runtime.ContainerSpec, _ []byte) (string, int, string, error) { return "a tar", 0, "", nil }}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	rec, _, _ := seal.GenerateKey()
	res := d.Handle(context.Background(), cmd(t, proto.CmdBackup, proto.BackupArgs{Base: true,
		Project: "acme", Env: "production", Name: "acme-db", Version: 17, Password: sealTo("pw-123456"),
		Storage: proto.Storage{Endpoint: s3Complete(t, false), Bucket: "backups", Region: "us-east-1", AccessKey: "ak", SecretKey: "sk", Insecure: true},
		Key:     "yawl/acme/production/acme-db/base/x.tar.age", RecoveryPubkey: rec.String()}))
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	s := rt.Ran[0].Spec
	if s.Image != "pgvector/pgvector:pg17-trixie" || strings.Join(s.Cmd, " ") != "pg_basebackup -h acme-db -U app -D - -Ft -X none" || !slices.Contains(s.Env, "PGPASSWORD=pw-123456") {
		t.Fatalf("base container %+v", s)
	}
}

func TestEnsurePITROffClearsWAL(t *testing.T) {
	rt := &runtime.Fake{}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	a, _, _ := ensureArgs(t, sealTo("pw-123456"))
	for _, pitr := range []bool{true, true, false} {
		a.PITR = pitr
		if res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, a)); !res.OK {
			t.Fatalf("%+v", res)
		}
	}
	var cleared int
	for _, r := range rt.Ran {
		if strings.Join(r.Spec.Cmd, " ") == "rm -rf "+walIn {
			cleared++
		}
	}
	if cleared != 1 {
		t.Fatalf("cleared %d times", cleared)
	}
}
