//go:build boxes

package boxes

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

func TestBoxBoots(t *testing.T) {
	ctx := context.Background()
	one, _, _ := Up(t, ctx)
	if s := strings.TrimSpace(one.Run(t, "systemctl is-system-running || true", nil)); s != "running" && s != "degraded" {
		t.Fatalf("systemd in box-one: %q", s)
	}
	if v := strings.TrimSpace(one.Run(t, "docker info --format '{{.ServerVersion}}'", nil)); v == "" {
		t.Fatal("docker in box-one does not answer")
	}
}

func TestBoxesStartClean(t *testing.T) {
	ctx := context.Background()
	c := docker(t)
	ensureImage(t, ctx, c)
	clean(ctx, c)
	if _, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{Name: "box-one",
		Config: &container.Config{Image: image, Cmd: []string{"sleep", "600"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.NetworkCreate(ctx, netName, client.NetworkCreateOptions{Driver: "bridge"}); err != nil {
		t.Fatal(err)
	}
	one, two, s3 := Up(t, ctx)
	if one.IP != "172.30.0.11" || two.IP != "172.30.0.12" || s3 != "172.30.0.20" {
		t.Fatalf("addresses %s %s %s", one.IP, two.IP, s3)
	}
}

func TestBoxesSkipSmallVM(t *testing.T) {
	ctx := context.Background()
	c := docker(t)
	info, err := c.Info(ctx, client.InfoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func(v int64) { minMemGB = v }(minMemGB)
	minMemGB = 1000
	want := fmt.Sprintf("The boxes need 1000 GB in Docker Desktop's VM; this one has %.1f GB.", float64(info.Info.MemTotal)/(1<<30))
	if got := memShort(ctx, c); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	minMemGB = 1
	if got := memShort(ctx, c); got != "" {
		t.Fatalf("a big enough VM is refused: %q", got)
	}
}
