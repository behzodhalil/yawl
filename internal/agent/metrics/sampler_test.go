package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/analytics"
	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestSamplerVisits(t *testing.T) {
	c := analytics.New()
	at := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	c.Line(`{"logger":"http.log.access.yawl","request":{"client_ip":"1.1.1.1","method":"GET","host":"a.example","uri":"/","headers":{"User-Agent":["Mozilla"]}},"size":10,"status":200,"resp_headers":{"Content-Type":["text/html"]}}`, at)
	s := &Sampler{RT: &runtime.Fake{}, Proc: t.TempDir(), Disk: t.TempDir(), Visits: c}
	out := s.Sample(context.Background(), at)
	if len(out.Visits) != 1 || out.Visits[0].Host != "a.example" || out.Visits[0].Views != 1 {
		t.Fatalf("%+v", out.Visits)
	}
}

func TestSamplerLogHits(t *testing.T) {
	s := &Sampler{RT: &runtime.Fake{}, Proc: t.TempDir(), Disk: t.TempDir(), Logs: func() []proto.LogHit { return []proto.LogHit{{Rule: "la_1", Lines: 3, First: "x"}} }}
	if out := s.Sample(context.Background(), time.Now()); len(out.LogHits) != 1 || out.LogHits[0].Lines != 3 {
		t.Fatalf("%+v", out.LogHits)
	}
}
