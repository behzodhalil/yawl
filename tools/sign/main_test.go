package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeygenAndSums(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "release.key")
	var out bytes.Buffer
	if err := run([]string{"keygen", keyPath}, &out); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(keyPath)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", fi.Mode())
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out.String()))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		t.Fatalf("%q", out.String())
	}
	var again bytes.Buffer
	if err := run([]string{"pub", keyPath}, &again); err != nil || again.String() != out.String() {
		t.Fatalf("pub %q %v", again.String(), err)
	}
	if err := run([]string{"keygen", keyPath}, &bytes.Buffer{}); err == nil {
		t.Fatal("keygen overwrote an existing key")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "yawl-linux-amd64"), []byte("amd"), 0o755)
	os.WriteFile(filepath.Join(dir, "yawl-linux-arm64"), []byte("arm"), 0o755)
	if err := run([]string{"sums", "--version", "1.2.3", dir, keyPath}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	sums, _ := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	sig, _ := os.ReadFile(filepath.Join(dir, "SHA256SUMS.sig"))
	a := sha256.Sum256([]byte("amd"))
	if !strings.Contains(string(sums), hex.EncodeToString(a[:])+"  yawl-linux-amd64\n") || !ed25519.Verify(pub, sums, sig) {
		t.Fatalf("%s", sums)
	}
}

// Review I5: the installer verifies with openssl, which reads a PEM key; a
// release's install.sh carries that key, stamped in by `make release`.
func TestPemAndStamp(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "k")
	var pub bytes.Buffer
	run([]string{"keygen", keyPath}, &pub)
	var pemOut bytes.Buffer
	if err := run([]string{"pem", keyPath}, &pemOut); err != nil || !strings.HasPrefix(pemOut.String(), "-----BEGIN PUBLIC KEY-----\n") {
		t.Fatalf("%q %v", pemOut.String(), err)
	}
	script := filepath.Join(t.TempDir(), "install.sh")
	os.WriteFile(script, []byte("#!/bin/sh\nRELEASE_KEY_PEM=\"\"\necho hi\n"), 0o755)
	var out bytes.Buffer
	if err := run([]string{"stamp", script, keyPath}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "RELEASE_KEY_PEM='"+pemOut.String()+"'") || !strings.Contains(out.String(), "echo hi") {
		t.Fatalf("%s", out.String())
	}
	os.WriteFile(script, []byte("#!/bin/sh\necho no key line\n"), 0o755)
	if err := run([]string{"stamp", script, keyPath}, &bytes.Buffer{}); err == nil {
		t.Fatal("stamped a script without the key line")
	}
}

func TestSumsWritesVersion(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "k")
	if err := run([]string{"keygen", keyPath}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "yawl-linux-amd64"), []byte("amd"), 0o755)
	if err := run([]string{"sums", "--version", "1.2.3", dir, keyPath}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	sums, _ := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if !strings.HasPrefix(string(sums), "# yawl 1.2.3\n") {
		t.Fatalf("%s", sums)
	}
	if err := run([]string{"sums", dir, keyPath}, &bytes.Buffer{}); err == nil {
		t.Fatal("sums without --version made an unversioned list")
	}
}
