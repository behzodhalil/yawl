package exec

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

type pipeShell struct {
	r       *io.PipeReader
	w       *io.PipeWriter
	resized []uint
}

func newPipeShell() *pipeShell { r, w := io.Pipe(); return &pipeShell{r: r, w: w} }

func (p *pipeShell) Read(b []byte) (int, error) { return p.r.Read(b) }
func (p *pipeShell) Write(b []byte) (int, error) {
	return p.w.Write([]byte(strings.ToUpper(string(b))))
}
func (p *pipeShell) Close() error { return p.w.Close() }
func (p *pipeShell) Resize(_ context.Context, c, r uint) error {
	p.resized = append(p.resized, c, r)
	return nil
}

type sent struct {
	mu   sync.Mutex
	msgs []string
}

func (s *sent) send(typ string, body any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch b := body.(type) {
	case proto.TermData:
		s.msgs = append(s.msgs, typ+" "+string(b.Data))
	case proto.TermEnd:
		s.msgs = append(s.msgs, typ+" "+b.Cause)
	}
	return nil
}

func (s *sent) has(m string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.msgs {
		if strings.Contains(x, m) {
			return true
		}
	}
	return false
}

func TestTerminals(t *testing.T) {
	sh := newPipeShell()
	rt := &runtime.Fake{OnExec: func(string, []string) (runtime.Shell, error) { return sh, nil }, States: map[string]runtime.State{
		"c0ffee": {Running: true, Name: "yawl-acme-production-api-dep_1"},
		"cad":    {Running: true, Name: "yawl-caddy"},
		"pg":     {Running: true, Name: "postgres"},
		"gone":   {Running: false, Name: "yawl-acme-production-api-dep_0"},
	}}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	out := &sent{}
	ts := d.Terminals()
	ts.Open(context.Background(), proto.TermOpen{ID: "t1", Container: "c0ffee", Cols: 80, Rows: 24}, out.send)
	ts.Input(proto.TermData{ID: "t1", Data: []byte("ls\n")})
	ts.Resize(proto.TermResize{ID: "t1", Cols: 120, Rows: 40})
	deadline := time.Now().Add(2 * time.Second)
	for !out.has("term.out LS") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ts.Close("t1")
	for !out.has("term.end") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !out.has("term.out LS") || !out.has("term.end") || len(sh.resized) != 2 || sh.resized[0] != 120 {
		t.Fatalf("%v %v", out.msgs, sh.resized)
	}
	for c, want := range map[string]string{"cad": "not a Yawl service's container", "pg": "not a Yawl service's container",
		"nothing": "not a Yawl service's container", "gone": "the container is not running"} {
		bad := &sent{}
		ts.Open(context.Background(), proto.TermOpen{ID: "t2", Container: c}, bad.send)
		for deadline := time.Now().Add(2 * time.Second); !bad.has("term.end") && time.Now().Before(deadline); {
			time.Sleep(10 * time.Millisecond)
		}
		if !bad.has("term.end " + want) {
			t.Errorf("%s: %v", c, bad.msgs)
		}
	}
	if len(rt.Execs) != 1 {
		t.Fatalf("execs %v", rt.Execs)
	}
}

func TestTerminalInputBeforeShell(t *testing.T) {
	sh := newPipeShell()
	ready := make(chan struct{})
	rt := &runtime.Fake{States: map[string]runtime.State{"c0ffee": {Running: true, Name: "yawl-acme-production-api-dep_1"}},
		OnExec: func(string, []string) (runtime.Shell, error) { <-ready; return sh, nil }}
	ts := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{}).Terminals()
	out := &sent{}
	ts.Open(context.Background(), proto.TermOpen{ID: "t1", Container: "c0ffee"}, out.send)
	time.Sleep(20 * time.Millisecond)
	ts.Input(proto.TermData{ID: "t1", Data: []byte("early\n")})
	ts.Resize(proto.TermResize{ID: "t1", Cols: 90, Rows: 20})
	close(ready)
	for deadline := time.Now().Add(2 * time.Second); !out.has("EARLY") && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if !out.has("term.out EARLY") {
		t.Fatalf("%v", out.msgs)
	}
	ts.Close("t1")
}

type stuckShell struct {
	*pipeShell
	closed chan struct{}
}

func (s *stuckShell) Write(b []byte) (int, error) { <-s.closed; return 0, io.ErrClosedPipe }
func (s *stuckShell) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return s.pipeShell.Close()
}

func TestTerminalStuckShell(t *testing.T) {
	sh := &stuckShell{newPipeShell(), make(chan struct{})}
	rt := &runtime.Fake{States: map[string]runtime.State{"c0ffee": {Running: true, Name: "yawl-acme-production-api-dep_1"}},
		OnExec: func(string, []string) (runtime.Shell, error) { return sh, nil }}
	ts := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{}).Terminals()
	out := &sent{}
	ts.Open(context.Background(), proto.TermOpen{ID: "t1", Container: "c0ffee"}, out.send)
	start := time.Now()
	for i := 0; i < 1000; i++ {
		ts.Input(proto.TermData{ID: "t1", Data: []byte("x")})
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("1000 keystrokes into a stuck shell took %v", d)
	}
	ts.Close("t1")
	for deadline := time.Now().Add(2 * time.Second); !out.has("term.end") && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if !out.has("term.end") {
		t.Fatal("a close did not end a stuck shell")
	}
}

type hungRT struct{ *runtime.Fake }

func (hungRT) Inspect(ctx context.Context, _ string) (runtime.State, error) {
	<-ctx.Done()
	return runtime.State{}, ctx.Err()
}

func TestTerminalHungDocker(t *testing.T) {
	old := termOpenWait
	termOpenWait = 100 * time.Millisecond
	defer func() { termOpenWait = old }()
	d := NewDispatcher(hungRT{&runtime.Fake{}}, &fakeProxy{}, &fakeBuilder{})
	out := &sent{}
	d.Terminals().Open(context.Background(), proto.TermOpen{ID: "t", Container: "yawl-acme"}, out.send)
	for i := 0; i < 200 && !out.has("term.end"); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !out.has("term.end") {
		t.Fatal("still waiting on Docker")
	}
}
