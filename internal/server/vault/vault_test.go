package vault

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/store"
)

func TestVaultRoundTrip(t *testing.T) {
	v, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, b := v.Seal([]byte("pem body")), v.Seal([]byte("pem body"))
	if a == b {
		t.Fatal("two seals of one value are equal")
	}
	got, err := v.Open(a)
	if err != nil || string(got) != "pem body" {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestVaultKeyFile(t *testing.T) {
	dir := t.TempDir()
	v1, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := v1.Seal([]byte("x"))
	v2, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := v2.Open(s); err != nil || string(got) != "x" {
		t.Fatalf("reloaded vault cannot open: %q %v", got, err)
	}
	fi, err := os.Stat(filepath.Join(dir, "server.key"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("server.key mode = %v, %v", fi.Mode().Perm(), err)
	}
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, "server.key"), make([]byte, 31), 0o600)
	if _, err := LoadOrCreate(bad); err == nil {
		t.Fatal("a 31-byte key file was accepted")
	}
}

func TestVaultRejectsTampered(t *testing.T) {
	v, _ := LoadOrCreate(t.TempDir())
	s := []byte(v.Seal([]byte("value")))
	s[len(s)-3] ^= 1
	if _, err := v.Open(string(s)); err == nil {
		t.Fatal("tampered ciphertext opened")
	}
	for _, bad := range []string{"", "plain", "v1:!!!"} {
		if _, err := v.Open(bad); err == nil {
			t.Fatalf("Open(%q) succeeded", bad)
		}
	}
}

func TestSecretSetting(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	v, _ := LoadOrCreate(t.TempDir())
	if _, err := GetSecret(ctx, st.Q, v, "github.app"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unset secret: %v", err)
	}
	if err := SetSecret(ctx, st.Q, v, "github.app", []byte("hello-secret")); err != nil {
		t.Fatal(err)
	}
	raw, _ := st.Q.GetSetting(ctx, "github.app")
	if !bytes.HasPrefix([]byte(raw), []byte("v1:")) || bytes.Contains([]byte(raw), []byte("hello-secret")) {
		t.Fatalf("stored %q", raw)
	}
	got, err := GetSecret(ctx, st.Q, v, "github.app")
	if err != nil || string(got) != "hello-secret" {
		t.Fatalf("GetSecret = %q, %v", got, err)
	}
}

func TestLoadOrCreateRace(t *testing.T) {
	for range 20 {
		dir := t.TempDir()
		keys := make(chan [32]byte, 8)
		for range 8 {
			go func() {
				v, err := LoadOrCreate(dir)
				if err != nil {
					t.Error(err)
					keys <- [32]byte{}
					return
				}
				keys <- v.key
			}()
		}
		first := <-keys
		for range 7 {
			if <-keys != first {
				t.Fatal("two first runs made two keys")
			}
		}
	}
}
