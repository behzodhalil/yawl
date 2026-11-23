package exec

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

type Clock struct {
	Now   func() time.Time
	After func(time.Duration) <-chan time.Time
}

var RealClock = Clock{Now: time.Now, After: time.After}

type Getter func(url string) (int, error)

func HTTPGetter() Getter {
	c := &http.Client{Timeout: 5 * time.Second}
	return func(url string) (int, error) {
		res, err := c.Get(url)
		if err != nil {
			return 0, err
		}
		res.Body.Close()
		return res.StatusCode, nil
	}
}

const workerGrace = 20 * time.Second

func CheckHealth(ctx context.Context, rt runtime.Runtime, a proto.HealthArgs, get Getter, clk Clock) proto.Result {
	fail := func(cause string) proto.Result { return proto.Result{Cause: cause} }
	if a.Kind == "worker" {
		select {
		case <-ctx.Done():
			return fail(ctx.Err().Error())
		case <-clk.After(workerGrace):
		}
		st, err := rt.Inspect(ctx, a.Container)
		if err != nil {
			return fail(err.Error())
		}
		if crashed(st) {
			return fail(crashCause(ctx, rt, a.Container, st))
		}
		return proto.Result{OK: true}
	}

	deadline := clk.Now().Add(a.Timeout)
	for {
		st, err := rt.Inspect(ctx, a.Container)
		if err != nil {
			return fail(err.Error())
		}
		if crashed(st) {
			return fail(crashCause(ctx, rt, a.Container, st))
		}
		url := fmt.Sprintf("http://%s%s", netip.AddrPortFrom(st.IP, uint16(a.Port)), a.Path)
		code, err := get(url)
		if err == nil && code < 500 {
			return proto.Result{OK: true}
		}
		if cause := portCause(ctx, rt, a); cause != "" {
			return fail(cause)
		}
		if !clk.Now().Before(deadline) {
			return fail(fmt.Sprintf("Health check on %s did not pass within %ds.", a.Path, int(a.Timeout.Round(time.Second).Seconds())))
		}
		select {
		case <-ctx.Done():
			return fail(ctx.Err().Error())
		case <-clk.After(time.Second):
		}
	}
}

// crashed is true once the app has exited even once: Docker restarts it
// (unless-stopped), so "running" alone would hide a crash loop.
func crashed(st runtime.State) bool { return !st.Running || st.Restarting || st.Restarts > 0 }

var errLine = regexp.MustCompile(`(?i)(error|exception|fatal|panic)`)

func crashCause(ctx context.Context, rt runtime.Runtime, id string, st runtime.State) string {
	tail, _ := rt.Tail(ctx, id, 50)
	lines := strings.Split(strings.TrimSpace(tail), "\n")
	line := lines[len(lines)-1]
	for i := len(lines) - 1; i >= 0; i-- {
		if errLine.MatchString(lines[i]) {
			line = lines[i]
			break
		}
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "The app exited" + exitCodeSuffix(st.ExitCode) + "."
	}
	return line + exitCodeSuffix(st.ExitCode)
}

func exitCodeSuffix(code int) string {
	if code == 0 {
		return ""
	}
	return fmt.Sprintf(" (exit code %d)", code)
}

func portCause(ctx context.Context, rt runtime.Runtime, a proto.HealthArgs) string {
	ls, err := rt.Listening(ctx, a.Container)
	if err != nil || len(ls) == 0 {
		return ""
	}
	var other *runtime.Listen
	for i, l := range ls {
		if l.Port == a.Port && !l.Loopback() {
			return ""
		}
		if l.Port == a.Port && l.Loopback() {
			return fmt.Sprintf("Your app listens on %s:%d. Bind to 0.0.0.0.", l.Addr, l.Port)
		}
		if other == nil && !l.Loopback() {
			other = &ls[i]
		}
	}
	if other != nil {
		return fmt.Sprintf("Your app listens on %d, Yawl sent traffic to PORT=%d.", other.Port, a.Port)
	}
	return ""
}
