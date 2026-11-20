//go:build e2e

package e2e

import (
	"testing"
	"time"
)

func TestE2EMetrics(t *testing.T) {
	b := setup(t)
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	l := startLoad(20)
	time.Sleep(3 * time.Minute)
	l.end()
	type pts struct {
		Points []struct {
			Req  int64 `json:"req"`
			CPU  float64
			Disk int64 `json:"disk"`
		} `json:"points"`
	}
	waitFor(t, 2*time.Minute, "two busy minutes on the graph", func() bool {
		var v pts
		b.api("GET", "/api/services/e2e/production/hello/metrics?range=1h", &v)
		busy := 0
		for _, p := range v.Points {
			if p.Req >= 1000 {
				busy++
			}
		}
		return busy >= 2
	})
	var m pts
	b.api("GET", "/api/servers/e2e/metrics?range=1h", &m)
	if len(m.Points) == 0 || m.Points[len(m.Points)-1].Disk == 0 {
		t.Fatalf("machine rows: %+v", m.Points)
	}
}
