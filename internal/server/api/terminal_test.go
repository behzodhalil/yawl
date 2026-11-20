package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type fakeTerm struct {
	in     chan []byte
	out    chan []byte
	done   chan string
	opened string
	resize string
}

func (f *fakeTerm) OpenTerm(_ context.Context, server, container string, cols, rows int) (TermSession, error) {
	f.opened = server + " " + container
	return f, nil
}
func (f *fakeTerm) Out() <-chan []byte                       { return f.out }
func (f *fakeTerm) Done() <-chan string                      { return f.done }
func (f *fakeTerm) Input(_ context.Context, b []byte) error  { f.in <- b; return nil }
func (f *fakeTerm) Resize(_ context.Context, c, r int) error { f.resize = "ok"; return nil }
func (f *fakeTerm) Close()                                   {}

func TestAPITerminal(t *testing.T) {
	_, tok, a := setupApp(t)
	ft := &fakeTerm{in: make(chan []byte, 4), out: make(chan []byte, 4), done: make(chan string, 1)}
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", Terms: ft}))
	defer srv.Close()
	call(t, srv, tok, "POST", "/api/apply", yaml)
	testDB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, container, created_at, updated_at) SELECT 'dep_live', id, 'c', 'live', 'yawl-acme-production-api-dep_live', 1, 1 FROM services WHERE name = 'api'`)
	testDB.Exec(`UPDATE services SET current_deploy_id = 'dep_live' WHERE name = 'api'`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/services/acme/production/api/terminal?cols=100&rows=30"
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	c.Write(ctx, websocket.MessageBinary, []byte("ls\n"))
	if b := <-ft.in; string(b) != "ls\n" || ft.opened != "srv_1 yawl-acme-production-api-dep_live" {
		t.Fatalf("%q %q", b, ft.opened)
	}
	ft.out <- []byte("a.txt\n")
	if _, b, err := c.Read(ctx); err != nil || string(b) != "a.txt\n" {
		t.Fatalf("%q %v", b, err)
	}
	ft.done <- "the shell ended"
	if typ, b, _ := c.Read(ctx); typ != websocket.MessageText || !strings.Contains(string(b), "the shell ended") {
		t.Fatalf("%v %q", typ, b)
	}
	var n int
	testDB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'terminal.opened'`).Scan(&n)
	if n != 1 {
		t.Fatalf("audit %d", n)
	}
	viewer := memberToken(t, srv, tok, "v3@acme.uz", "viewer")
	if _, res, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + viewer}}}); err == nil || res == nil || res.StatusCode != 403 {
		t.Fatalf("a Viewer: %v %v", res, err)
	}
}

type floodTerm struct {
	fakeTerm
	closed chan struct{}
}

func (f *floodTerm) OpenTerm(context.Context, string, string, int, int) (TermSession, error) {
	go func() {
		for {
			select {
			case f.out <- make([]byte, 1<<20):
			case <-f.closed:
				return
			}
		}
	}()
	return f, nil
}
func (f *floodTerm) Close() {
	select {
	case <-f.closed:
	default:
		close(f.closed)
	}
}

func TestAPITerminalMinors(t *testing.T) {
	_, tok, a := setupApp(t)
	ft := &fakeTerm{in: make(chan []byte, 4), out: make(chan []byte, 4), done: make(chan string, 1)}
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", Terms: ft}))
	defer srv.Close()
	call(t, srv, tok, "POST", "/api/apply", yaml)
	testDB.Exec(`INSERT INTO deploys (id, service_id, commit_sha, state, container, created_at, updated_at) SELECT 'dep_live', id, 'c', 'live', 'yawl-acme-production-api-dep_live', 1, 1 FROM services WHERE name = 'api'`)
	testDB.Exec(`UPDATE services SET current_deploy_id = 'dep_live' WHERE name = 'api'`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/services/acme/production/api/terminal"
	auth := &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}}}
	c, _, err := websocket.Dial(ctx, url, auth)
	if err != nil {
		t.Fatal(err)
	}
	ft.done <- "not a Yawl service's container"
	c.Read(ctx)
	c.CloseNow()
	var n int
	testDB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'terminal.opened'`).Scan(&n)
	if n != 0 {
		t.Fatalf("a refused open audited as opened: %d", n)
	}

	old := browserWriteWait
	browserWriteWait = 200 * time.Millisecond
	defer func() { browserWriteWait = old }()
	fl := &floodTerm{fakeTerm: fakeTerm{out: make(chan []byte), done: make(chan string, 1)}, closed: make(chan struct{})}
	srv2 := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", Terms: fl}))
	defer srv2.Close()
	c, _, err = websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv2.URL, "http")+"/api/services/acme/production/api/terminal", auth)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	select {
	case <-fl.closed:
	case <-time.After(8 * time.Second):
		t.Fatal("a browser that stopped reading held the session")
	}
}
