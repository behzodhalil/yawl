package metrics

import (
	"slices"
	"sync"

	"github.com/behzodhalil/yawl/internal/proto"
)

type OOMCount struct {
	mu sync.Mutex
	n  map[[3]string]int
}

func (c *OOMCount) Add(project, env, service string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == nil {
		c.n = map[[3]string]int{}
	}
	c.n[[3]string{project, env, service}]++
}

func (c *OOMCount) Take() []proto.OOMSample {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []proto.OOMSample
	for k, n := range c.n {
		out = append(out, proto.OOMSample{Project: k[0], Env: k[1], Service: k[2], Kills: n})
	}
	c.n = nil
	slices.SortFunc(out, func(a, b proto.OOMSample) int {
		return slices.Compare([]string{a.Project, a.Env, a.Service}, []string{b.Project, b.Env, b.Service})
	})
	return out
}

func OOMService(labels map[string]string) (project, env, service string, ok bool) {
	if labels["yawl.service"] == "" || labels["yawl.release"] != "" || labels["yawl.cron"] != "" {
		return "", "", "", false
	}
	return labels["yawl.project"], labels["yawl.env"], labels["yawl.service"], true
}
