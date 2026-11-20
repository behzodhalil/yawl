package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/behzodhalil/yawl/internal/agent/conn"
	"github.com/behzodhalil/yawl/internal/agent/exec"
	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/agent/secrets"
	"github.com/behzodhalil/yawl/internal/agent/state"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
	"github.com/behzodhalil/yawl/internal/server/gateway"
	"github.com/behzodhalil/yawl/internal/server/join"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type rig struct {
	st       *store.Store
	gw       *gateway.Gateway
	url      string
	serverID string
	agentDir string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	return newRigOn(t, nil)
}

func newRigOn(t *testing.T, wrap func(db.DBTX) db.DBTX) *rig {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "yawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ca, err := join.LoadOrCreateCA(filepath.Join(dir, "etc"))
	if err != nil {
		t.Fatal(err)
	}
	q := st.Q
	if wrap != nil {
		q = db.New(wrap(st.DB))
	}
	gw := gateway.New(q, ca, nil)
	mux := http.NewServeMux()
	mux.Handle("POST /agent/join", join.HandlerLimited(st, ca, 0, gw.Drop))
	mux.Handle("/agent", gw.Handler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tok, err := join.NewToken(context.Background(), st.Q, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(dir, "agent")
	if err := conn.Enroll(context.Background(), srv.URL, tok, "hel1", "1.2.3.4", agentDir); err != nil {
		t.Fatal(err)
	}
	id, _ := os.ReadFile(filepath.Join(agentDir, "server_id"))
	return &rig{st: st, gw: gw, url: srv.URL, serverID: string(id), agentDir: agentDir}
}

func (r *rig) dial(t *testing.T, h conn.Handler) {
	t.Helper()
	cfg, err := conn.LoadConfig(r.agentDir, "test")
	if err != nil {
		t.Fatal(err)
	}
	cfg.HeartbeatEvery = 50 * time.Millisecond
	st, err := state.Open(filepath.Join(r.agentDir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); st.Close() })
	go conn.Dial(ctx, cfg, st, h)
	deadline := time.Now().Add(5 * time.Second)
	for !r.gw.Connected(r.serverID) {
		if time.Now().After(deadline) {
			t.Fatal("agent never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func echo(_ context.Context, c proto.Command) proto.Result { return proto.Result{Key: c.Key, OK: true} }

func TestCommandRoundTrip(t *testing.T) {
	r := newRig(t)
	r.dial(t, echo)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := r.gw.Send(ctx, r.serverID, proto.Command{Key: "dep_1:build", Name: proto.CmdBuild})
	if err != nil || !res.OK || res.Key != "dep_1:build" {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestLargeResultAfterHello(t *testing.T) {
	r := newRig(t)
	out := strings.Repeat("x", 1<<20)
	r.dial(t, func(_ context.Context, c proto.Command) proto.Result {
		return proto.Result{Key: c.Key, OK: true, Output: out}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := r.gw.Send(ctx, r.serverID, proto.Command{Key: "dep_1:build", Name: proto.CmdBuild})
	if err != nil || res.Output != out {
		t.Fatalf("got %d bytes, %v", len(res.Output), err)
	}
}

func TestRejectsForgedHello(t *testing.T) {
	r := newRig(t)
	other, _ := join.LoadOrCreateCA(t.TempDir())
	forged := filepath.Join(t.TempDir(), "agent")
	os.MkdirAll(forged, 0o700)
	for _, f := range []string{"agent.key", "server_id", "server_url"} {
		b, _ := os.ReadFile(filepath.Join(r.agentDir, f))
		os.WriteFile(filepath.Join(forged, f), b, 0o600)
	}
	os.WriteFile(filepath.Join(forged, "agent.crt"), other.PEM, 0o600)
	os.WriteFile(filepath.Join(forged, "ca.crt"), other.PEM, 0o600)
	fcfg, err := conn.LoadConfig(forged, "test")
	if err != nil {
		t.Fatal(err)
	}
	st, _ := state.Open(filepath.Join(forged, "agent.db"))
	defer st.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	go conn.Dial(ctx, fcfg, st, echo)
	<-ctx.Done()
	if r.gw.Connected(r.serverID) {
		t.Fatal("a cert from another CA was accepted")
	}
}

func TestReplayAfterReconnect(t *testing.T) {
	r := newRig(t)
	var calls atomic.Int32
	release := make(chan struct{})
	r.dial(t, func(ctx context.Context, c proto.Command) proto.Result {
		calls.Add(1)
		<-release
		return proto.Result{Key: c.Key, OK: true, Output: "built"}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan proto.Result, 1)
	go func() {
		res, err := r.gw.Send(ctx, r.serverID, proto.Command{Key: "dep_9:build", Name: proto.CmdBuild})
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	for calls.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	r.gw.Drop(r.serverID)
	close(release)
	res := <-done
	if !res.OK || res.Output != "built" {
		t.Fatalf("got %+v", res)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("handler ran %d times, want 1", n)
	}
}

func TestHeartbeatTouches(t *testing.T) {
	r := newRig(t)
	r.dial(t, echo)
	deadline := time.Now().Add(3 * time.Second)
	for {
		s, err := r.st.Q.GetServer(context.Background(), r.serverID)
		if err == nil && s.LastHeartbeat.Valid && s.AgentVersion == "test" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no heartbeat recorded: %+v %v", s, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRejoinRevokesOldCert(t *testing.T) {
	r := newRig(t)
	tok, _ := join.NewToken(context.Background(), r.st.Q, time.Now())
	newDir := filepath.Join(t.TempDir(), "agent2")
	if err := conn.Enroll(context.Background(), r.url, tok, "hel1", "1.2.3.4", newDir); err != nil {
		t.Fatal(err)
	}
	oldCfg, _ := conn.LoadConfig(r.agentDir, "test")
	st, _ := state.Open(filepath.Join(t.TempDir(), "old.db"))
	defer st.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	go conn.Dial(ctx, oldCfg, st, echo)
	<-ctx.Done()
	if r.gw.Connected(r.serverID) {
		t.Fatal("the certificate issued before the re-join is still accepted")
	}
	r.agentDir = newDir
	r.dial(t, echo)
}

func TestCertRenewal(t *testing.T) {
	r := newRig(t)
	before, _ := os.ReadFile(filepath.Join(r.agentDir, "agent.crt"))
	cfg, err := conn.LoadConfig(r.agentDir, "test")
	if err != nil {
		t.Fatal(err)
	}
	cfg.HeartbeatEvery = 50 * time.Millisecond
	cfg.RenewBefore = 31 * 24 * time.Hour
	st, _ := state.Open(filepath.Join(r.agentDir, "agent.db"))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); st.Close() })
	go conn.Dial(ctx, cfg, st, echo)

	deadline := time.Now().Add(10 * time.Second)
	for {
		after, _ := os.ReadFile(filepath.Join(r.agentDir, "agent.crt"))
		srv, _ := r.st.Q.GetServer(context.Background(), r.serverID)
		c, _ := join.ParseCertPEM(after)
		if string(after) != string(before) && c != nil && srv.CertSerial == c.SerialNumber.Text(16) && r.gw.Connected(r.serverID) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("certificate was not renewed and accepted")
		}
		time.Sleep(50 * time.Millisecond)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if res, err := r.gw.Send(ctx2, r.serverID, proto.Command{Key: "after-renew", Name: proto.CmdBuild}); err != nil || !res.OK {
		t.Fatalf("commands after renewal: %+v %v", res, err)
	}
}

func TestSilentAgentIsDropped(t *testing.T) {
	r := newRig(t)
	r.gw.PingEvery = 100 * time.Millisecond
	cfg, _ := conn.LoadConfig(r.agentDir, "test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ws, _, err := websocket.Dial(ctx, strings.Replace(r.url, "http://", "ws://", 1)+"/agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	_, b, _ := ws.Read(ctx)
	env, _ := proto.Decode(b)
	var ch proto.Challenge
	json.Unmarshal(env.Body, &ch)
	sum := sha256.Sum256(proto.AuthMessage(ch.Nonce))
	sig, _ := ecdsa.SignASN1(rand.Reader, cfg.Key, sum[:])
	hello, _ := proto.Encode(proto.TypeHello, "", proto.Hello{Protocol: proto.Version, ServerID: cfg.ServerID, CertPEM: cfg.CertPEM, Sig: sig})
	ws.Write(ctx, websocket.MessageText, hello)
	waitConnected := time.Now().Add(2 * time.Second)
	for !r.gw.Connected(r.serverID) && time.Now().Before(waitConnected) {
		time.Sleep(10 * time.Millisecond)
	}
	if !r.gw.Connected(r.serverID) {
		t.Fatal("setup: never connected")
	}
	deadline := time.Now().Add(3 * time.Second)
	for r.gw.Connected(r.serverID) {
		if time.Now().After(deadline) {
			t.Fatal("a silent agent is still counted as connected")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRevealRoundTrip(t *testing.T) {
	r := newRig(t)
	kr, err := secrets.Load(r.agentDir)
	if err != nil {
		t.Fatal(err)
	}
	d := exec.NewDispatcher(&runtime.Fake{}, nil, nil)
	d.Keys = kr
	r.dial(t, d.Handle)
	box, _ := seal.Seal(kr.Public, []byte("s3cret value"))
	pub, priv, _ := seal.GenerateKey()
	cmd, _ := proto.NewCommand("reveal:1", proto.CmdReveal, proto.RevealArgs{Box: box, To: pub.String()})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := r.gw.Send(ctx, r.serverID, cmd)
	var data proto.RevealData
	json.Unmarshal(res.Data, &data)
	got, oerr := seal.Open(pub, priv, data.Box)
	if err != nil || oerr != nil || string(got) != "s3cret value" {
		t.Fatalf("%+v %v %v %q", res, err, oerr, got)
	}
}

func rawHello(t *testing.T, r *rig, key *ecdsa.PrivateKey, certPEM string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, strings.Replace(r.url, "http", "ws", 1)+"/agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	_, b, err := c.Read(ctx)
	var env proto.Envelope
	var ch proto.Challenge
	if err != nil || json.Unmarshal(b, &env) != nil || json.Unmarshal(env.Body, &ch) != nil {
		t.Fatalf("challenge: %v %s", err, b)
	}
	sum := sha256.Sum256(proto.AuthMessage(ch.Nonce))
	sig, _ := ecdsa.SignASN1(rand.Reader, key, sum[:])
	hello, _ := proto.Encode(proto.TypeHello, "", proto.Hello{AgentVersion: "old", Protocol: proto.Version, ServerID: r.serverID, CertPEM: certPEM, Sig: sig})
	if err := c.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatal(err)
	}
	return c
}

func rawRenew(t *testing.T, r *rig, c *websocket.Conn) (string, *ecdsa.PrivateKey) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: r.serverID}}, k)
	msg, _ := proto.Encode(proto.TypeRenew, "", proto.Renew{CSRPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))})
	if c.Write(ctx, websocket.MessageText, msg) != nil {
		return "", nil
	}
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				t.Fatal("the gateway neither renewed nor closed the connection")
			}
			return "", nil
		}
		var env proto.Envelope
		var out proto.Renewed
		if json.Unmarshal(b, &env) == nil && env.Type == proto.TypeRenewed && json.Unmarshal(env.Body, &out) == nil {
			return out.CertPEM, k
		}
	}
}

func serialOf(t *testing.T, certPEM string) string {
	t.Helper()
	c, err := join.ParseCertPEM([]byte(certPEM))
	if err != nil {
		t.Fatal(err)
	}
	return c.SerialNumber.Text(16)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
	}
}

func rejoin(t *testing.T, r *rig) string {
	t.Helper()
	tok, _ := join.NewToken(context.Background(), r.st.Q, time.Now())
	dir := filepath.Join(t.TempDir(), "agent2")
	if err := conn.Enroll(context.Background(), r.url, tok, "hel1", "1.2.3.4", dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func refused(c *websocket.Conn) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		if _, _, err := c.Read(ctx); err != nil {
			return ctx.Err() == nil
		}
	}
}

func TestRejoinRevokesHoardedRenewal(t *testing.T) {
	r := newRig(t)
	old, _ := conn.LoadConfig(r.agentDir, "test")
	c := rawHello(t, r, old.Key, old.CertPEM)
	waitFor(t, "the old agent never connected", func() bool { return r.gw.Connected(r.serverID) })
	c2, k2 := rawRenew(t, r, c)
	if c2 == "" {
		t.Fatal("no renewal before the re-join")
	}
	c.CloseNow()
	waitFor(t, "the old agent never left", func() bool { return !r.gw.Connected(r.serverID) })

	r.agentDir = rejoin(t, r)
	r.dial(t, echo)
	if !refused(rawHello(t, r, k2, c2)) {
		t.Fatal("the hoarded renewal was accepted")
	}
	if s, _ := r.st.Q.GetServer(context.Background(), r.serverID); s.CertSerial == serialOf(t, c2) || s.NextCertSerial != "" {
		t.Fatalf("serials %q %q", s.CertSerial, s.NextCertSerial)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if res, err := r.gw.Send(ctx, r.serverID, proto.Command{Key: "after-rejoin", Name: proto.CmdBuild}); err != nil || !res.OK || res.Output == "forged" {
		t.Fatalf("result %+v %v", res, err)
	}
}

func TestRejoinRevokesLiveConnection(t *testing.T) {
	r := newRig(t)
	old, _ := conn.LoadConfig(r.agentDir, "test")
	c := rawHello(t, r, old.Key, old.CertPEM)
	waitFor(t, "the old agent never connected", func() bool { return r.gw.Connected(r.serverID) })
	rejoin(t, r)
	if !refused(c) {
		t.Fatal("the re-join left the old connection open")
	}
	waitFor(t, "the gateway still holds the old connection", func() bool { return !r.gw.Connected(r.serverID) })
}

func TestRenewRefusedOnceReplaced(t *testing.T) {
	r := newRig(t)
	old, _ := conn.LoadConfig(r.agentDir, "test")
	c := rawHello(t, r, old.Key, old.CertPEM)
	waitFor(t, "the old agent never connected", func() bool { return r.gw.Connected(r.serverID) })
	if err := r.st.Q.SetServerCert(context.Background(), db.SetServerCertParams{CertSerial: "beef", UpdatedAt: 1, ID: r.serverID}); err != nil {
		t.Fatal(err)
	}
	if c2, _ := rawRenew(t, r, c); c2 != "" {
		t.Fatal("a replaced certificate's connection renewed")
	}
	if s, _ := r.st.Q.GetServer(context.Background(), r.serverID); s.NextCertSerial != "" {
		t.Fatalf("next %q", s.NextCertSerial)
	}
	waitFor(t, "the replaced connection stayed", func() bool { return !r.gw.Connected(r.serverID) })
}
