package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/workers"
	"github.com/behzodhalil/yawl/internal/update"
)

func updateServer(t *testing.T, version string, good bool) (*httptest.Server, ed25519.PublicKey) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	bin := []byte("server " + version)
	sum := sha256.Sum256(bin)
	name := "yawl-" + goruntime.GOOS + "-" + goruntime.GOARCH
	sums := []byte(fmt.Sprintf("# yawl %s\n%s  %s\n", version, hex.EncodeToString(sum[:]), name))
	sig := ed25519.Sign(priv, sums)
	if !good {
		sig[0] ^= 1
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v" + version + "/SHA256SUMS":
			w.Write(sums)
		case "/v" + version + "/SHA256SUMS.sig":
			w.Write(sig)
		case "/v" + version + "/" + name:
			w.Write(bin)
		case "/latest":
			w.Write([]byte(`{"tag_name":"v` + version + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, pub
}

func updateAPI(t *testing.T, version string, rel *httptest.Server, key ed25519.PublicKey) (*httptest.Server, string, string, *int) {
	_, _, a := setupApp(t)
	exe := filepath.Join(t.TempDir(), "yawl")
	os.WriteFile(exe, []byte("server "+version), 0o755)
	restarts := 0
	srv := httptest.NewServer(New(Deps{App: a, Version: version, Exe: exe, Restart: func() { restarts++ },
		Updater: func() (update.Updater, error) {
			return update.Updater{Base: rel.URL, Latest: rel.URL + "/latest", Key: key}, nil
		}}))
	t.Cleanup(srv.Close)
	tok, _ := a.CreateToken(context.Background(), "x")
	return srv, tok, exe, &restarts
}

func TestUpdateRefusesDev(t *testing.T) {
	rel, key := updateServer(t, "1.2.0", true)
	srv, tok, _, _ := updateAPI(t, "dev", rel, key)
	if res, b := call(t, srv, tok, "POST", "/api/update", `{"version":"1.2.0"}`); res.StatusCode != 409 ||
		strings.TrimSpace(string(b)) != "This is a development build; it does not update itself." {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestUpdateSwapsAndExits(t *testing.T) {
	rel, key := updateServer(t, "1.2.0", true)
	srv, tok, exe, restarts := updateAPI(t, "1.1.0", rel, key)
	if _, b := call(t, srv, tok, "GET", "/api/update", ""); !strings.Contains(string(b), `"current":"1.1.0"`) || !strings.Contains(string(b), `"latest":"1.2.0"`) {
		t.Fatalf("%s", b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/update", `{"version":"1.2.0"}`); res.StatusCode != 202 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if b, _ := os.ReadFile(exe); string(b) != "server 1.2.0" || *restarts != 1 {
		t.Fatalf("%q restarts %d", b, *restarts)
	}

	bad, key2 := updateServer(t, "1.3.0", false)
	srv, tok, exe, restarts = updateAPI(t, "1.1.0", bad, key2)
	if res, b := call(t, srv, tok, "POST", "/api/update", `{"version":"1.3.0"}`); res.StatusCode != 502 ||
		!strings.Contains(string(b), "The release signature does not match this build's key.") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if b, _ := os.ReadFile(exe); string(b) != "server 1.1.0" || *restarts != 0 {
		t.Fatalf("%q restarts %d", b, *restarts)
	}
}

func TestUpdateBacksUpFirst(t *testing.T) {
	rel, key := updateServer(t, "1.2.0", true)
	for _, c := range []struct {
		backup error
		body   string
		code   int
		swaps  bool
		says   string
	}{
		{nil, `{"version":"1.2.0"}`, 202, true, `"backup":"taken"`},
		{errors.New("upload to acme-backups: 403"), `{"version":"1.2.0"}`, 409, false, "The backup before the update failed: upload to acme-backups: 403"},
		{errors.New("upload to acme-backups: 403"), `{"version":"1.2.0","skip_backup":true}`, 202, true, `"backup":"skipped"`},
		{workers.ErrNoStorage, `{"version":"1.2.0"}`, 202, true, `"backup":"none: backup storage is not set`},
	} {
		_, _, a := setupApp(t)
		exe := filepath.Join(t.TempDir(), "yawl")
		os.WriteFile(exe, []byte("server 1.1.0"), 0o755)
		ran := 0
		srv := httptest.NewServer(New(Deps{App: a, Version: "1.1.0", Exe: exe, Restart: func() {},
			RunSelfBackup: func(context.Context) error { ran++; return c.backup },
			Updater: func() (update.Updater, error) {
				return update.Updater{Base: rel.URL, Latest: rel.URL + "/latest", Key: key}, nil
			}}))
		tok, _ := a.CreateToken(context.Background(), "x")
		res, b := call(t, srv, tok, "POST", "/api/update", c.body)
		got, _ := os.ReadFile(exe)
		if res.StatusCode != c.code || (string(got) == "server 1.2.0") != c.swaps || !strings.Contains(string(b), c.says) {
			t.Errorf("%s with %v: %d %s, binary %q", c.body, c.backup, res.StatusCode, b, got)
		}
		if strings.Contains(c.body, "skip_backup") && ran != 0 {
			t.Errorf("skip_backup still backed up")
		}
		srv.Close()
	}
}

func TestUpdateLatestNeverGoesBack(t *testing.T) {
	rel, key := updateServer(t, "1.0.0", true)
	srv, tok, exe, restarts := updateAPI(t, "1.2.0", rel, key)
	if res, b := call(t, srv, tok, "POST", "/api/update", ""); res.StatusCode != 409 ||
		strings.TrimSpace(string(b)) != "The latest release (v1.0.0) is older than this panel (v1.2.0); name the version to go back." {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if b, _ := os.ReadFile(exe); string(b) != "server 1.2.0" || *restarts != 0 {
		t.Fatalf("%q restarts %d", b, *restarts)
	}
	if res, b := call(t, srv, tok, "POST", "/api/update", `{"version":"1.0.0"}`); res.StatusCode != 202 {
		t.Fatalf("named: %d %s", res.StatusCode, b)
	}
	for _, c := range [][2]string{{"0.9.9", "0.10.0"}, {"1.2", "1.2.1"}, {"0.0.1-accept", "0.0.2-accept"}} {
		if !older(c[0], c[1]) || older(c[1], c[0]) {
			t.Errorf("older(%s, %s)", c[0], c[1])
		}
	}
}
