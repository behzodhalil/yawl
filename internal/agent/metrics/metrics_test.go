package metrics

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"

	"github.com/behzodhalil/yawl/internal/proto"
)

func read(t *testing.T, name string) string {
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMachineFromProc(t *testing.T) {
	a, b := ParseStat(read(t, "stat1")), ParseStat(read(t, "stat2"))
	if got := CPUBusy(a, b); math.Abs(got-66.6667) > 0.01 {
		t.Fatalf("cpu %v", got)
	}
	used, total := ParseMeminfo(read(t, "meminfo"))
	if used != 3_072_000_000 || total != 4_096_000_000 {
		t.Fatalf("mem %d / %d", used, total)
	}
}

func TestCaddyHosts(t *testing.T) {
	c := &Caddy{}
	if hs := c.Hosts(read(t, "caddy1.txt")); hs != nil {
		t.Fatalf("first read has nothing to compare with: %+v", hs)
	}
	hs := c.Hosts(read(t, "caddy2.txt"))
	if len(hs) != 2 || hs[0] != (proto.HostSample{Host: "api.acme.uz", Req: 100, Err5: 5, P95ms: 250}) || !hs[1].Gap {
		t.Fatalf("%+v", hs)
	}
}

func TestCaddyCounterReset(t *testing.T) {
	c := &Caddy{}
	c.Hosts(read(t, "caddy1.txt"))
	for _, h := range c.Hosts(read(t, "caddy2.txt")) {
		if h.Host == "web.acme.uz" && (!h.Gap || h.Req != 0) {
			t.Fatalf("a reset host was counted: %+v", h)
		}
	}
}

func TestRingSince(t *testing.T) {
	r := &Ring{N: 60}
	for i := int64(1); i <= 70; i++ {
		r.Add(proto.Sample{At: i})
	}
	all := r.Since(0)
	if len(all) != 60 || all[0].At != 11 || all[59].At != 70 {
		t.Fatalf("%d, first %d", len(all), all[0].At)
	}
	if got := r.Since(65); len(got) != 5 || got[0].At != 66 {
		t.Fatalf("%+v", got)
	}
}

func TestSamplerReadsAll(t *testing.T) {
	dir := t.TempDir()
	for _, f := range [][2]string{{"stat", "stat1"}, {"meminfo", "meminfo"}} {
		os.WriteFile(dir+"/"+f[0], []byte(read(t, f[1])), 0o644)
	}
	os.WriteFile(dir+"/loadavg", []byte("0.52 0.40 0.30 1/200 999\n"), 0o644)
	rt := &runtime.Fake{Stat: map[string]runtime.Stats{"c1": {CPU: 1_000, System: 10_000, CPUs: 4, Mem: 300}}}
	rt.Start(context.Background(), runtime.ContainerSpec{Name: "yawl-acme-production-api-dep1", Labels: map[string]string{"yawl.project": "acme", "yawl.env": "production", "yawl.service": "api"}})
	rt.Start(context.Background(), runtime.ContainerSpec{Name: "yawl-caddy"})
	text := read(t, "caddy1.txt")
	s := &Sampler{RT: rt, Proc: dir, Disk: dir, Caddy: func(context.Context) (string, error) { return text, nil }}
	first := s.Sample(context.Background(), time.Unix(60, 0))
	if len(first.Containers) != 0 || first.HostsOK {
		t.Fatalf("a first sample has no base to measure CPU and requests from: %+v", first)
	}
	rt.Stat["c1"] = runtime.Stats{CPU: 1_500, System: 18_000, CPUs: 4, Mem: 300}
	os.WriteFile(dir+"/stat", []byte(read(t, "stat2")), 0o644)
	text = read(t, "caddy2.txt")
	got := s.Sample(context.Background(), time.Unix(120, 0))
	if got.At != 120_000 || math.Abs(got.Machine.CPU-66.6667) > 0.01 || got.Machine.Mem != 3_072_000_000 || got.Machine.Load != 0.52 || got.Machine.DiskTotal == 0 {
		t.Fatalf("machine %+v", got.Machine)
	}
	if len(got.Containers) != 1 || got.Containers[0] != (proto.ContainerSample{Project: "acme", Env: "production", Service: "api", CPU: 25, Mem: 300}) || !got.HostsOK {
		t.Fatalf("containers %+v", got.Containers)
	}
	if len(got.Hosts) != 2 || got.Hosts[0].Req != 100 {
		t.Fatalf("hosts %+v", got.Hosts)
	}
}

func TestCaddyTwoMethods(t *testing.T) {
	text := func(get, put int) string {
		return fmt.Sprintf("caddy_http_request_duration_seconds_count{code=\"200\",handler=\"reverse_proxy\",host=\"h\",method=\"GET\",server=\"http\"} %d\n"+
			"caddy_http_request_duration_seconds_count{code=\"200\",handler=\"reverse_proxy\",host=\"h\",method=\"PUT\",server=\"http\"} %d\n", get, put)
	}
	c := &Caddy{}
	c.Hosts(text(100, 2))
	hs := c.Hosts(text(1300, 3))
	if len(hs) != 1 || hs[0].Gap || hs[0].Req != 1201 {
		t.Fatalf("%+v", hs)
	}
}

func TestSamplerFirstCPU(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/stat", []byte(read(t, "stat1")), 0o644)
	s := &Sampler{RT: &runtime.Fake{}, Proc: dir, Disk: dir}
	s.primeCPU()
	os.WriteFile(dir+"/stat", []byte(read(t, "stat2")), 0o644)
	if got := s.Sample(context.Background(), time.Unix(60, 0)); math.Abs(got.Machine.CPU-66.6667) > 0.01 {
		t.Fatalf("machine %+v", got.Machine)
	}
}

func TestCaddyHTTPRedirectNotCounted(t *testing.T) {
	text := func(redirects, served int) string {
		return fmt.Sprintf("caddy_http_request_duration_seconds_count{code=\"308\",handler=\"static_response\",host=\"h\",method=\"GET\",server=\"http\"} %d\n"+
			"caddy_http_request_duration_seconds_count{code=\"200\",handler=\"reverse_proxy\",host=\"h\",method=\"GET\",server=\"https\"} %d\n", redirects, served)
	}
	c := &Caddy{}
	c.Hosts(text(10, 10))
	if hs := c.Hosts(text(15, 15)); len(hs) != 1 || hs[0].Req != 5 {
		t.Fatalf("%+v", hs)
	}
}
