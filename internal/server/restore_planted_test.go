package server

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agekey"
	"github.com/behzodhalil/yawl/internal/server/selfbackup"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestRestoreRefusesPlantedObject(t *testing.T) {
	recovery := vectorsKey(t)
	_, pub, err := agekey.FromRecovery(recovery)
	if err != nil {
		t.Fatal(err)
	}
	sign, _ := agekey.BackupSigningKey(recovery)
	mem := &selfbackup.Mem{}
	backup := func(who string) []byte {
		data, etc := t.TempDir(), t.TempDir()
		st, err := store.Open(filepath.Join(data, "yawl.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "made.by", Value: who})
		for _, n := range selfbackup.EtcFiles {
			os.WriteFile(filepath.Join(etc, n), []byte(n+" of "+who), 0o600)
		}
		var buf bytes.Buffer
		if err := selfbackup.Make(context.Background(), st.DB, data, etc, pub, &buf); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	put := func(key string, b []byte) string {
		mem.Put(context.Background(), key, bytes.NewReader(b), int64(len(b)))
		return key
	}
	panel := backup("panel")
	good := put(selfbackup.Name(sign, "2026-11-08T040000Z", panel), panel)
	planted := backup("attacker")
	unsigned := put("yawl-server/zz-planted.tar.age", planted)
	forged := put("yawl-server/2099-01-01T000000Z."+strings.Repeat("0", 64)+".tar.age", planted)
	_, sig, _ := strings.Cut(strings.TrimPrefix(good, "yawl-server/2026-11-08T040000Z"), ".")
	renamed := put("yawl-server/2098-01-01T000000Z."+sig, panel)

	restore := func(o RestoreOpts, typed string) (string, string, string, error) {
		o.Data, o.Etc, o.Store = filepath.Join(t.TempDir(), "data"), filepath.Join(t.TempDir(), "etc"), mem
		var out bytes.Buffer
		err := Restore(context.Background(), o, strings.NewReader(recovery+"\n"+typed), &out)
		return o.Data, o.Etc, out.String(), err
	}
	madeBy := func(data string) string {
		st, err := store.Open(filepath.Join(data, "yawl.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		v, _ := st.Q.GetSetting(context.Background(), "made.by")
		return v
	}
	nothing := func(name, data, etc string) {
		for _, p := range []string{filepath.Join(data, "yawl.db"), filepath.Join(etc, "ca.key")} {
			if _, err := os.Stat(p); err == nil {
				t.Fatalf("%s: wrote %s", name, p)
			}
		}
	}

	data, etc, out, err := restore(RestoreOpts{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if v := madeBy(data); v != "panel" {
		t.Fatalf("restored made.by %q, want the panel's", v)
	}
	if b, _ := os.ReadFile(filepath.Join(etc, "ca.key")); string(b) != "ca.key of panel" {
		t.Fatalf("ca.key %q", b)
	}
	for _, want := range []string{"Skipped " + unsigned + ": it is not signed.", "Skipped " + forged + ": its signature does not match", "Skipped " + renamed + ": its signature does not match", "Restored the panel from " + good + "."} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}

	for _, c := range []struct {
		o          RestoreOpts
		typed, err string
	}{
		{RestoreOpts{From: unsigned}, "", "is not signed, so nothing shows this panel made it"},
		{RestoreOpts{From: forged}, "", "is not signed with this recovery key"},
		{RestoreOpts{From: renamed}, "", "is not signed with this recovery key"},
		{RestoreOpts{From: forged, Unverified: true}, forged + "\n", "is not signed with this recovery key"},
		{RestoreOpts{From: unsigned, Unverified: true}, "yes\n", "Not confirmed; nothing was restored."},
	} {
		data, etc, _, err := restore(c.o, c.typed)
		if err == nil || !strings.Contains(err.Error(), c.err) {
			t.Fatalf("%+v: %v", c.o, err)
		}
		nothing(c.o.From, data, etc)
	}

	data, _, out, err = restore(RestoreOpts{From: unsigned, Unverified: true}, unsigned+"\n")
	if err != nil || madeBy(data) != "attacker" || !strings.Contains(out, "anyone who can write to") || !strings.Contains(out, "Its SHA-256 is ") {
		t.Fatalf("%v %q", err, out)
	}
}
