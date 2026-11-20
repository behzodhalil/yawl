package exec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// pgUID is the postgres user of the official image; Postgres refuses a key
// that it does not own.
const pgUID = 999

const pgConfDir = "/etc/yawl-pg"

// HBA is the database's pg_hba.conf. First match wins: the allowlist over
// TLS, then a reject for the bridge gateway (published-port traffic proxied
// from the host itself), then the environment's own network.
func HBA(allow []netip.Prefix, gateway netip.Addr, subnet netip.Prefix, replication bool) string {
	var b strings.Builder
	b.WriteString("local all all trust\n")
	b.WriteString("host all all 127.0.0.1/32 trust\n")
	for _, p := range allow {
		fmt.Fprintf(&b, "hostssl app app %s scram-sha-256\n", p)
	}
	fmt.Fprintf(&b, "host all all %s/32 reject\n", gateway)
	fmt.Fprintf(&b, "host app all %s scram-sha-256\n", subnet)
	if replication {
		fmt.Fprintf(&b, "host replication app %s scram-sha-256\n", subnet)
	}
	return b.String()
}

func ensureTLS(dir string, ip netip.Addr) error {
	if _, err := os.Stat(filepath.Join(dir, "server.key")); err == nil {
		return nil
	}
	cert, key, err := newCert(ip)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "server.crt"), cert, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "server.key"), key, 0o600)
}

func newCert(ip netip.Addr) (cert, key []byte, err error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "yawl-postgres"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if ip.IsValid() {
		tmpl.IPAddresses = append(tmpl.IPAddresses, ip.AsSlice())
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		return nil, nil, err
	}
	kb, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), nil
}

func tlsPair(dir string, ip netip.Addr) (cert, key []byte, err error) {
	if dir == "" {
		return newCert(ip)
	}
	if err := ensureTLS(dir, ip); err != nil {
		return nil, nil, err
	}
	if cert, err = os.ReadFile(filepath.Join(dir, "server.crt")); err != nil {
		return nil, nil, err
	}
	key, err = os.ReadFile(filepath.Join(dir, "server.key"))
	return cert, key, err
}

func parseAllow(allow []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, a := range allow {
		p, err := netip.ParsePrefix(a)
		if err != nil {
			return nil, errors.New("allow entry " + strconv.Quote(a) + " is not a CIDR")
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

var meshPrefix = netip.MustParsePrefix("10.73.0.0/16")

func pgconfHash(port int, allow []string, privateIP string, privatePort int, pitr bool) string {
	key := strconv.Itoa(port) + "|" + strings.Join(allow, ",") + "|hba2"
	if privatePort != 0 {
		key += "|" + privateIP + ":" + strconv.Itoa(privatePort)
	}
	if pitr {
		key += "|pitr"
	}
	s := sha256.Sum256([]byte(key))
	return hex.EncodeToString(s[:8])
}
