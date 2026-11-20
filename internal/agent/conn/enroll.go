package conn

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/secrets"
	"github.com/behzodhalil/yawl/internal/agent/wg"
)

type JoinRequest struct {
	Token       string `json:"token"`
	CSRPEM      string `json:"csr_pem"`
	AgentPubkey string `json:"agent_pubkey"`
	WGPubkey    string `json:"wg_pubkey"`
	Name        string `json:"name"`
	PublicIP    string `json:"public_ip"`
}

type JoinResponse struct {
	ServerID string `json:"server_id"`
	CertPEM  string `json:"cert_pem"`
	CAPEM    string `json:"ca_pem"`
}

func Enroll(ctx context.Context, serverURL, token, name, publicIP, dir string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: name}}, key)
	if err != nil {
		return err
	}
	kr, err := secrets.LoadOrCreate(dir)
	if err != nil {
		return err
	}
	wgKey, err := wg.LoadOrCreateKey(dir)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(JoinRequest{
		Token: token, Name: name, PublicIP: publicIP, AgentPubkey: kr.Public.String(), WGPubkey: wgKey.PublicKey().String(),
		CSRPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/agent/join", bytes.NewReader(body))
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("join refused: %s", bytes.TrimSpace(msg))
	}
	var jr JoinResponse
	if err := json.NewDecoder(res.Body).Decode(&jr); err != nil {
		return err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	files := map[string][]byte{
		"agent.key":  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}),
		"agent.crt":  []byte(jr.CertPEM),
		"ca.crt":     []byte(jr.CAPEM),
		"server_id":  []byte(jr.ServerID),
		"server_url": []byte(serverURL),
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func NeedsRenewal(c *x509.Certificate, now time.Time) bool {
	return c.NotAfter.Sub(now) <= 10*24*time.Hour
}
