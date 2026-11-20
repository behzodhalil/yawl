package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func release(t *testing.T, version string, bin []byte, key ed25519.PrivateKey, mangle func(path string, b []byte) []byte) *httptest.Server {
	return releaseHead(t, version, SumsHeader(version)+"\n", bin, key, mangle)
}

func releaseHead(t *testing.T, version, head string, bin []byte, key ed25519.PrivateKey, mangle func(path string, b []byte) []byte) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(bin)
	sums := []byte(head + fmt.Sprintf("%s  yawl-linux-amd64\n%s  yawl-linux-arm64\n", hex.EncodeToString(sum[:]), strings.Repeat("0", 64)))
	files := map[string][]byte{
		"/v" + version + "/yawl-linux-amd64": bin,
		"/v" + version + "/SHA256SUMS":       sums,
		"/v" + version + "/SHA256SUMS.sig":   ed25519.Sign(key, sums),
		"/latest":                            []byte(`{"tag_name":"v` + version + `"}`),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if mangle != nil {
			if b = mangle(r.URL.Path, b); b == nil {
				http.NotFound(w, r)
				return
			}
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func keys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func noTemp(t *testing.T, dir string) {
	es, _ := os.ReadDir(dir)
	if len(es) != 0 {
		t.Fatalf("left %v in %s", es[0].Name(), dir)
	}
}

func TestFetchVerified(t *testing.T) {
	pub, priv := keys(t)
	srv := release(t, "1.2.0", []byte("new binary"), priv, nil)
	dir := t.TempDir()
	p, err := Updater{Base: srv.URL, Key: pub}.Fetch(context.Background(), "1.2.0", "linux", "amd64", dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	fi, _ := os.Stat(p)
	if string(b) != "new binary" || fi.Mode().Perm() != 0o755 || filepath.Dir(p) != dir {
		t.Fatalf("%q %v %s", b, fi.Mode(), p)
	}
}

func TestFetchRefusesBadSignature(t *testing.T) {
	_, priv := keys(t)
	other, _ := keys(t)
	srv := release(t, "1.2.0", []byte("new binary"), priv, nil)
	dir := t.TempDir()
	_, err := Updater{Base: srv.URL, Key: other}.Fetch(context.Background(), "1.2.0", "linux", "amd64", dir)
	if err == nil || err.Error() != "The release signature does not match this build's key." {
		t.Fatalf("%v", err)
	}
	noTemp(t, dir)
}

func TestFetchRefusesTruncated(t *testing.T) {
	pub, priv := keys(t)
	srv := release(t, "1.2.0", []byte("new binary"), priv, func(p string, b []byte) []byte {
		if strings.HasSuffix(p, "yawl-linux-amd64") {
			return b[:len(b)-1]
		}
		return b
	})
	dir := t.TempDir()
	_, err := Updater{Base: srv.URL, Key: pub}.Fetch(context.Background(), "1.2.0", "linux", "amd64", dir)
	if err == nil || err.Error() != "yawl-linux-amd64 does not match SHA256SUMS." {
		t.Fatalf("%v", err)
	}
	noTemp(t, dir)
}

func TestFetchMissingRelease(t *testing.T) {
	pub, priv := keys(t)
	srv := release(t, "1.2.0", []byte("x"), priv, nil)
	_, err := Updater{Base: srv.URL, Key: pub}.Fetch(context.Background(), "9.9.9", "linux", "amd64", t.TempDir())
	if err == nil || err.Error() != "Release v9.9.9 has no yawl-linux-amd64." {
		t.Fatalf("%v", err)
	}
}

func TestNoKey(t *testing.T) {
	old := ReleaseKey
	ReleaseKey = ""
	defer func() { ReleaseKey = old }()
	if _, err := Default("http://x"); err == nil || err.Error() != "This build has no release key, so it cannot update itself. Install a release build." {
		t.Fatalf("%v", err)
	}
}

func TestSwap(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "yawl")
	os.WriteFile(exe, []byte("old"), 0o755)
	f, _ := os.Open(exe) // the running process holds the old inode
	defer f.Close()
	nw := filepath.Join(dir, ".yawl-new-1")
	os.WriteFile(nw, []byte("new"), 0o755)
	if err := Swap(nw, exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("%q", b)
	}
	old := make([]byte, 3)
	f.Read(old)
	if string(old) != "old" {
		t.Fatalf("the old file was overwritten in place: %q", old)
	}
}

func TestLatestVersion(t *testing.T) {
	pub, priv := keys(t)
	srv := release(t, "1.3.0", []byte("x"), priv, nil)
	v, err := Updater{Latest: srv.URL + "/latest", Key: pub}.LatestVersion(context.Background())
	if err != nil || v != "1.3.0" {
		t.Fatalf("%q %v", v, err)
	}
}

func TestDefaultLatestFollowsBase(t *testing.T) {
	pub, priv := keys(t)
	old := ReleaseKey
	ReleaseKey = base64.StdEncoding.EncodeToString(pub)
	defer func() { ReleaseKey = old }()
	u, err := Default("")
	if err != nil || u.Base != "https://useyawl.com/releases" || u.Latest != "https://useyawl.com/releases/latest" {
		t.Fatalf("%+v %v", u, err)
	}
	srv := release(t, "1.3.0", []byte("x"), priv, nil)
	u, err = Default(srv.URL + "/")
	if err != nil || u.Latest != srv.URL+"/latest" {
		t.Fatalf("%+v %v", u, err)
	}
	if v, err := u.LatestVersion(context.Background()); err != nil || v != "1.3.0" {
		t.Fatalf("%q %v", v, err)
	}
}

func TestFetchMissingSig(t *testing.T) {
	pub, priv := keys(t)
	srv := release(t, "1.2.3", []byte("new binary"), priv, func(path string, b []byte) []byte {
		if strings.HasSuffix(path, ".sig") {
			return nil
		}
		return b
	})
	_, err := Updater{Base: srv.URL, Key: pub}.Fetch(context.Background(), "1.2.3", "linux", "amd64", t.TempDir())
	if err == nil || err.Error() != "Release v1.2.3 has no SHA256SUMS.sig." {
		t.Fatalf("%v", err)
	}
}

func TestFetchRefusesWrongVersion(t *testing.T) {
	pub, priv := keys(t)
	srv := releaseHead(t, "1.2.3", "# yawl 1.2.2\n", []byte("old binary"), priv, nil)
	dir := t.TempDir()
	_, err := Updater{Base: srv.URL, Key: pub}.Fetch(context.Background(), "1.2.3", "linux", "amd64", dir)
	if err == nil || err.Error() != "This release's SHA256SUMS names v1.2.2, not v1.2.3." {
		t.Fatalf("%v", err)
	}
	noTemp(t, dir)
}

func TestFetchRefusesUnversioned(t *testing.T) {
	pub, priv := keys(t)
	srv := releaseHead(t, "1.2.3", "", []byte("new binary"), priv, nil)
	_, err := Updater{Base: srv.URL, Key: pub}.Fetch(context.Background(), "1.2.3", "linux", "amd64", t.TempDir())
	if err == nil || err.Error() != "This release's SHA256SUMS does not name its version; it was made before version binding." {
		t.Fatalf("%v", err)
	}
}

func TestFetchSyncs(t *testing.T) {
	pub, priv := keys(t)
	srv := release(t, "1.2.3", []byte("new binary"), priv, nil)
	n := 0
	defer func(f func(*os.File) error) { syncFile = f }(syncFile)
	syncFile = func(f *os.File) error { n++; return f.Sync() }
	if _, err := (Updater{Base: srv.URL, Key: pub}).Fetch(context.Background(), "1.2.3", "linux", "amd64", t.TempDir()); err != nil || n != 1 {
		t.Fatalf("synced %d times, %v", n, err)
	}
}

func TestInstallScriptChecksSumsHeader(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "install", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	for _, want := range []string{
		`"` + SumsHeader("$version") + `")`,
		`"` + SumsVersionPrefix + `"*)`,
		`${named#` + strings.Replace(SumsVersionPrefix, "#", `\#`, 1) + `}`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("install.sh does not check the SHA256SUMS header with %q", want)
		}
	}
}
