package join

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const certTTL = 30 * 24 * time.Hour

type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	PEM  []byte
}

func LoadOrCreateCA(dir string) (*CA, error) {
	certPath, keyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	certPEM, err := os.ReadFile(certPath)
	if errors.Is(err, os.ErrNotExist) {
		return createCA(certPath, keyPath)
	}
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	cert, err := ParseCertPEM(certPEM)
	if err != nil {
		return nil, err
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, errors.New("ca.key: not PEM")
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, Key: key, PEM: certPEM}, nil
}

func createCA(certPath, keyPath string) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: "yawl agent CA"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(10, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return nil, err
	}
	cert, _ := x509.ParseCertificate(der)
	return &CA{Cert: cert, Key: key, PEM: certPEM}, nil
}

func (ca *CA) Sign(csr *x509.CertificateRequest, serverID string) ([]byte, error) {
	if err := csr.CheckSignature(); err != nil {
		return nil, err
	}
	now := time.Now().Truncate(time.Second)
	tpl := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: serverID},
		NotBefore: now, NotAfter: now.Add(certTTL),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, csr.PublicKey, ca.Key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func (ca *CA) Verify(c *x509.Certificate) error {
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	_, err := c.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	return err
}

func ParseCertPEM(b []byte) (*x509.Certificate, error) {
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "CERTIFICATE" {
		return nil, errors.New("not a PEM certificate")
	}
	return x509.ParseCertificate(blk.Bytes)
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}
