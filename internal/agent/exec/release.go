package exec

import (
	"context"
	"errors"
	"fmt"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func (d *Dispatcher) release(ctx context.Context, a proto.ReleaseArgs) proto.Result {
	env, cause := d.env(a.Vars)
	if cause != "" {
		return proto.Result{Cause: cause}
	}
	if a.Kind == "web" {
		env = append(env, "PORT="+strconv.Itoa(a.Port))
	}
	network := "yawl-" + a.Project + "-" + a.Env
	if err := d.rt.EnsureNetwork(ctx, network); err != nil {
		return proto.Result{Cause: err.Error()}
	}
	rctx, cancel := context.WithTimeout(ctx, a.Timeout)
	defer cancel()
	name := fmt.Sprintf("yawl-%s-%s-%s-release-%s", a.Project, a.Env, a.Service, a.Deploy)
	if _, err := d.rt.Inspect(ctx, name); err == nil {
		return d.adoptRelease(rctx, name, a.Timeout)
	}
	var mounts []runtime.Mount
	for _, v := range a.Volumes {
		vol, err := proto.VolumeName(a.Project, a.Env, a.Service, v.Name)
		if err != nil {
			return proto.Result{Cause: err.Error()}
		}
		mounts = append(mounts, runtime.Mount{Source: vol, Target: v.Path, Volume: true})
	}
	out := &tail{max: 4 << 10}
	if cause := yawlNetworks(a.Networks); cause != "" {
		return proto.Result{Cause: cause}
	}
	if cause := checkCPU(a.Service, a.CPU, goruntime.NumCPU()); cause != "" {
		return proto.Result{Cause: cause}
	}
	exit, stderr, err := d.rt.RunOnce(rctx, runtime.ContainerSpec{Name: name, Image: a.Image, Network: network, Env: env, Cmd: a.Cmd, Mounts: mounts, Networks: a.Networks, CPU: a.CPU, Memory: a.Memory,
		Labels: map[string]string{"yawl.project": a.Project, "yawl.env": a.Env, "yawl.service": a.Service, "yawl.release": "1"}}, nil, out)
	output := strings.TrimRight(out.String(), "\n")
	if stderr != "" {
		output = strings.TrimRight(output+"\n"+stderr, "\n")
	}
	res := proto.Result{Output: output}
	switch {
	case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
		res.Cause = fmt.Sprintf("Release command did not finish in %s.", a.Timeout)
	case err != nil:
		res.Cause = "release command: " + err.Error()
	case exit != 0:
		res.Cause = fmt.Sprintf("Release command exited with code %d: %s", exit, lastLine(output))
	default:
		res.OK = true
	}
	return res
}

func (d *Dispatcher) adoptRelease(ctx context.Context, name string, timeout time.Duration) proto.Result {
	defer d.rt.Remove(context.WithoutCancel(ctx), name)
	for {
		st, err := d.rt.Inspect(ctx, name)
		if err != nil {
			return proto.Result{Cause: "release command: " + err.Error()}
		}
		if !st.Running {
			if st.ExitCode != 0 {
				return proto.Result{Cause: fmt.Sprintf("Release command exited with code %d (in an earlier agent run; its output is gone).", st.ExitCode)}
			}
			return proto.Result{OK: true, Output: "The release command ran in an earlier agent run; its output is gone."}
		}
		select {
		case <-ctx.Done():
			return proto.Result{Cause: fmt.Sprintf("Release command did not finish in %s.", timeout)}
		case <-d.Clock.After(time.Second):
		}
	}
}

func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return "(no output)"
}

type tail struct {
	max int
	b   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tail) String() string { return string(t.b) }
