package runtime

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/moby/moby/client"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockDocker(t *testing.T, answer func(method, path string) (int, string)) (*Docker, *[]string) {
	var seen []string
	rt := roundTrip(func(r *http.Request) (*http.Response, error) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		code, body := answer(r.Method, r.URL.Path)
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"application/json"}}, Request: r}, nil
	})
	c, err := client.New(client.WithAPIVersion("1.44"), client.WithHost("tcp://docker.test:2375"), client.WithHTTPClient(&http.Client{Transport: rt}))
	if err != nil {
		t.Fatal(err)
	}
	return &Docker{C: c}, &seen
}

func TestPullFailsOnStreamError(t *testing.T) {
	d, seen := mockDocker(t, func(method, path string) (int, string) {
		switch {
		case strings.HasSuffix(path, "/images/create"):
			return 200, `{"status":"11: Pulling from grafana/grafana"}` + "\n" + `{"errorDetail":{"message":"write /var/lib/docker: no space left on device"},"error":"write /var/lib/docker: no space left on device"}` + "\n"
		case strings.HasSuffix(path, "/json"):
			return 200, `{"Id":"sha256:old","RepoDigests":["grafana/grafana@sha256:old"]}`
		}
		return 201, ""
	})
	_, err := d.Pull(context.Background(), "grafana/grafana:11", "yawl/acme-production-grafana:dep_2", "", "")
	if err == nil || !strings.Contains(err.Error(), "no space left on device") {
		t.Fatalf("err %v, calls %v", err, *seen)
	}
	for _, c := range *seen {
		if strings.Contains(c, "/tag") {
			t.Fatalf("tagged after a failed pull: %v", *seen)
		}
	}
}

func TestPullTagsByIDThenUntags(t *testing.T) {
	d, seen := mockDocker(t, func(method, path string) (int, string) {
		switch {
		case strings.HasSuffix(path, "/images/create"):
			return 200, `{"status":"Digest: sha256:new"}` + "\n"
		case method == "GET" && strings.HasSuffix(path, "/json"):
			return 200, `{"Id":"sha256:abc","RepoDigests":["grafana/grafana@sha256:new"]}`
		case method == "DELETE":
			return 200, `[{"Untagged":"grafana/grafana:11"}]`
		}
		return 201, ""
	})
	digest, err := d.Pull(context.Background(), "grafana/grafana:11", "yawl/acme-production-grafana:dep_2", "", "")
	if err != nil || digest != "sha256:new" {
		t.Fatalf("%q %v", digest, err)
	}
	calls := strings.Join(*seen, ", ")
	if !strings.Contains(calls, "POST /v1.44/images/sha256:abc/tag") || !strings.Contains(calls, "DELETE /v1.44/images/grafana/grafana:11") {
		t.Fatalf("%s", calls)
	}
}

func TestPullDigest(t *testing.T) {
	two := []string{"ghcr.io/acme/grafana@sha256:other", "grafana/grafana@sha256:mine"}
	cases := []struct {
		ref, pulled string
		rds         []string
		want        string
	}{
		{"grafana/grafana@sha256:pinned", "sha256:x", two, "sha256:pinned"},
		{"grafana/grafana:11", "sha256:pulled", two, "sha256:pulled"},
		{"grafana/grafana:11", "", two, "sha256:mine"},
		{"docker.io/library/nginx:1", "", []string{"acme/nginx@sha256:other", "nginx@sha256:mine"}, "sha256:mine"},
		{"acme/nginx:1", "", []string{"other/acme/nginx@sha256:other"}, ""},
	}
	for _, c := range cases {
		if got := pullDigest(c.ref, c.pulled, c.rds); got != c.want {
			t.Fatalf("%s: %q, want %q", c.ref, got, c.want)
		}
	}
}
