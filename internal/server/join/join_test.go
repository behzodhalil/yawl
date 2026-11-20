package join

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
)

func testStore(t *testing.T) *store.Store {
	s, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestTokenOnceAndHour(t *testing.T) {
	st, ctx, now := testStore(t), context.Background(), time.Now()
	tok, err := NewToken(ctx, st.Q, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := Redeem(ctx, st.Q, tok, now.Add(time.Minute)); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := Redeem(ctx, st.Q, tok, now.Add(2*time.Minute)); !errors.Is(err, ErrTokenUsed) {
		t.Fatalf("second use: want ErrTokenUsed, got %v", err)
	}
	tok2, _ := NewToken(ctx, st.Q, now)
	if err := Redeem(ctx, st.Q, tok2, now.Add(61*time.Minute)); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("late use: want ErrTokenExpired, got %v", err)
	}
	if err := Redeem(ctx, st.Q, "nonsense", now); !errors.Is(err, ErrTokenUnknown) {
		t.Fatalf("unknown: got %v", err)
	}
}

func TestSignValidity(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "x"}}, key)
	csr, _ := x509.ParseCertificateRequest(der)
	pemCert, err := ca.Sign(csr, "srv_abc")
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseCertPEM(pemCert)
	if err != nil {
		t.Fatal(err)
	}
	if c.NotAfter.Sub(c.NotBefore) != 30*24*time.Hour || c.Subject.CommonName != "srv_abc" {
		t.Fatalf("validity %v, CN %q", c.NotAfter.Sub(c.NotBefore), c.Subject.CommonName)
	}
	if err := ca.Verify(c); err != nil {
		t.Fatalf("does not chain: %v", err)
	}
}

func TestCAReloads(t *testing.T) {
	dir := t.TempDir()
	a, _ := LoadOrCreateCA(dir)
	b, err := LoadOrCreateCA(dir)
	if err != nil || !a.Cert.Equal(b.Cert) {
		t.Fatalf("second load made a new CA (%v)", err)
	}
}

func TestJoinPanelToken(t *testing.T) {
	st, ctx, now := testStore(t), context.Background(), time.Now()
	p, _ := NewPanelToken(ctx, st.Q, now)
	n, _ := NewToken(ctx, st.Q, now)
	if !IsPanel(ctx, st.Q, p) || IsPanel(ctx, st.Q, n) || IsPanel(ctx, st.Q, "nonsense") {
		t.Fatal("panel flag")
	}
	if err := Redeem(ctx, st.Q, p, now); err != nil {
		t.Fatal(err)
	}
}

func TestServerName(t *testing.T) {
	for in, want := range map[string]string{
		"PhotonVPS": "photonvps", "hel1": "hel1", "srv1234567.vps.example": "srv1234567-vps-example",
		"My_Box--2": "my-box-2", "1and1": "s-1and1", "---": "", strings.Repeat("a", 40): strings.Repeat("a", 31),
	} {
		if got := ServerName(in); got != want {
			t.Errorf("ServerName(%q) = %q, want %q", in, got, want)
		}
	}
}
