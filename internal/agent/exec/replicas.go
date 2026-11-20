package exec

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"sync"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func (d *Dispatcher) group(ctx context.Context, id string) ([]string, error) {
	st, err := d.rt.Inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	l := st.Labels
	if l["yawl.deploy"] == "" || l["yawl.replica"] == "" {
		return []string{id}, nil
	}
	cs, err := d.rt.List(ctx, map[string]string{"yawl.project": l["yawl.project"], "yawl.env": l["yawl.env"], "yawl.service": l["yawl.service"], "yawl.deploy": l["yawl.deploy"]})
	if err != nil {
		return nil, err
	}
	type copy struct {
		n  int
		id string
	}
	var out []copy
	for _, c := range cs {
		n, err := strconv.Atoi(c.Labels["yawl.replica"])
		if err != nil || c.Labels["yawl.release"] != "" {
			continue
		}
		if _, err := d.rt.Inspect(ctx, c.ID); err != nil {
			continue
		}
		out = append(out, copy{n, c.ID})
	}
	if len(out) == 0 {
		return []string{id}, nil
	}
	slices.SortFunc(out, func(a, b copy) int { return a.n - b.n })
	ids := make([]string, len(out))
	for i, c := range out {
		ids[i] = c.id
	}
	return ids, nil
}

func (d *Dispatcher) eachCopy(ctx context.Context, id string, fn func(id string) proto.Result) proto.Result {
	ids, err := d.group(ctx, id)
	if err != nil || len(ids) == 1 {
		return fn(id)
	}
	name := func(i int, res proto.Result) proto.Result {
		res.Cause = fmt.Sprintf("copy %d: %s", i+1, res.Cause)
		return res
	}
	results := make([]proto.Result, len(ids))
	var wg sync.WaitGroup
	for i := 1; i < len(ids); i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results[i] = fn(ids[i]) }()
	}
	wg.Wait()
	for i := 1; i < len(ids); i++ {
		if !results[i].OK {
			return name(i, results[i])
		}
	}
	if res := fn(ids[0]); !res.OK {
		return name(0, res)
	}
	return proto.Result{OK: true}
}

func (d *Dispatcher) upstreams(ctx context.Context, id string, port int) ([]string, error) {
	ids, err := d.group(ctx, id)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range ids {
		st, err := d.rt.Inspect(ctx, c)
		if err != nil {
			return nil, err
		}
		if !st.Running || !st.IP.IsValid() {
			continue // review yawl-1: a copy Docker is restarting has no address, even the only one
		}
		out = append(out, netip.AddrPortFrom(st.IP, uint16(port)).String())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no copy of %s has an address", id)
	}
	return out, nil
}

func (d *Dispatcher) startCopies(ctx context.Context, spec runtime.ContainerSpec, n int) (string, error) {
	var ids []string
	for k := 1; k <= max(n, 1); k++ {
		s := spec
		s.Labels = withLabel(spec.Labels, "yawl.replica", strconv.Itoa(k))
		if k > 1 {
			s.Name = fmt.Sprintf("%s-%d", spec.Name, k)
		}
		id, err := d.rt.Start(ctx, s)
		if err != nil {
			if id != "" {
				ids = append(ids, id)
			}
			for _, c := range ids {
				d.rt.Stop(ctx, c, stopGrace)
				d.rt.Remove(ctx, c)
			}
			return "", err
		}
		ids = append(ids, id)
	}
	return ids[0], nil
}

func withLabel(m map[string]string, k, v string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for a, b := range m {
		out[a] = b
	}
	out[k] = v
	return out
}
