package exec

import (
	"context"
	"regexp"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

var termContainer = regexp.MustCompile(`^yawl-[a-z0-9][a-z0-9_-]*$`)

const (
	termIdle                  = 30 * time.Minute
	termMax                   = 4 * time.Hour
	termInputBufferBeforeDrop = 256
)

var termOpenWait = 30 * time.Second

var termShell = []string{"/bin/sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"}

type termSession struct {
	in     chan []byte
	touch  chan struct{}
	size   chan [2]uint
	closed chan struct{}
	once   sync.Once
}

func (s *termSession) close() { s.once.Do(func() { close(s.closed) }) }

type Terminals struct {
	rt runtime.Runtime
	mu sync.Mutex
	m  map[string]*termSession
}

func (d *Dispatcher) Terminals() *Terminals {
	d.termOnce.Do(func() { d.terms = &Terminals{rt: d.rt, m: map[string]*termSession{}} })
	return d.terms
}

func (t *Terminals) Open(ctx context.Context, o proto.TermOpen, send func(typ string, body any) error) {
	end := func(cause string) { send(proto.TypeTermEnd, proto.TermEnd{ID: o.ID, Cause: cause}) }
	s := &termSession{in: make(chan []byte, termInputBufferBeforeDrop), touch: make(chan struct{}, 1), size: make(chan [2]uint, 1), closed: make(chan struct{})}
	t.mu.Lock()
	t.m[o.ID] = s
	t.mu.Unlock()
	go t.run(ctx, o, s, end, send)
}

func (t *Terminals) run(ctx context.Context, o proto.TermOpen, s *termSession, end func(string), send func(string, any) error) {
	defer func() {
		t.mu.Lock()
		delete(t.m, o.ID)
		t.mu.Unlock()
	}()
	octx, ocancel := context.WithTimeout(ctx, termOpenWait) // Exec's stream outlives it: Docker's client unties a hijacked conn from ctx
	defer ocancel()
	st, err := t.rt.Inspect(octx, o.Container)
	if err != nil || !termContainer.MatchString(st.Name) || st.Name == "yawl-caddy" || st.Name == tsContainer {
		end("not a Yawl service's container")
		return
	}
	if !st.Running {
		end("the container is not running")
		return
	}
	sh, err := t.rt.Exec(octx, o.Container, termShell, uint(max(o.Cols, 20)), uint(max(o.Rows, 5)))
	if err != nil {
		end(err.Error())
		return
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), termMax)
	defer cancel()
	go func() {
		for {
			select {
			case b := <-s.in:
				if _, err := sh.Write(b); err != nil {
					return
				}
			case <-s.closed:
				return
			}
		}
	}()
	go func() {
		defer sh.Close()
		idle := time.NewTimer(termIdle)
		defer idle.Stop()
		for {
			select {
			case <-s.touch:
				idle.Reset(termIdle)
			case z := <-s.size:
				sh.Resize(sctx, z[0], z[1])
			case <-s.closed:
				return
			case <-idle.C:
				return
			case <-sctx.Done():
				return
			}
		}
	}()
	buf := make([]byte, 32<<10)
	for {
		n, err := sh.Read(buf)
		if n > 0 && send(proto.TypeTermOut, proto.TermData{ID: o.ID, Data: append([]byte(nil), buf[:n]...)}) != nil {
			s.close()
		}
		if err != nil {
			s.close()
			end("the shell ended")
			return
		}
	}
}

func (t *Terminals) get(id string) *termSession {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.m[id]
}

func (t *Terminals) Input(d proto.TermData) {
	if s := t.get(d.ID); s != nil {
		select {
		case s.in <- d.Data:
		default:
		}
		select {
		case s.touch <- struct{}{}:
		default:
		}
	}
}

func (t *Terminals) Resize(r proto.TermResize) {
	if s := t.get(r.ID); s != nil {
		select {
		case <-s.size:
		default:
		}
		select {
		case s.size <- [2]uint{uint(r.Cols), uint(r.Rows)}:
		default:
		}
	}
}

func (t *Terminals) Close(id string) {
	if s := t.get(id); s != nil {
		s.close()
	}
}
