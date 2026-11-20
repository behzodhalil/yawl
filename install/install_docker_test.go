//go:build docker

package install

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"net"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
)

func run(t *testing.T, image string, env ...string) (int, string) {
	t.Helper()
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	return runScript(t, image, script, append([]string{"YAWL_INSTALL_DRY=1"}, env...)...)
}

func runScript(t *testing.T, image string, script []byte, env ...string) (int, string) {
	t.Helper()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var out bytes.Buffer
	code, stderr, err := rt.RunOnce(ctx, runtime.ContainerSpec{Image: image, Network: "bridge", Cmd: []string{"sh", "-s"}, Env: env},
		bytes.NewReader(script), &out)
	if err != nil {
		t.Fatal(err)
	}
	return code, out.String() + stderr
}

func TestInstallPreflight(t *testing.T) {
	for _, img := range []string{"ubuntu:24.04", "ubuntu:26.04", "debian:12"} {
		code, out := run(t, img)
		for _, want := range []string{"ok os", "ok arch", "ok ports 80 443", "ok memory", "ok disk"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s: no %q in\n%s", img, want, out)
			}
		}
		if code != 0 || !(strings.Contains(out, "install docker") || strings.Contains(out, "ok docker")) {
			t.Fatalf("%s: exit %d\n%s", img, code, out)
		}
	}
	if code, out := run(t, "ubuntu:24.04"); !strings.Contains(out, "ok os ubuntu 24.04") || code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	if code, out := run(t, "ubuntu:26.04"); !strings.Contains(out, "ok os ubuntu 26.04") || code != 0 {
		t.Fatalf("26.04: %d %s", code, out)
	}
	code, out := run(t, "alpine:3")
	if code != 1 || !strings.Contains(out, "Yawl supports Ubuntu 22.04, 24.04 and 26.04 and Debian 12; this is alpine 3") {
		t.Fatalf("alpine: %d\n%s", code, out)
	}
	code, out = run(t, "debian:12", "YAWL_BINARY=/x")
	if code != 1 || !strings.Contains(out, "YAWL_BINARY /x does not exist.") {
		t.Fatalf("binary: %d\n%s", code, out)
	}
}

func TestInstallVerifiesDownload(t *testing.T) {
	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "k")
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	script, _ := os.ReadFile("install.sh")
	stamped := strings.Replace(string(script), `RELEASE_KEY_PEM=""`, "RELEASE_KEY_PEM='"+pemKey+"'", 1)

	name := "yawl-linux-" + goruntime.GOARCH
	bin := []byte("a yawl binary")
	sum := sha256.Sum256(bin)
	line := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	sums := []byte("# yawl 9.9.9\n" + line)
	good := ed25519.Sign(priv, sums)
	older := []byte("# yawl 9.9.8\n" + line)
	olderSig := ed25519.Sign(priv, older)
	serve := func(sums, sig, body []byte) string {
		l, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v9.9.9/SHA256SUMS":
				w.Write(sums)
			case "/v9.9.9/SHA256SUMS.sig":
				w.Write(sig)
			case "/v9.9.9/" + name:
				w.Write(body)
			case "/latest":
				w.Write([]byte(`{"tag_name": "v9.9.9", "name": "Yawl 9.9.9"}`))
			default:
				http.NotFound(w, r)
			}
		})}
		go srv.Serve(l)
		t.Cleanup(func() { srv.Close() })
		rt, _ := runtime.NewDocker()
		_, gw, err := rt.NetworkInfo(context.Background(), "bridge")
		if err != nil {
			t.Fatal(err)
		}
		return "http://" + gw.String() + ":" + strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	}
	bad := append([]byte(nil), good...)
	bad[0] ^= 1
	for _, c := range []struct {
		name            string
		sums, sig, body []byte
		code            int
		want            string
		latest          bool
	}{
		{"good", sums, good, bin, 0, "ok verified v9.9.9", false},
		{"latest", sums, good, bin, 0, "ok verified v9.9.9", true},
		{"bad signature", sums, bad, bin, 1, "The release signature does not match this installer's key.", false},
		{"cut binary", sums, good, bin[:5], 1, name + " does not match SHA256SUMS.", false},
		{"wrong version", older, olderSig, bin, 1, "This release's SHA256SUMS names v9.9.8, not v9.9.9.", false},
	} {
		base := serve(c.sums, c.sig, c.body)
		env := []string{"YAWL_INSTALL_VERIFY_ONLY=1", "YAWL_VERSION=9.9.9", "YAWL_RELEASES=" + base}
		if c.latest {
			env = []string{"YAWL_INSTALL_VERIFY_ONLY=1", "YAWL_RELEASES=" + base + "/"}
		}
		code, out := runScript(t, "debian:12", []byte(stamped), env...)
		if code != c.code || !strings.Contains(out, c.want) {
			t.Fatalf("%s: exit %d\n%s", c.name, code, out)
		}
	}
}

func TestInstallRerun(t *testing.T) {
	code, out := run(t, "ubuntu:24.04", "YAWL_TEST_BUSY=80 443", "YAWL_TEST_OURS=1")
	if code != 0 || !strings.Contains(out, "ok ports 80 443 (Yawl's own Caddy)") {
		t.Fatalf("ours: exit %d\n%s", code, out)
	}
	code, out = run(t, "ubuntu:24.04", "YAWL_TEST_BUSY=80 443", "YAWL_TEST_OURS=0")
	if code != 1 || !strings.Contains(out, "Port(s) 80 443 are in use.") {
		t.Fatalf("someone else's: exit %d\n%s", code, out)
	}
	code, out = run(t, "ubuntu:24.04", "YAWL_TEST_BUSY=", "YAWL_TEST_OURS=0")
	if code != 0 || !strings.Contains(out, "ok ports 80 443\n") {
		t.Fatalf("nothing busy: exit %d\n%s", code, out)
	}
}

func TestInstallBadIP(t *testing.T) {
	code, out := run(t, "ubuntu:24.04", "YAWL_TEST_IPIFY=<html>")
	if code != 1 || !strings.Contains(out, "Could not learn this machine's public address from api.ipify.org (got: <html>); set YAWL_PUBLIC_IP.") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	code, out = run(t, "ubuntu:24.04", "YAWL_TEST_IPIFY=deadbeef")
	if code != 1 || !strings.Contains(out, "(got: deadbeef); set YAWL_PUBLIC_IP.") {
		t.Fatalf("a hex word: exit %d\n%s", code, out)
	}
	long := strings.Repeat("x", 200)
	code, out = run(t, "ubuntu:24.04", "YAWL_TEST_IPIFY="+long)
	if code != 1 || !strings.Contains(out, "(got: "+long[:60]+"); set YAWL_PUBLIC_IP.") {
		t.Fatalf("a long answer: exit %d\n%s", code, out)
	}
	code, out = run(t, "ubuntu:24.04", "YAWL_TEST_IPIFY=203.0.113.9")
	if code != 0 || !strings.Contains(out, "ok address 203.0.113.9") {
		t.Fatalf("good address: exit %d\n%s", code, out)
	}
}

func TestInstallOpensWireGuardPort(t *testing.T) {
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	fake := []byte("cat > /usr/local/bin/ufw <<'UFW'\n#!/bin/sh\n[ \"$1\" = status ] && echo 'Status: active'\nUFW\nchmod +x /usr/local/bin/ufw\n")
	code, out := runScript(t, "ubuntu:24.04", append(fake, script...), "YAWL_INSTALL_DRY=1")
	if code != 0 || !strings.Contains(out, "would open UDP 51820 in ufw for the private network") ||
		!strings.Contains(out, "would set net.ipv4.ip_nonlocal_bind=1 (a database binds its private address before the network is up at boot)") {
		t.Fatalf("with ufw: exit %d\n%s", code, out)
	}
	code, out = run(t, "ubuntu:24.04")
	if code != 0 || !strings.Contains(out, "note: the private network uses UDP 51820 between your servers; open it in your cloud's firewall") {
		t.Fatalf("without ufw: exit %d\n%s", code, out)
	}
}

func TestInstallJoin(t *testing.T) {
	code, out := run(t, "ubuntu:24.04", "YAWL_JOIN=https://panel.acme.uz tok123", "YAWL_TEST_IPIFY=203.0.113.9")
	if code != 0 || !strings.Contains(out, "would install the agent only and join https://panel.acme.uz") {
		t.Fatalf("join: exit %d\n%s", code, out)
	}
	code, out = run(t, "ubuntu:24.04", "YAWL_JOIN=nope")
	if code != 1 || !strings.Contains(out, "YAWL_JOIN is the panel's address and a join token, like 'https://panel.acme.uz TOKEN'.") {
		t.Fatalf("bad join: exit %d\n%s", code, out)
	}
}
