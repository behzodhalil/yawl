package exec

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func probe(t *testing.T, st runtime.State, kind string, get Getter) proto.Result {
	t.Helper()
	rt := &runtime.Fake{States: map[string]runtime.State{"c1": st}}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Get = get
	return d.Handle(context.Background(), cmd(t, proto.CmdProbe, proto.ProbeArgs{Container: "c1", Kind: kind, Path: "/health", Port: 10000}))
}

func TestProbeWeb(t *testing.T) {
	up := runtime.State{Running: true, IP: netip.MustParseAddr("172.18.0.5")}
	var got string
	if res := probe(t, up, "web", func(u string) (int, error) { got = u; return 200, nil }); !res.OK || got != "http://172.18.0.5:10000/health" {
		t.Fatalf("%+v %s", res, got)
	}
	if res := probe(t, up, "web", func(string) (int, error) { return 500, nil }); res.OK || res.Cause != "GET /health returned 500" {
		t.Fatalf("%+v", res)
	}
	if res := probe(t, up, "web", func(string) (int, error) { return 0, errors.New("timeout") }); res.OK || res.Cause != "GET /health did not answer: timeout" {
		t.Fatalf("%+v", res)
	}
	if res := probe(t, runtime.State{ExitCode: 2}, "web", func(string) (int, error) { return 200, nil }); res.OK || res.Cause != "the app is not running (exit code 2)" {
		t.Fatalf("%+v", res)
	}
}

func TestProbeWorker(t *testing.T) {
	if res := probe(t, runtime.State{Running: true}, "worker", nil); !res.OK {
		t.Fatalf("%+v", res)
	}
	if res := probe(t, runtime.State{Running: true, Restarting: true, ExitCode: 1}, "worker", nil); res.OK || res.Cause != "it keeps restarting (exit code 1)" {
		t.Fatalf("%+v", res)
	}
	if res := probe(t, runtime.State{Running: true}, "postgres", nil); !res.OK {
		t.Fatalf("%+v", res)
	}
}
