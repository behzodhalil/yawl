package metrics

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/analytics"
	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

type Sampler struct {
	RT     runtime.Runtime
	Caddy  func(ctx context.Context) (string, error)
	Proc   string
	Disk   string
	OOM    *OOMCount
	Visits *analytics.Counter
	Logs   func() []proto.LogHit

	cpu   CPU
	net   Net
	caddy Caddy
	prev  map[string]runtime.Stats
}

func (s *Sampler) Sample(ctx context.Context, at time.Time) proto.Sample {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	out := proto.Sample{At: at.Truncate(time.Minute).UnixMilli()}
	if s.Visits != nil {
		out.Visits = s.Visits.Take(at)
	}
	if s.OOM != nil {
		out.OOMs = s.OOM.Take()
	}
	if s.Logs != nil {
		out.LogHits = s.Logs()
	}
	if b, err := os.ReadFile(filepath.Join(s.Proc, "stat")); err == nil {
		c := ParseStat(string(b))
		if s.cpu.Total > 0 {
			out.Machine.CPU = CPUBusy(s.cpu, c)
		}
		s.cpu = c
	}
	if b, err := os.ReadFile(filepath.Join(s.Proc, "meminfo")); err == nil {
		out.Machine.Mem, out.Machine.MemTotal = ParseMeminfo(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(s.Proc, "loadavg")); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			out.Machine.Load, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	if b, err := os.ReadFile(filepath.Join(s.Proc, "net/dev")); err == nil {
		rx, tx := NetBytes(string(b))
		out.Machine.NetRx, out.Machine.NetTx = s.net.Rate(rx, tx, at.UnixMilli())
	} else {
		s.net.Forget()
	}
	var st syscall.Statfs_t
	if syscall.Statfs(s.Disk, &st) == nil {
		bs := uint64(st.Bsize)
		out.Machine.DiskTotal = st.Blocks * bs
		out.Machine.Disk = (st.Blocks - st.Bavail) * bs
	}
	if cs, err := s.RT.List(ctx, nil); err == nil {
		seen := map[string]runtime.Stats{}
		for _, c := range cs {
			svc := c.Labels["yawl.service"]
			if svc == "" {
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			st, err := s.RT.Stats(cctx, c.ID)
			cancel()
			if err != nil {
				continue
			}
			seen[c.ID] = st
			p, ok := s.prev[c.ID]
			if !ok || st.System <= p.System || st.CPU < p.CPU {
				continue
			}
			cpu := float64(st.CPU-p.CPU) / float64(st.System-p.System) * float64(max(st.CPUs, 1)) * 100
			out.Containers = append(out.Containers, proto.ContainerSample{Project: c.Labels["yawl.project"], Env: c.Labels["yawl.env"],
				Service: svc, CPU: cpu, Mem: st.Mem})
		}
		s.prev = seen
	}
	if s.Caddy != nil {
		if text, err := s.Caddy(ctx); err == nil {
			primed := s.caddy.Primed()
			out.Hosts = s.caddy.Hosts(text)
			out.HostsOK = primed
		} else {
			s.caddy.Forget()
		}
	}
	return out
}

func (s *Sampler) Loop(ctx context.Context, r *Ring) {
	s.primeCPU()
	for {
		now := time.Now()
		next := now.Truncate(time.Minute).Add(time.Minute)
		select {
		case <-ctx.Done():
			return
		case <-time.After(next.Sub(now)):
		}
		r.Add(s.Sample(ctx, next))
	}
}

func (s *Sampler) primeCPU() {
	if b, err := os.ReadFile(filepath.Join(s.Proc, "stat")); err == nil {
		s.cpu = ParseStat(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(s.Proc, "net/dev")); err == nil {
		rx, tx := NetBytes(string(b))
		s.net.Rate(rx, tx, time.Now().UnixMilli())
	}
}
