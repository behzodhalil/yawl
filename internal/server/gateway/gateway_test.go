package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/behzodhalil/yawl/internal/proto"
)

func TestChallengeCarriesVersion(t *testing.T) {
	g := &Gateway{Version: "1.2.0"}
	env, err := proto.Decode(g.challenge([]byte("0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	json.Unmarshal(env.Body, &raw)
	if raw["server_version"] != "1.2.0" || env.Type != proto.TypeChallenge {
		t.Fatalf("%s", env.Body)
	}
}

// A stopping server drops its agent connections, so each agent reconnects to
// whatever serves next (an update, a restart). Hijacked WebSockets outlive
// http.Server.Shutdown otherwise.
func TestCloseDropsAgents(t *testing.T) {
	g := New(nil, nil, nil)
	n := 0
	g.conns["srv_1"] = &agentConn{cancel: func() { n++ }}
	g.conns["srv_2"] = &agentConn{cancel: func() { n++ }}
	g.Close()
	if n != 2 || g.Connected("srv_1") {
		t.Fatalf("cancelled %d", n)
	}
}

func TestOpenTerm(t *testing.T) {
	got := make(chan proto.Envelope, 8)
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		for {
			_, b, err := c.Read(r.Context())
			if err != nil {
				return
			}
			env, _ := proto.Decode(b)
			got <- env
		}
	}))
	defer agent.Close()
	ctx := context.Background()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(agent.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	g := New(nil, nil, nil)
	g.conns["srv_1"] = &agentConn{ws: ws, closed: make(chan struct{}), cancel: func() {}}
	term, err := g.OpenTerm(ctx, "srv_1", "yawl-acme-production-api-dep_1", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if env := <-got; env.Type != proto.TypeTermOpen || !strings.Contains(string(env.Body), "yawl-acme-production-api-dep_1") {
		t.Fatalf("%s %s", env.Type, env.Body)
	}
	term.Input(ctx, []byte("ls\n"))
	if env := <-got; env.Type != proto.TypeTermIn {
		t.Fatalf("%s", env.Type)
	}
	out, _ := proto.Encode(proto.TypeTermOut, "", proto.TermData{ID: term.ID, Data: []byte("a.txt\n")})
	env, _ := proto.Decode(out)
	g.deliverTerm("srv_1", env)
	if b := <-term.Out(); string(b) != "a.txt\n" {
		t.Fatalf("%q", b)
	}
	end, _ := proto.Encode(proto.TypeTermEnd, "", proto.TermEnd{ID: term.ID, Cause: "the shell ended"})
	env, _ = proto.Decode(end)
	g.deliverTerm("srv_1", env)
	if c := <-term.Done(); c != "the shell ended" {
		t.Fatalf("%q", c)
	}
	t2, _ := g.OpenTerm(ctx, "srv_1", "yawl-x", 80, 24)
	<-got
	out, _ = proto.Encode(proto.TypeTermOut, "", proto.TermData{ID: t2.ID, Data: []byte("forged")})
	env, _ = proto.Decode(out)
	g.deliverTerm("srv_2", env)
	select {
	case b := <-t2.Out():
		t.Fatalf("another agent wrote %q", b)
	default:
	}
	g.dropTerms("srv_1", g.conns["srv_1"])
	if c := <-t2.Done(); c != "the agent went away" {
		t.Fatalf("%q", c)
	}
}

func deafAgent(t *testing.T) *websocket.Conn {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := websocket.Accept(w, r, nil); err == nil {
			<-r.Context().Done()
		}
	}))
	t.Cleanup(agent.Close)
	ws, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(agent.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestTermConnMinors(t *testing.T) {
	ctx := context.Background()
	g := New(nil, nil, nil)
	old := &agentConn{ws: deafAgent(t), closed: make(chan struct{}), cancel: func() {}}
	g.conns["srv_1"] = old
	t1, _ := g.OpenTerm(ctx, "srv_1", "yawl-a", 80, 24)
	g.conns["srv_1"] = &agentConn{ws: deafAgent(t), closed: make(chan struct{}), cancel: func() {}}
	t2, _ := g.OpenTerm(ctx, "srv_1", "yawl-b", 80, 24)
	g.dropTerms("srv_1", old)
	if c := <-t1.Done(); c != "the agent went away" {
		t.Fatalf("%q", c)
	}
	select {
	case c := <-t2.Done():
		t.Fatalf("a session on the new connection ended: %q", c)
	default:
	}

	wait := termWriteWait
	termWriteWait = 200 * time.Millisecond
	defer func() { termWriteWait = wait }()
	for t2.Input(ctx, make([]byte, 1<<20)) == nil {
	}
	done := make(chan struct{})
	go func() { t2.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited on a stalled connection")
	}
}

func TestPreHelloReadLimitIsSmall(t *testing.T) {
	srv := httptest.NewServer(New(nil, nil, nil).Handler())
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	big := []byte(strings.Repeat("x", 64<<10))
	for i := 0; i < 3; i++ {
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if err != nil {
			t.Fatalf("peer %d: %v", i, err)
		}
		t.Cleanup(func() { c.CloseNow() })
		if _, _, err := c.Read(ctx); err != nil {
			t.Fatalf("peer %d: %v", i, err)
		}
		c.Write(ctx, websocket.MessageText, big)
		if _, _, err := c.Read(ctx); websocket.CloseStatus(err) != websocket.StatusMessageTooBig {
			t.Fatalf("peer %d: want cut off at the read limit (1009), got %v", i, err)
		}
	}
}
