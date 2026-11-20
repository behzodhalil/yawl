package selfbackup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agekey"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func recovery(t *testing.T) string {
	b, _ := os.ReadFile("../../seal/testdata/vectors.json")
	var v struct{ Recovery string }
	json.Unmarshal(b, &v)
	return v.Recovery
}

func fixture(t *testing.T) (data, etc string, st *store.Store) {
	data, etc = t.TempDir(), t.TempDir()
	st, err := store.Open(filepath.Join(data, "yawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.Q.InsertOwner(context.Background(), db.InsertOwnerParams{ID: "own_1", Email: "me@acme.uz", PasswordHash: "x", CreatedAt: 1, UpdatedAt: 1})
	for _, n := range []string{"ca.crt", "ca.key", "server.key"} {
		os.WriteFile(filepath.Join(etc, n), []byte("contents of "+n), 0o600)
	}
	return data, etc, st
}

func TestSelfBackupContents(t *testing.T) {
	data, etc, st := fixture(t)
	id, pub, _ := agekey.FromRecovery(recovery(t))
	var buf bytes.Buffer
	if err := Make(context.Background(), st.DB, data, etc, pub, &buf); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte("me@acme.uz")) || bytes.Contains(buf.Bytes(), []byte("contents of ca.key")) {
		t.Fatal("the backup is not encrypted")
	}
	d2, e2 := t.TempDir(), t.TempDir()
	if err := Unpack(&buf, id, d2, e2, nil); err != nil {
		t.Fatal(err)
	}
	st2, err := store.Open(filepath.Join(d2, "yawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if o, err := st2.Q.GetOwner(context.Background(), "own_1"); err != nil || o.Email != "me@acme.uz" {
		t.Fatalf("%v %v", o, err)
	}
	for _, n := range []string{"ca.crt", "ca.key", "server.key"} {
		b, _ := os.ReadFile(filepath.Join(e2, n))
		fi, _ := os.Stat(filepath.Join(e2, n))
		if string(b) != "contents of "+n || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %q %v", n, b, fi.Mode())
		}
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestSelfBackupCleansUp(t *testing.T) {
	data, etc, st := fixture(t)
	_, pub, _ := agekey.FromRecovery(recovery(t))
	Make(context.Background(), st.DB, data, etc, pub, &bytes.Buffer{})
	if err := Make(context.Background(), st.DB, data, etc, pub, failWriter{}); err == nil {
		t.Fatal("a failing writer succeeded")
	}
	es, _ := os.ReadDir(data)
	for _, e := range es {
		if strings.HasPrefix(e.Name(), "yawl-vacuum-") {
			t.Fatalf("left %s", e.Name())
		}
	}
}

func TestUnpackWrongKey(t *testing.T) {
	data, etc, st := fixture(t)
	_, pub, _ := agekey.FromRecovery(recovery(t))
	var buf bytes.Buffer
	Make(context.Background(), st.DB, data, etc, pub, &buf)
	other, _, _ := agekey.FromRecovery("yawl-recovery-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	d2 := t.TempDir()
	if err := Unpack(&buf, other, d2, t.TempDir(), nil); err == nil || err.Error() != "This backup was encrypted to another recovery key." {
		t.Fatalf("%v", err)
	}
	if es, _ := os.ReadDir(d2); len(es) != 0 {
		t.Fatal("a wrong key wrote files")
	}
}
