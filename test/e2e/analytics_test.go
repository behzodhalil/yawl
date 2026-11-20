//go:build e2e

package e2e

import (
	"net/http"
	"testing"
	"time"
)

func TestE2EAnalytics(t *testing.T) {
	b := setup(t)
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest("GET", "http://127.0.0.1/", nil)
		req.Host = host
		req.Header.Set("User-Agent", "Mozilla/5.0 e2e")
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
		}
	}
	waitFor(t, 4*time.Minute, "the requests in Analytics", func() bool {
		var v struct{ Requests, S2 int64 }
		b.api("GET", "/api/services/e2e/production/hello/analytics?range=24h", &v)
		return v.Requests >= 5 && v.S2 >= 5
	})
}
