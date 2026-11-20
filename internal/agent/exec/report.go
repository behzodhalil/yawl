package exec

import (
	"context"
	"slices"
	"strings"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func (d *Dispatcher) report(ctx context.Context) proto.Result {
	cs, err := d.rt.List(ctx, nil)
	if err != nil {
		return proto.Result{Cause: err.Error()}
	}
	newest := map[[3]string]runtime.Container{}
	for _, c := range cs {
		l := c.Labels
		if l["yawl.deploy"] == "" || l["yawl.release"] != "" || (l["yawl.replica"] != "" && l["yawl.replica"] != "1") {
			continue
		}
		if st, err := d.rt.Inspect(ctx, c.Name); err != nil || !st.Running {
			continue
		}
		k := [3]string{l["yawl.project"], l["yawl.env"], l["yawl.service"]}
		if n, seen := newest[k]; !seen || c.Created.After(n.Created) {
			newest[k] = c
		}
	}
	out := proto.ReportData{Running: []proto.Running{}}
	for k, c := range newest {
		out.Running = append(out.Running, proto.Running{Project: k[0], Env: k[1], Service: k[2], Deploy: c.Labels["yawl.deploy"], Image: c.Image, Container: c.Name})
	}
	slices.SortFunc(out.Running, func(a, b proto.Running) int { return strings.Compare(a.Container, b.Container) })
	return ok(out)
}
