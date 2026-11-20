package runtime

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

func TestCleanupRemovesUnusedNetworks(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	nets := `[{"Name":"yawl-cells-churn1","Id":"n1","Created":"` + old + `"},{"Name":"yawl-acme-staging","Id":"n2","Created":"` + old + `"},
		{"Name":"yawl-acme-pr-9","Id":"n3","Created":"` + time.Now().Format(time.RFC3339) + `"},{"Name":"yawl-acme-cron","Id":"n4","Created":"` + old + `"},
		{"Name":"bridge","Id":"n5","Created":"` + old + `"},{"Name":"mine-yawl-x","Id":"n6","Created":"` + old + `"}]`
	var removed []string
	rt := roundTrip(func(r *http.Request) (*http.Response, error) {
		body := "{}"
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/containers/json"):
			body = "[]"
			if r.URL.Query().Get("all") == "1" {
				body = `[{"Id":"c1","State":"exited","NetworkSettings":{"Networks":{"yawl-acme-staging":{}}}}]`
			}
		case strings.HasSuffix(p, "/networks") && r.Method == "GET":
			body = nets
		case r.Method == "DELETE":
			removed = append(removed, p[strings.LastIndex(p, "/")+1:])
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"application/json"}}, Request: r}, nil
	})
	c, err := client.New(client.WithAPIVersion("1.44"), client.WithHost("tcp://docker.test:2375"), client.WithHTTPClient(&http.Client{Transport: rt}))
	if err != nil {
		t.Fatal(err)
	}
	d := &Docker{C: c}
	if err := d.EnsureNetwork(context.Background(), "yawl-acme-cron"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Cleanup(context.Background(), CleanupOpts{StoppedFor: time.Hour, CacheFor: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(removed, []string{"n1"}) {
		t.Fatalf("removed %q", removed)
	}
}
