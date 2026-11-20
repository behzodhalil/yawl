package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type logsSender struct{ got proto.LogsArgs }

func (l *logsSender) Send(_ context.Context, _ string, c proto.Command) (proto.Result, error) {
	json.Unmarshal(c.Args, &l.got)
	b, _ := json.Marshal(proto.LogsData{Lines: []proto.LogsLine{{Text: "hi"}, {Text: "boom", Stream: "stderr"}}})
	return proto.Result{Key: c.Key, OK: true, Data: b}, nil
}

func TestAPILogs(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_1", Name: "hel1", PublicIp: "1.2.3.4", AgentPubkey: base64.StdEncoding.EncodeToString(make([]byte, 32)), CreatedAt: 1, UpdatedAt: 1})
	a := app.New(st)
	tok, _ := a.CreateToken(ctx, "laptop")
	gw := &logsSender{}
	srv := httptest.NewServer(New(Deps{App: a, GW: gw}))
	defer srv.Close()
	call(t, srv, tok, "POST", "/api/apply", yaml)
	put(t, srv, tok, "/api/recovery-key", `{"pubkey":"`+base64.StdEncoding.EncodeToString(make([]byte, 32))+`"}`)
	put(t, srv, tok, "/api/services/acme/production/api/env/API_KEY", sealed())
	since := time.Date(2026, 10, 24, 3, 0, 0, 5, time.UTC)
	res, b := call(t, srv, tok, "GET", "/api/services/acme/production/api/logs?q=err&n=50&since="+since.Format(time.RFC3339Nano), "")
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var data proto.LogsData
	json.Unmarshal(b, &data)
	g := gw.got
	if len(data.Lines) != 2 || g.Query != "err" || g.Limit != 50 || !g.Since.Equal(since) || g.Project != "acme" || g.Service != "api" ||
		len(g.Vars) != 1 || g.Vars[0].Key != "API_KEY" || g.Drain {
		t.Fatalf("data %+v args %+v", data, g)
	}
	if !strings.Contains(string(b), `"Text":"hi"}`) || !strings.Contains(string(b), `"Text":"boom","Stream":"stderr"}`) {
		t.Fatalf("%s", b)
	}
	call(t, srv, tok, "GET", "/api/services/acme/production/api/logs?from=drain", "")
	if !gw.got.Drain || gw.got.Stream != "" {
		t.Fatalf("%+v", gw.got)
	}
	call(t, srv, tok, "GET", "/api/services/acme/production/api/logs?stream=stderr", "")
	if gw.got.Stream != "stderr" {
		t.Fatalf("%+v", gw.got)
	}
	if res, _ := call(t, srv, tok, "GET", "/api/services/acme/production/api/logs?stream=stdin", ""); res.StatusCode != 400 {
		t.Fatalf("%d", res.StatusCode)
	}
}
