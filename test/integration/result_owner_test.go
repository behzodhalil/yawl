package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/behzodhalil/yawl/internal/agent/conn"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/join"
)

func (r *rig) rogue(t *testing.T) func(proto.Result) {
	t.Helper()
	ctx := context.Background()
	tok, err := join.NewToken(ctx, r.st.Q, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "b")
	if err := conn.Enroll(ctx, r.url, tok, "hel2", "5.6.7.8", dir); err != nil {
		t.Fatal(err)
	}
	cfg, err := conn.LoadConfig(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(r.url, "http")+"/agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	_, b, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := proto.Decode(b)
	var ch proto.Challenge
	json.Unmarshal(env.Body, &ch)
	sum := sha256.Sum256(proto.AuthMessage(ch.Nonce))
	sig, _ := ecdsa.SignASN1(rand.Reader, cfg.Key, sum[:])
	hello, _ := proto.Encode(proto.TypeHello, "", proto.Hello{AgentVersion: "test", Protocol: proto.Version, ServerID: cfg.ServerID, CertPEM: cfg.CertPEM, Sig: sig})
	ws.Write(ctx, websocket.MessageText, hello)
	go func() {
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				return
			}
		}
	}()
	for deadline := time.Now().Add(5 * time.Second); !r.gw.Connected(cfg.ServerID); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("rogue agent never connected")
		}
	}
	return func(res proto.Result) {
		msg, _ := proto.Encode(proto.TypeResult, res.Key, res)
		if err := ws.Write(ctx, websocket.MessageText, msg); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResultBoundToAgent(t *testing.T) {
	r := newRig(t)
	got, release := make(chan proto.Command, 4), make(chan struct{})
	r.dial(t, func(ctx context.Context, c proto.Command) proto.Result {
		got <- c
		<-release
		data, _ := json.Marshal(proto.StartData{Container: "genuine"})
		return proto.Result{Key: c.Key, OK: true, Data: data}
	})
	forge := r.rogue(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan proto.Result, 1)
	go func() {
		res, _ := r.gw.Send(ctx, r.serverID, proto.Command{Key: "dep_1:start", Name: proto.CmdStart})
		done <- res
	}()
	<-got
	data, _ := json.Marshal(proto.StartData{Container: "yawl-victim-production-db"})
	forge(proto.Result{Key: "dep_1:start", OK: true, Data: data})
	time.Sleep(100 * time.Millisecond)
	close(release)
	var s proto.StartData
	json.Unmarshal((<-done).Data, &s)
	if s.Container != "genuine" {
		t.Fatalf("A's start took %q", s.Container)
	}
}

func TestSendNeedsKey(t *testing.T) {
	r := newRig(t)
	if _, err := r.gw.Send(context.Background(), r.serverID, proto.Command{Name: proto.CmdInspect}); err == nil {
		t.Fatal("keyless command sent")
	}
}
