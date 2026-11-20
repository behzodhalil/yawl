//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/behzodhalil/yawl/internal/server/app"
)

func TestE2ETerminal(t *testing.T) {
	b := setup(t)
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tok, err := app.New(b.store()).CreateToken(ctx, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := websocket.Dial(ctx, "ws://"+listen+"/api/services/e2e/production/hello/terminal?cols=100&rows=30",
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	c.Write(ctx, websocket.MessageBinary, []byte("echo yawl-$((40+2)) $HOSTNAME; exit\n"))
	var out strings.Builder
	for !strings.Contains(out.String(), "yawl-42") {
		typ, b, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("%v after %q", err, out.String())
		}
		if typ == websocket.MessageText && strings.Contains(string(b), `"end"`) {
			out.Write(b)
			break
		}
		out.Write(b)
	}
	if !strings.Contains(out.String(), "yawl-42") {
		t.Fatalf("%q", out.String())
	}
}
