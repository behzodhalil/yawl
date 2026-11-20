package exec

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestDrain(t *testing.T) {
	var mu sync.Mutex
	var pushes []map[string]any
	var queried string
	loki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "123456" || p != "glc_token" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/loki/api/v1/push":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			mu.Lock()
			pushes = append(pushes, b)
			mu.Unlock()
			w.WriteHeader(204)
		case "/loki/api/v1/query_range":
			queried = r.URL.Query().Get("query")
			w.Write([]byte(`{"data":{"result":[{"stream":{"yawl_service":"web"},"values":[["1793376000000000000","older line"]]},` +
				`{"stream":{"yawl_service":"web","yawl_stream":"stderr"},"values":[["1793376001000000000","older error"]]}]}}`))
		}
	}))
	defer loki.Close()
	t0 := time.Now()
	rt := &runtime.Fake{LogLines: map[string][]runtime.LogLine{}, States: map[string]runtime.State{}}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	kr, seal := keyring(t)
	d.Keys = kr
	d.DrainFile = filepath.Join(t.TempDir(), "drain.json")
	res := d.Handle(context.Background(), cmd(t, proto.CmdLogDrain, proto.LogDrainArgs{URL: loki.URL, User: "123456", TokenBox: seal("glc_token"), Server: "hel1"}))
	if !res.OK {
		t.Fatal(res.Cause)
	}
	ctx := context.Background()
	id, _ := rt.Start(ctx, runtime.ContainerSpec{Name: "acme-production-web-1", Labels: map[string]string{"yawl.project": "acme", "yawl.env": "production", "yawl.service": "web", "yawl.deploy": "dep_1"}})
	rel, _ := rt.Start(ctx, runtime.ContainerSpec{Name: "release", Labels: map[string]string{"yawl.project": "acme", "yawl.env": "production", "yawl.service": "web", "yawl.deploy": "dep_1", "yawl.release": "1"}})
	rt.States[id] = runtime.State{Running: true, Env: []string{"API_KEY=sk_live_abcdef", "NODE_ENV=production", "PATH=/usr/local/bin:/usr/bin"}}
	rt.LogLines[id] = []runtime.LogLine{{Time: t0.Add(time.Second), Text: "started in production mode"}, {Time: t0.Add(2 * time.Second), Text: "charging with sk_live_abcdef", Stream: "stderr"}}
	rt.LogLines[rel] = []runtime.LogLine{{Time: t0.Add(time.Second), Text: "migrating"}}
	d.drainOnce(ctx, t0)
	d.drainOnce(ctx, t0)
	mu.Lock()
	got, _ := json.Marshal(pushes)
	mu.Unlock()
	if len(pushes) != 1 || !strings.Contains(string(got), `"yawl_project":"acme"`) || !strings.Contains(string(got), `"yawl_server":"hel1"`) ||
		!strings.Contains(string(got), "started in production mode") || !strings.Contains(string(got), "charging with ••••") ||
		strings.Contains(string(got), "sk_live") || strings.Contains(string(got), "migrating") ||
		!strings.Contains(string(got), `"yawl_stream":"stdout"},"values":[["`) || !strings.Contains(string(got), `"yawl_stream":"stderr"},"values":[["`) {
		t.Fatalf("%d %s", len(pushes), got)
	}
	res = d.Handle(ctx, cmd(t, proto.CmdLogs, proto.LogsArgs{Project: "acme", Env: "production", Service: "web", Query: `pay"x`, Drain: true, Since: t0.Add(-time.Hour)}))
	var out proto.LogsData
	json.Unmarshal(res.Data, &out)
	if !res.OK || len(out.Lines) != 2 || out.Lines[0].Text != "older line" || out.Lines[0].Stream != "" || out.Lines[1].Stream != "stderr" || !out.Drain ||
		queried != `{yawl_project="acme",yawl_env="production",yawl_service="web"} |= "pay\"x"` {
		t.Fatalf("%+v %+v %q", res, out, queried)
	}
	d.Handle(ctx, cmd(t, proto.CmdLogs, proto.LogsArgs{Project: "acme", Env: "production", Service: "web", Drain: true, Stream: "stderr"}))
	if queried != `{yawl_project="acme",yawl_env="production",yawl_service="web",yawl_stream="stderr"}` {
		t.Fatalf("%q", queried)
	}
	d.Handle(ctx, cmd(t, proto.CmdLogDrain, proto.LogDrainArgs{Off: true}))
	rt.LogLines[id] = append(rt.LogLines[id], runtime.LogLine{Time: t0.Add(3 * time.Second), Text: "later"})
	d.drainOnce(ctx, t0)
	if len(pushes) != 1 {
		t.Fatalf("sent while off: %d", len(pushes))
	}
}

func (d *Dispatcher) drainOnce(ctx context.Context, start time.Time) {
	var wg sync.WaitGroup
	d.superviseDrain(ctx, start, &wg)
	wg.Wait()
	d.flushDrain(ctx)
}

type inspectRT struct {
	*runtime.Fake
	during func()
	err    error
}

func (r *inspectRT) Inspect(ctx context.Context, id string) (runtime.State, error) {
	if r.during != nil {
		r.during()
	}
	if r.err != nil {
		return runtime.State{}, r.err
	}
	return r.Fake.Inspect(ctx, id)
}

func TestDrainReviewFixes(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	refuse := true
	loki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		if refuse {
			refuse = false
			http.Error(w, "entry too far behind", 400)
			return
		}
		bodies = append(bodies, string(b))
		w.WriteHeader(204)
	}))
	defer loki.Close()
	t0 := time.Now()
	fake := &runtime.Fake{LogLines: map[string][]runtime.LogLine{}, States: map[string]runtime.State{}}
	rt := &inspectRT{Fake: fake}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	ctx := context.Background()
	on := func() {
		if res := d.Handle(ctx, cmd(t, proto.CmdLogDrain, proto.LogDrainArgs{URL: loki.URL, Server: "hel1"})); !res.OK {
			t.Fatal(res.Cause)
		}
	}
	on()
	id, _ := fake.Start(ctx, runtime.ContainerSpec{Name: "web-1", Labels: map[string]string{"yawl.project": "acme", "yawl.env": "production", "yawl.service": "web", "yawl.deploy": "dep_1"}})
	fake.States[id] = runtime.State{Running: true, Env: []string{"API_KEY=sk_live_abcdef"}}
	fake.LogLines[id] = []runtime.LogLine{{Time: t0.Add(time.Second), Text: "paid with sk_live_abcdef"}}

	rt.during = func() { d.drain.set(nil) }
	d.drainOnce(ctx, t0)

	on()
	rt.during, rt.err = nil, errors.New("docker is busy")
	d.drainOnce(ctx, t0)
	mu.Lock()
	if len(bodies) != 0 {
		t.Fatalf("sent without its values masked: %v", bodies)
	}
	mu.Unlock()

	rt.err = nil
	d.drainOnce(ctx, t0)
	fake.LogLines[id] = append(fake.LogLines[id], runtime.LogLine{Time: t0.Add(2 * time.Second), Text: "next"})
	d.drainOnce(ctx, t0)
	mu.Lock()
	if len(bodies) != 1 || !strings.Contains(bodies[0], "next") || strings.Contains(bodies[0], "sk_live") {
		t.Fatalf("%v", bodies)
	}
	mu.Unlock()

	old := drainKeepBytes
	drainKeepBytes = 100
	defer func() { drainKeepBytes = old }()
	r := &d.drain
	for i := 0; i < 10; i++ {
		r.holdLocked(drainLine{text: strings.Repeat("x", 30)})
	}
	if r.bytes > 100 || len(r.buf) != 3 {
		t.Fatalf("%d bytes in %d lines", r.bytes, len(r.buf))
	}
}

type listRT struct {
	*runtime.Fake
	cs       []runtime.Container
	inspects int
}

func (r *listRT) List(context.Context, map[string]string) ([]runtime.Container, error) {
	return r.cs, nil
}

func (r *listRT) Inspect(ctx context.Context, id string) (runtime.State, error) {
	r.inspects++
	return r.Fake.Inspect(ctx, id)
}

func TestDrainMinors(t *testing.T) {
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	if res := d.Handle(context.Background(), cmd(t, proto.CmdLogDrain, proto.LogDrainArgs{URL: "https://1234:glc_tok@logs.example.com"})); res.OK || strings.Contains(res.Cause, "glc_tok") {
		t.Fatalf("user:token@ taken: %+v", res)
	}
	loki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer loki.Close()
	fake := &runtime.Fake{LogLines: map[string][]runtime.LogLine{}, States: map[string]runtime.State{"c1": {}}}
	labels := map[string]string{"yawl.project": "acme", "yawl.env": "production", "yawl.service": "web", "yawl.deploy": "dep_1"}
	rt := &listRT{Fake: fake, cs: []runtime.Container{{ID: "c1", Labels: labels}}}
	d = NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	ctx := context.Background()
	if res := d.Handle(ctx, cmd(t, proto.CmdLogDrain, proto.LogDrainArgs{URL: loki.URL, Server: "hel1"})); !res.OK {
		t.Fatal(res.Cause)
	}
	t0 := time.Now()
	d.drainOnce(ctx, t0)
	d.drainOnce(ctx, t0)
	if rt.inspects != 1 {
		t.Fatalf("a stopped container looked at %d times", rt.inspects)
	}
	rt.cs = nil
	d.drainOnce(ctx, t0)
	if len(d.drain.cursor) != 0 {
		t.Fatalf("cursor kept for a removed container: %v", d.drain.cursor)
	}
}
