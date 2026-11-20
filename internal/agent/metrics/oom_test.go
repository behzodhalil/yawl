package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestOOMCountTake(t *testing.T) {
	var c OOMCount
	c.Add("acme", "production", "web")
	c.Add("acme", "production", "api")
	c.Add("acme", "production", "api")
	got := c.Take()
	want := []proto.OOMSample{{Project: "acme", Env: "production", Service: "api", Kills: 2}, {Project: "acme", Env: "production", Service: "web", Kills: 1}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("%+v", got)
	}
	if again := c.Take(); len(again) != 0 {
		t.Fatalf("second take %+v", again)
	}
}

func TestSamplerTakesOOMs(t *testing.T) {
	c := &OOMCount{}
	c.Add("acme", "production", "api")
	s := &Sampler{RT: &runtime.Fake{}, Proc: t.TempDir(), Disk: t.TempDir(), OOM: c}
	if got := s.Sample(context.Background(), time.Now()).OOMs; len(got) != 1 || got[0].Kills != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestOOMService(t *testing.T) {
	if p, e, s, ok := OOMService(map[string]string{"yawl.project": "acme", "yawl.env": "production", "yawl.service": "api"}); !ok || p+e+s != "acmeproductionapi" {
		t.Fatal(p, e, s, ok)
	}
	for _, l := range []map[string]string{
		{"yawl.service": "api", "yawl.release": "1"},
		{"yawl.service": "jobs", "yawl.cron": "1"},
		{"name": "something-else"},
	} {
		if _, _, _, ok := OOMService(l); ok {
			t.Errorf("%v counted", l)
		}
	}
}
