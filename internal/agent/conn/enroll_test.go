package conn

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/behzodhalil/yawl/internal/seal"
)

func TestNeedsRenewal(t *testing.T) {
	now := time.Now()
	if NeedsRenewal(&x509.Certificate{NotAfter: now.Add(11 * 24 * time.Hour)}, now) {
		t.Fatal("11 days left should not renew")
	}
	if !NeedsRenewal(&x509.Certificate{NotAfter: now.Add(10 * 24 * time.Hour)}, now) {
		t.Fatal("10 days left should renew")
	}
}

func TestEnrollWritesFiles(t *testing.T) {
	var got JoinRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent/join" {
			http.NotFound(w, r)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(JoinResponse{ServerID: "srv_1", CertPEM: "CERT", CAPEM: "CA"})
	}))
	defer srv.Close()
	dir := t.TempDir()
	if err := Enroll(context.Background(), srv.URL, "tok", "hel1", "1.2.3.4", dir); err != nil {
		t.Fatal(err)
	}
	if got.Token != "tok" || got.Name != "hel1" || got.PublicIP != "1.2.3.4" {
		t.Fatalf("request: %+v", got)
	}
	if b, _ := pem.Decode([]byte(got.CSRPEM)); b == nil || b.Type != "CERTIFICATE REQUEST" {
		t.Fatal("no CSR sent")
	}
	if _, err := seal.ParsePublicKey(got.AgentPubkey); err != nil {
		t.Fatalf("agent_pubkey %q: %v", got.AgentPubkey, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "secrets.key")); len(b) != 32 {
		t.Fatalf("secrets.key is %d bytes", len(b))
	}
	for _, f := range []string{"agent.key", "agent.crt", "ca.crt", "server_id", "server_url", "secrets.key"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
	if fi, _ := os.Stat(filepath.Join(dir, "agent.key")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("agent.key mode %v", fi.Mode().Perm())
	}
}

func TestEnrollMakesWGKey(t *testing.T) {
	var got JoinRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(JoinResponse{ServerID: "srv_1", CertPEM: "CERT", CAPEM: "CA"})
	}))
	defer srv.Close()
	dir := t.TempDir()
	if err := Enroll(context.Background(), srv.URL, "tok", "hel1", "1.2.3.4", dir); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "wg.key"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("wg.key: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "wg.key"))
	priv, err := wgtypes.ParseKey(strings.TrimSpace(string(b)))
	if err != nil || got.WGPubkey != priv.PublicKey().String() || strings.Contains(got.WGPubkey, strings.TrimSpace(string(b))) {
		t.Fatalf("pubkey %q: %v", got.WGPubkey, err)
	}
}
