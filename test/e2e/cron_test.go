//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dockerclient "github.com/moby/moby/client"

	"github.com/behzodhalil/yawl/internal/cli"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestE2ECron(t *testing.T) {
	b := setup(t)
	os.WriteFile(filepath.Join(b.repoDir, "yawl.yaml"), []byte(fmt.Sprintf(`project: e2e
environment: production
server: e2e
services:
  - name: hello
    kind: web
    repo: file://%s
    branch: master
    drain_timeout: 3s
    health_timeout: 30s
  - name: report
    kind: cron
    repo: file://%s
    branch: master
    schedule: "* * * * *"
    timezone: UTC
    command: [sh, -c, "echo hello from cron; exit 3"]
    timeout: 5m
`, b.repoDir, b.repoDir)), 0o644)
	ctx := context.Background()
	if err := cli.Run(ctx, "apply", []string{"--yes"}); err != nil {
		t.Fatal(err)
	}
	if err := cli.Run(ctx, "deploy", []string{"report"}); err != nil {
		t.Fatal(err)
	}
	const runs = "/api/services/e2e/production/report/runs"
	var v struct {
		Runs []proto.CronRun `json:"runs"`
	}
	finished := func() int {
		v.Runs = nil
		b.api("GET", runs, &v)
		n := 0
		for _, r := range v.Runs {
			if r.FinishedAt > 0 && !r.Skipped {
				n++
			}
		}
		return n
	}
	waitFor(t, 75*time.Second, "a scheduled run", func() bool { return finished() >= 1 })
	r := v.Runs[0]
	for _, x := range v.Runs {
		if x.FinishedAt > 0 && !x.Skipped {
			r = x
			break
		}
	}
	if r.Exit != 3 || !strings.HasSuffix(r.Output, "hello from cron") || r.ScheduledAt%60_000 != 0 {
		t.Fatalf("run %+v", r)
	}
	waitFor(t, 75*time.Second, "the failure on Telegram", func() bool {
		for _, m := range b.telegram.all() {
			if strings.HasPrefix(m, "e2e/production/report failed at ") && strings.Contains(m, "(UTC): exit 3 after ") {
				return true
			}
		}
		return false
	})
	before := finished()
	if code := b.api("POST", runs, nil); code != 202 && code != 409 {
		t.Fatalf("run now: %d", code)
	}
	waitFor(t, 75*time.Second, "the run now", func() bool { return finished() > before })
	c, err := dockerclient.New(dockerclient.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "no cron container left", func() bool {
		res, err := c.ContainerList(ctx, dockerclient.ContainerListOptions{All: true, Filters: make(dockerclient.Filters).Add("label", "yawl.cron=1")})
		return err == nil && len(res.Items) == 0
	})
}
