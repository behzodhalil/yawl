package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/spec"
)

type TermSession interface {
	Out() <-chan []byte
	Done() <-chan string
	Input(ctx context.Context, b []byte) error
	Resize(ctx context.Context, cols, rows int) error
	Close()
}

type Terminals interface {
	OpenTerm(ctx context.Context, serverID, container string, cols, rows int) (TermSession, error)
}

func (h *handler) terminal(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.service(w, r)
	if !ok {
		return
	}
	if h.d.Terms == nil {
		http.Error(w, "terminals are not wired on this panel", http.StatusNotImplemented)
		return
	}
	container := ""
	if spec.IsDatabase(spec.Kind(svc.Kind)) {
		container = fmt.Sprintf("yawl-%s-%s-%s", svc.Project, svc.Env, svc.Name)
	} else if svc.CurrentDeployID.Valid {
		if d, err := h.a.St.Q.GetDeploy(r.Context(), svc.CurrentDeployID.String); err == nil && d.State == "live" {
			container = d.Container
		}
	}
	if container == "" || svc.UserStopped == 1 {
		http.Error(w, svc.Name+" is not running; deploy or start it first.", http.StatusConflict)
		return
	}
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	c, err := websocket.Accept(w, r, nil) // the default checks Origin: another site cannot open one with the cookie
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := r.Context()
	t, err := h.d.Terms.OpenTerm(ctx, svc.ServerID, container, cols, rows)
	if err != nil {
		wctx, cancel := context.WithTimeout(ctx, browserWriteWait)
		defer cancel()
		c.Write(wctx, websocket.MessageText, mustJSON(map[string]string{"end": err.Error()}))
		return
	}
	defer t.Close()
	write := func(typ websocket.MessageType, b []byte) error {
		wctx, cancel := context.WithTimeout(ctx, browserWriteWait)
		defer cancel()
		return c.Write(wctx, typ, b)
	}
	audited := false
	go func() {
		for {
			typ, b, err := c.Read(ctx)
			if err != nil {
				t.Close()
				return
			}
			if typ == websocket.MessageBinary {
				t.Input(ctx, b)
				continue
			}
			var size struct{ Cols, Rows int }
			if json.Unmarshal(b, &size) == nil && size.Cols > 0 && size.Rows > 0 {
				t.Resize(ctx, size.Cols, size.Rows)
			}
		}
	}()
	for {
		select {
		case b := <-t.Out():
			if !audited {
				if err := app.Event(ctx, h.a.St.Q, time.Now().UnixMilli(), "terminal.opened", svc.ID, actor(r), map[string]string{"service": svc.ID, "container": container}); err != nil {
					write(websocket.MessageText, mustJSON(map[string]string{"end": "the audit log could not be written: " + err.Error()}))
					return
				}
				audited = true
			}
			if write(websocket.MessageBinary, b) != nil {
				return
			}
		case cause := <-t.Done():
			write(websocket.MessageText, mustJSON(map[string]string{"end": cause}))
			c.Close(websocket.StatusNormalClosure, "")
			return
		case <-ctx.Done():
			return
		}
	}
}

// browserWriteWait bounds each write to the browser: a hijacked request's
// context is never cancelled, so a browser that stops reading would hold the
// handler until TCP gives up.
var browserWriteWait = 10 * time.Second

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
