package exec

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/behzodhalil/yawl/internal/proto"
)

func (d *Dispatcher) probe(ctx context.Context, a proto.ProbeArgs) proto.Result {
	return d.eachCopy(ctx, a.Container, func(id string) proto.Result {
		b := a
		b.Container = id
		return d.probeOne(ctx, b)
	})
}

func (d *Dispatcher) probeOne(ctx context.Context, a proto.ProbeArgs) proto.Result {
	st, err := d.rt.Inspect(ctx, a.Container)
	if err != nil {
		return proto.Result{Cause: "its container is gone: " + err.Error()}
	}
	if !st.Running {
		return proto.Result{Cause: fmt.Sprintf("the app is not running (exit code %d)", st.ExitCode)}
	}
	if st.Restarting {
		return proto.Result{Cause: fmt.Sprintf("it keeps restarting (exit code %d)", st.ExitCode)}
	}
	if a.Kind != "web" {
		return proto.Result{OK: true}
	}
	code, err := d.Get(fmt.Sprintf("http://%s%s", netip.AddrPortFrom(st.IP, uint16(a.Port)), a.Path))
	switch {
	case err != nil:
		return proto.Result{Cause: fmt.Sprintf("GET %s did not answer: %v", a.Path, err)}
	case code >= 500:
		return proto.Result{Cause: fmt.Sprintf("GET %s returned %d", a.Path, code)}
	}
	return proto.Result{OK: true}
}
