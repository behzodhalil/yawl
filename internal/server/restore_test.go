package server

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agekey"
	"github.com/behzodhalil/yawl/internal/server/selfbackup"
	"github.com/behzodhalil/yawl/internal/server/store"
)

func vectorsKey(t *testing.T) string {
	b, _ := os.ReadFile("../seal/testdata/vectors.json")
	var v struct{ Recovery string }
	json.Unmarshal(b, &v)
	return v.Recovery
}

func backedUp(t *testing.T, mem *selfbackup.Mem, stamp string) string {
	t.Helper()
	data, etc := t.TempDir(), t.TempDir()
	st, err := store.Open(filepath.Join(data, "yawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, n := range selfbackup.EtcFiles {
		os.WriteFile(filepath.Join(etc, n), []byte(n+" of "+stamp), 0o600)
	}
	_, pub, _ := agekey.FromRecovery(vectorsKey(t))
	var buf bytes.Buffer
	if err := selfbackup.Make(context.Background(), st.DB, data, etc, pub, &buf); err != nil {
		t.Fatal(err)
	}
	sign, _ := agekey.BackupSigningKey(vectorsKey(t))
	key := selfbackup.Name(sign, stamp, buf.Bytes())
	mem.Put(context.Background(), key, &buf, int64(buf.Len()))
	return key
}

func TestRestoreRefusesExisting(t *testing.T) {
	data := t.TempDir()
	os.WriteFile(filepath.Join(data, "yawl.db"), []byte("live"), 0o600)
	err := Restore(context.Background(), RestoreOpts{Data: data, Etc: t.TempDir(), Store: &selfbackup.Mem{}}, strings.NewReader(vectorsKey(t)+"\n"), &bytes.Buffer{})
	want := filepath.Join(data, "yawl.db") + " already exists. Restore runs on a new machine; move it away first."
	if err == nil || err.Error() != want {
		t.Fatalf("%v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(data, "yawl.db")); string(b) != "live" {
		t.Fatal("the live database was touched")
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	mem := &selfbackup.Mem{}
	backedUp(t, mem, "2026-10-20T040000Z")
	newest := backedUp(t, mem, "2026-10-22T040000Z")
	backedUp(t, mem, "2026-10-21T040000Z")
	data, etc := filepath.Join(t.TempDir(), "data"), filepath.Join(t.TempDir(), "etc")
	var out bytes.Buffer
	if err := Restore(context.Background(), RestoreOpts{Data: data, Etc: etc, Store: mem}, strings.NewReader(vectorsKey(t)+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(etc, "ca.key")); string(b) != "ca.key of 2026-10-22T040000Z" {
		t.Fatalf("restored %q, want the newest", b)
	}
	st, err := store.Open(filepath.Join(data, "yawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if v, _ := st.Q.GetSetting(context.Background(), "adopt.pending"); v != "1" {
		t.Fatalf("adopt.pending %q", v)
	}
	if strings.TrimSpace(out.String()) != "Restored the panel from "+newest+". Start yawl-server: it opens in adopt mode." {
		t.Fatalf("%q", out.String())
	}
}

func TestRestoreFrom(t *testing.T) {
	mem := &selfbackup.Mem{}
	a := backedUp(t, mem, "a")
	backedUp(t, mem, "b")
	etc := filepath.Join(t.TempDir(), "etc")
	Restore(context.Background(), RestoreOpts{Data: t.TempDir(), Etc: etc, Store: mem, From: a}, strings.NewReader(vectorsKey(t)), &bytes.Buffer{})
	if b, _ := os.ReadFile(filepath.Join(etc, "ca.key")); string(b) != "ca.key of a" {
		t.Fatalf("%q", b)
	}
}

func TestRestoreWrongKey(t *testing.T) {
	mem := &selfbackup.Mem{}
	backedUp(t, mem, "a")
	data := filepath.Join(t.TempDir(), "data")
	err := Restore(context.Background(), RestoreOpts{Data: data, Etc: t.TempDir(), Store: mem},
		strings.NewReader("yawl-recovery-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n"), &bytes.Buffer{})
	if err == nil || !strings.HasPrefix(err.Error(), "No panel backup in  is signed with this recovery key.") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(data, "yawl.db")); err == nil {
		t.Fatal("a wrong key wrote yawl.db")
	}
}

func TestRestoreNothingThere(t *testing.T) {
	err := Restore(context.Background(), RestoreOpts{Data: t.TempDir(), Etc: t.TempDir(), Bucket: "acme", Store: &selfbackup.Mem{}}, strings.NewReader(vectorsKey(t)), &bytes.Buffer{})
	if err == nil || err.Error() != "No panel backup in acme under yawl-server/." {
		t.Fatalf("%v", err)
	}
}

func TestRestoreRefusesLeftovers(t *testing.T) {
	for _, c := range []struct{ dir, name string }{{"data", "yawl.db-wal"}, {"data", "yawl.db-shm"}, {"etc", "server.key"}, {"etc", "ca.key"}} {
		root := t.TempDir()
		data, etc := filepath.Join(root, "data"), filepath.Join(root, "etc")
		os.MkdirAll(data, 0o700)
		os.MkdirAll(etc, 0o700)
		left := filepath.Join(root, c.dir, c.name)
		os.WriteFile(left, []byte("old"), 0o600)
		err := Restore(context.Background(), RestoreOpts{Data: data, Etc: etc, Store: &selfbackup.Mem{}}, strings.NewReader(vectorsKey(t)), &bytes.Buffer{})
		want := left + " already exists. Restore runs on a new machine; move it away first."
		if err == nil || err.Error() != want {
			t.Fatalf("%s: %v", c.name, err)
		}
	}
}

func TestRestoreAdoptsBeforeRename(t *testing.T) {
	mem := &selfbackup.Mem{}
	backedUp(t, mem, "a")
	data := filepath.Join(t.TempDir(), "data")
	var seen string
	restorePrepared = func(dbPath string) {
		st, _ := store.Open(dbPath)
		seen, _ = st.Q.GetSetting(context.Background(), "adopt.pending")
		st.Close()
	}
	defer func() { restorePrepared = nil }()
	if err := Restore(context.Background(), RestoreOpts{Data: data, Etc: filepath.Join(t.TempDir(), "etc"), Store: mem}, strings.NewReader(vectorsKey(t)), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if seen != "1" {
		t.Fatalf("the staged database had adopt.pending %q", seen)
	}
}
