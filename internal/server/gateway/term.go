package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/coder/websocket"

	"github.com/behzodhalil/yawl/internal/proto"
)

type Term struct {
	ID     string
	server string
	conn   *agentConn
	g      *Gateway
	out    chan []byte
	done   chan string
}

func (t *Term) Out() <-chan []byte  { return t.out }
func (t *Term) Done() <-chan string { return t.done }

func (t *Term) Input(ctx context.Context, b []byte) error {
	return t.g.write(ctx, t.server, proto.TypeTermIn, proto.TermData{ID: t.ID, Data: b})
}

func (t *Term) Resize(ctx context.Context, cols, rows int) error {
	return t.g.write(ctx, t.server, proto.TypeTermResize, proto.TermResize{ID: t.ID, Cols: cols, Rows: rows})
}

func (t *Term) Close() {
	t.g.write(context.Background(), t.server, proto.TypeTermClose, proto.TermEnd{ID: t.ID})
	t.g.endTerm(t.ID, "closed")
}

func (g *Gateway) OpenTerm(ctx context.Context, serverID, container string, cols, rows int) (*Term, error) {
	b := make([]byte, 12)
	rand.Read(b)
	t := &Term{ID: "term_" + hex.EncodeToString(b), server: serverID, g: g, out: make(chan []byte, 256), done: make(chan string, 1)}
	g.mu.Lock()
	if g.terms == nil {
		g.terms = map[string]*Term{}
	}
	g.terms[t.ID] = t
	t.conn = g.conns[serverID]
	g.mu.Unlock()
	if err := g.write(ctx, serverID, proto.TypeTermOpen, proto.TermOpen{ID: t.ID, Container: container, Cols: cols, Rows: rows}); err != nil {
		g.endTerm(t.ID, err.Error())
		return nil, err
	}
	return t, nil
}

var termWriteWait = 10 * time.Second

func (g *Gateway) write(ctx context.Context, serverID, typ string, body any) error {
	ctx, cancel := context.WithTimeout(ctx, termWriteWait)
	defer cancel()
	g.mu.Lock()
	c := g.conns[serverID]
	g.mu.Unlock()
	if c == nil {
		return errors.New("the server's agent is not connected")
	}
	msg, err := proto.Encode(typ, "", body)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.ws.Write(ctx, websocket.MessageText, msg)
}

func (g *Gateway) deliverTerm(serverID string, env proto.Envelope) {
	switch env.Type {
	case proto.TypeTermOut:
		var d proto.TermData
		if json.Unmarshal(env.Body, &d) != nil {
			return
		}
		g.mu.Lock()
		t := g.terms[d.ID]
		g.mu.Unlock()
		if t != nil && t.server == serverID {
			select {
			case t.out <- d.Data:
			default:
			}
		}
	case proto.TypeTermEnd:
		var e proto.TermEnd
		if json.Unmarshal(env.Body, &e) != nil {
			return
		}
		g.mu.Lock()
		t := g.terms[e.ID]
		g.mu.Unlock()
		if t != nil && t.server == serverID {
			g.endTerm(e.ID, e.Cause)
		}
	}
}

func (g *Gateway) endTerm(id, cause string) {
	g.mu.Lock()
	t := g.terms[id]
	delete(g.terms, id)
	g.mu.Unlock()
	if t != nil {
		t.done <- cause
	}
}

func (g *Gateway) dropTerms(serverID string, c *agentConn) {
	g.mu.Lock()
	var ids []string
	for id, t := range g.terms {
		if t.server == serverID && t.conn == c {
			ids = append(ids, id)
		}
	}
	g.mu.Unlock()
	for _, id := range ids {
		g.endTerm(id, "the agent went away")
	}
}
