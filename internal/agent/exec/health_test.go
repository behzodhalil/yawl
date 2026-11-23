package exec

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func fakeClock() Clock {
	now := time.Unix(0, 0)
	return Clock{
		Now: func() time.Time { return now },
		After: func(d time.Duration) <-chan time.Time {
			now = now.Add(d)
			c := make(chan time.Time, 1)
			c <- now
			return c
		},
	}
}

func started(t *testing.T, rt *runtime.Fake) string {
	t.Helper()
	id, err := rt.Start(context.Background(), runtime.ContainerSpec{Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := rt.Inspect(context.Background(), id)
	st.IP = netip.MustParseAddr("172.18.0.5")
	rt.Set(id, st)
	return id
}

func web(id string) proto.HealthArgs {
	return proto.HealthArgs{Container: id, Kind: "web", Path: "/health", Port: 10000, Timeout: 60 * time.Second}
}

func TestHealthPasses(t *testing.T) {
	rt := &runtime.Fake{}
	id := started(t, rt)
	calls := 0
	get := func(url string) (int, error) {
		calls++
		if url != "http://172.18.0.5:10000/health" {
			t.Fatalf("polled %s", url)
		}
		if calls < 3 {
			return 0, errors.New("connection refused")
		}
		return 200, nil
	}
	if res := CheckHealth(context.Background(), rt, web(id), get, fakeClock()); !res.OK {
		t.Fatalf("want OK, got %+v", res)
	}
}

func TestCauseCrash(t *testing.T) {
	rt := &runtime.Fake{}
	id := started(t, rt)
	rt.Set(id, runtime.State{Running: false, ExitCode: 1})
	rt.Tails = map[string]string{id: "Traceback (most recent call last):\nModuleNotFoundError: No module named 'jinja2'\n"}
	res := CheckHealth(context.Background(), rt, web(id), func(string) (int, error) { return 0, errors.New("refused") }, fakeClock())
	want := "ModuleNotFoundError: No module named 'jinja2' (exit code 1)"
	if res.OK || res.Cause != want {
		t.Fatalf("cause %q, want %q", res.Cause, want)
	}
}

func TestCauseCrashOmitsExitCodeZero(t *testing.T) {
	rt := &runtime.Fake{}
	id := started(t, rt)
	rt.Set(id, runtime.State{Running: false, ExitCode: 0})
	rt.Tails = map[string]string{id: "Caused by: java.lang.IllegalStateException: Required environment variable JWT_SECRET is not set\n"}
	res := CheckHealth(context.Background(), rt, web(id), func(string) (int, error) { return 0, errors.New("refused") }, fakeClock())
	want := "Caused by: java.lang.IllegalStateException: Required environment variable JWT_SECRET is not set"
	if res.OK || res.Cause != want {
		t.Fatalf("cause %q, want %q", res.Cause, want)
	}
}

func TestCauseLocalhostBind(t *testing.T) {
	rt := &runtime.Fake{}
	id := started(t, rt)
	rt.Listens = map[string][]runtime.Listen{id: {{Addr: netip.MustParseAddr("127.0.0.1"), Port: 10000}}}
	res := CheckHealth(context.Background(), rt, web(id), func(string) (int, error) { return 0, errors.New("refused") }, fakeClock())
	if want := "Your app listens on 127.0.0.1:10000. Bind to 0.0.0.0."; res.Cause != want {
		t.Fatalf("cause %q, want %q", res.Cause, want)
	}
}

func TestCauseOtherPort(t *testing.T) {
	rt := &runtime.Fake{}
	id := started(t, rt)
	rt.Listens = map[string][]runtime.Listen{id: {{Addr: netip.MustParseAddr("0.0.0.0"), Port: 8080}}}
	res := CheckHealth(context.Background(), rt, web(id), func(string) (int, error) { return 0, errors.New("refused") }, fakeClock())
	if want := "Your app listens on 8080, Yawl sent traffic to PORT=10000."; res.Cause != want {
		t.Fatalf("cause %q, want %q", res.Cause, want)
	}
}

func TestCauseTimeout(t *testing.T) {
	rt := &runtime.Fake{}
	id := started(t, rt)
	res := CheckHealth(context.Background(), rt, web(id), func(string) (int, error) { return 503, nil }, fakeClock())
	if want := "Health check on /health did not pass within 60s."; res.Cause != want {
		t.Fatalf("cause %q, want %q", res.Cause, want)
	}
}

func TestWorkerHealthy(t *testing.T) {
	rt := &runtime.Fake{}
	id := started(t, rt)
	res := CheckHealth(context.Background(), rt, proto.HealthArgs{Container: id, Kind: "worker"}, nil, fakeClock())
	if !res.OK {
		t.Fatalf("want OK, got %+v", res)
	}
}

func TestCauseCrashWhileRestarting(t *testing.T) {
	rt := &runtime.Fake{}
	id := started(t, rt)
	rt.Set(id, runtime.State{Running: true, Restarting: true, Restarts: 2, ExitCode: 1, IP: netip.MustParseAddr("172.18.0.5")})
	rt.Tails = map[string]string{id: "ModuleNotFoundError: No module named 'jinja2'\n"}
	res := CheckHealth(context.Background(), rt, web(id), func(string) (int, error) { return 0, errors.New("refused") }, fakeClock())
	if want := "ModuleNotFoundError: No module named 'jinja2' (exit code 1)"; res.Cause != want {
		t.Fatalf("cause %q, want %q", res.Cause, want)
	}
}
