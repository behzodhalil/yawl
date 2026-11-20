package join

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func joinAs(t *testing.T, h http.Handler, token, name, ip, wg string) int {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csr, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: name}}, key)
	body, _ := json.Marshal(Request{Token: token, Name: name, PublicIP: ip, WGPubkey: wg,
		CSRPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}))})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/agent/join", bytes.NewReader(body)))
	return w.Code
}

func wgKey(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }

func TestJoinAssignsPrivateIP(t *testing.T) {
	st, ctx := testStore(t), context.Background()
	ca, _ := LoadOrCreateCA(t.TempDir())
	h := HandlerLimited(st, ca, 0, func(string) {})
	tok := func() string { s, _ := NewToken(ctx, st.Q, time.Now()); return s }
	for i, n := range []string{"hel1", "fsn1", "nbg1"} {
		if code := joinAs(t, h, tok(), n, "1.2.3."+string(rune('4'+i)), wgKey(byte(i+1))); code != 200 {
			t.Fatalf("%s: %d", n, code)
		}
	}
	ip := func(n string) (string, string) {
		var a, k string
		st.DB.QueryRow(`SELECT private_ip, wg_pubkey FROM servers WHERE name = ?`, n).Scan(&a, &k)
		return a, k
	}
	if a, k := ip("hel1"); a != "10.73.0.1" || k != wgKey(1) {
		t.Fatal(a, k)
	}
	if a, _ := ip("nbg1"); a != "10.73.0.3" {
		t.Fatal(a)
	}
	joinAs(t, h, tok(), "fsn1", "1.2.3.5", wgKey(9))
	if a, k := ip("fsn1"); a != "10.73.0.2" || k != wgKey(9) {
		t.Fatal(a, k)
	}
	st.DB.Exec(`UPDATE servers SET private_ip = '' WHERE name = 'fsn1'`)
	joinAs(t, h, tok(), "sin1", "1.2.3.9", wgKey(7))
	if a, _ := ip("sin1"); a != "10.73.0.2" {
		t.Fatal(a)
	}
	if code := joinAs(t, h, tok(), "bad1", "1.2.3.10", "not-a-key"); code != 400 {
		t.Fatalf("a bad key joined: %d", code)
	}
}

func TestRejoinDropsShares(t *testing.T) {
	st, ctx := testStore(t), context.Background()
	ca, _ := LoadOrCreateCA(t.TempDir())
	h := HandlerLimited(st, ca, 0, func(string) {})
	tok := func() string { s, _ := NewToken(ctx, st.Q, time.Now()); return s }
	joinAs(t, h, tok(), "hel1", "1.2.3.4", wgKey(1))
	var id string
	st.DB.QueryRow(`SELECT id FROM servers WHERE name = 'hel1'`).Scan(&id)
	st.DB.Exec(`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('prj_1','acme',1,1)`)
	st.DB.Exec(`INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES ('env_1','prj_1','production',?,1,1)`, id)
	st.DB.Exec(`INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, created_at, updated_at) VALUES ('svc_db','env_1','acme-db','postgres','','',0,'',0,0,1,1)`)
	if _, err := st.DB.Exec(`INSERT INTO db_shares (service_id, server_id, url_box, updated_at) VALUES ('svc_db', ?, 'old', 1)`, id); err != nil {
		t.Fatal(err)
	}
	joinAs(t, h, tok(), "hel1", "1.2.3.4", wgKey(2))
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM db_shares WHERE server_id = ?`, id).Scan(&n)
	if n != 0 {
		t.Fatal("shares sealed to the old key survived the rejoin")
	}
}

func TestJoinChecksAgentKey(t *testing.T) {
	st, ctx := testStore(t), context.Background()
	ca, _ := LoadOrCreateCA(t.TempDir())
	h := HandlerLimited(st, ca, 0, func(string) {})
	tok, _ := NewToken(ctx, st.Q, time.Now())
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csr, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "hel1"}}, key)
	body, _ := json.Marshal(Request{Token: tok, Name: "hel1", PublicIP: "1.2.3.4", AgentPubkey: "x",
		CSRPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}))})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/agent/join", bytes.NewReader(body)))
	if w.Code != 400 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
