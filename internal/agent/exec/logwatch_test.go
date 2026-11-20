package exec

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func (d *Dispatcher) watchOnce(ctx context.Context, start time.Time) {
	var wg sync.WaitGroup
	d.superviseLogWatch(ctx, start, &wg)
	wg.Wait()
}

func hitsText(hs []proto.LogHit) string {
	var out []string
	for _, h := range hs {
		out = append(out, fmt.Sprintf("%s:%d:%s", h.Rule, h.Lines, h.First))
	}
	return strings.Join(out, "|")
}

func TestLogWatch(t *testing.T) {
	ctx := context.Background()
	rt := &runtime.Fake{LogLines: map[string][]runtime.LogLine{}, States: map[string]runtime.State{}}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	app := func(svc string, extra ...string) map[string]string {
		l := map[string]string{"yawl.project": "acme", "yawl.env": "production", "yawl.service": svc, "yawl.deploy": "dep_1"}
		for i := 0; i+1 < len(extra); i += 2 {
			l[extra[i]] = extra[i+1]
		}
		return l
	}
	api, _ := rt.Start(ctx, runtime.ContainerSpec{Name: "api", Labels: app("api")})
	web, _ := rt.Start(ctx, runtime.ContainerSpec{Name: "web", Labels: app("web")})
	rel, _ := rt.Start(ctx, runtime.ContainerSpec{Name: "rel", Labels: app("api", "yawl.release", "1")})
	rt.States[api] = runtime.State{Running: true, Env: []string{"API_KEY=sk_live_abcdef", "NODE_ENV=production"}}
	rt.States[web] = runtime.State{Running: true}
	rt.States[rel] = runtime.State{Running: true}
	t0 := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	rt.LogLines[api] = []runtime.LogLine{
		{Time: t0.Add(-time.Second), Text: "before the rule", Stream: "stderr"},
		{Time: t0.Add(time.Second), Text: "listening"},
		{Time: t0.Add(2 * time.Second), Text: "panic: bad key sk_live_abcdef " + strings.Repeat("x", 400), Stream: "stderr"},
		{Time: t0.Add(3 * time.Second), Text: "upstream TIMEOUT"},
		{Time: t0.Add(4 * time.Second), Text: "again", Stream: "stderr"},
	}
	rt.LogLines[web] = []runtime.LogLine{{Time: t0.Add(time.Second), Text: "web error", Stream: "stderr"}}
	rt.LogLines[rel] = []runtime.LogLine{{Time: t0.Add(time.Second), Text: "migration error", Stream: "stderr"}}

	res := d.Handle(ctx, cmd(t, proto.CmdMetrics, proto.MetricsArgs{LogWatch: []proto.LogWatch{
		{Rule: "la_err", Project: "acme", Env: "production", Service: "api"},
		{Rule: "la_time", Project: "acme", Env: "production", Service: "api", Text: "Timeout"},
	}}))
	if !res.OK {
		t.Fatal(res.Cause)
	}
	d.watchOnce(ctx, t0)
	hits := d.TakeLogHits()
	first := "panic: bad key •••• " + strings.Repeat("x", 300-len("panic: bad key •••• "))
	if got := hitsText(hits); got != "la_err:2:"+first+"|la_time:1:upstream TIMEOUT" {
		t.Fatalf("%s", got)
	}
	if d.TakeLogHits() != nil {
		t.Fatal("taken twice")
	}
	rt.LogLines[api] = append(rt.LogLines[api], runtime.LogLine{Time: t0.Add(5 * time.Second), Text: "later", Stream: "stderr"})
	d.watchOnce(ctx, t0)
	if got := hitsText(d.TakeLogHits()); got != "la_err:1:later" {
		t.Fatalf("%s", got)
	}
	d.Handle(ctx, cmd(t, proto.CmdMetrics, proto.MetricsArgs{}))
	rt.LogLines[api] = append(rt.LogLines[api], runtime.LogLine{Time: t0.Add(6 * time.Second), Text: "unwatched", Stream: "stderr"})
	d.watchOnce(ctx, t0)
	if hits := d.TakeLogHits(); hits != nil {
		t.Fatalf("%+v", hits)
	}
}
