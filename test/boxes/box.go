//go:build boxes

package boxes

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

const (
	netName   = "yawl-boxes"
	seaweedfs = "chrislusf/seaweedfs@sha256:4e61d15fd35994cb1e43e1e553dff106794841fd9a99ade2fc8c8bfce4d7872d"
)

var image = func() string {
	df, _ := os.ReadFile("Dockerfile")
	sum := sha256.Sum256(df)
	return "yawl-box:" + hex.EncodeToString(sum[:])[:12]
}()

var (
	minMemGB    int64 = 6
	subnet            = netip.MustParsePrefix("172.30.0.0/24")
	names             = []string{"box-one", "box-two", "box-s3"}
	runDeadline time.Time
)

type Box struct {
	Name, IP string
	c        *client.Client
}

func docker(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func memShort(ctx context.Context, c *client.Client) string {
	info, err := c.Info(ctx, client.InfoOptions{})
	if err != nil {
		return err.Error()
	}
	if info.Info.MemTotal >= minMemGB<<30 {
		return ""
	}
	return fmt.Sprintf("The boxes need %d GB in Docker Desktop's VM; this one has %.1f GB.", minMemGB, float64(info.Info.MemTotal)/(1<<30))
}

func ensureImage(t *testing.T, ctx context.Context, c *client.Client) {
	t.Helper()
	if _, err := c.ImageInspect(ctx, image); err == nil {
		return
	}
	df, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	var ctxTar bytes.Buffer
	tw := tar.NewWriter(&ctxTar)
	tw.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0o644, Size: int64(len(df))})
	tw.Write(df)
	tw.Close()
	res, err := c.ImageBuild(ctx, &ctxTar, client.ImageBuildOptions{Tags: []string{image}, Remove: true})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	dec := json.NewDecoder(res.Body)
	for {
		var m struct{ Error string }
		if err := dec.Decode(&m); err == io.EOF {
			return
		} else if err != nil {
			t.Fatalf("building %s: %v", image, err)
		}
		if m.Error != "" {
			t.Fatalf("building %s: %s", image, m.Error)
		}
	}
}

func clean(ctx context.Context, c *client.Client) {
	for _, n := range names {
		c.ContainerRemove(ctx, n, client.ContainerRemoveOptions{Force: true})
	}
	c.NetworkRemove(ctx, netName, client.NetworkRemoveOptions{})
}

func Up(t *testing.T, ctx context.Context) (one, two *Box, s3 string) {
	t.Helper()
	c := docker(t)
	if why := memShort(ctx, c); why != "" {
		t.Skip(why)
	}
	ensureImage(t, ctx, c)
	clean(ctx, c)
	t.Cleanup(func() {
		if os.Getenv("YAWL_BOX_KEEP") != "" && t.Failed() {
			t.Log("YAWL_BOX_KEEP: the boxes stay up")
			return
		}
		clean(context.WithoutCancel(ctx), c)
	})
	if _, err := c.NetworkCreate(ctx, netName, client.NetworkCreateOptions{Driver: "bridge",
		IPAM: &network.IPAM{Config: []network.IPAMConfig{{Subnet: subnet}}}}); err != nil {
		t.Fatal(err)
	}
	start := func(name, ip string, cfg *container.Config, hc *container.HostConfig) {
		nc := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			netName: {IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr(ip)}}}}
		pull(t, ctx, c, cfg.Image)
		if _, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{Name: name, Config: cfg, HostConfig: hc, NetworkingConfig: nc}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.ContainerStart(ctx, name, client.ContainerStartOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	boxes := []*Box{{Name: "box-one", IP: "172.30.0.11", c: c}, {Name: "box-two", IP: "172.30.0.12", c: c}}
	for _, b := range boxes {
		start(b.Name, b.IP, &container.Config{Image: image, Hostname: strings.TrimPrefix(b.Name, "box-")}, &container.HostConfig{
			Privileged: true, CgroupnsMode: container.CgroupnsModeHost,
			Tmpfs: map[string]string{"/run": "", "/run/lock": ""},
			// containerd keeps Docker 29's images: on the box's own overlay root it cannot mount overlay
			Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: "yawl-" + b.Name + "-docker", Target: "/var/lib/docker"},
				{Type: mount.TypeVolume, Source: "yawl-" + b.Name + "-containerd", Target: "/var/lib/containerd"}},
		})
	}
	start("box-s3", "172.30.0.20", &container.Config{Image: seaweedfs, Cmd: []string{"server", "-dir=/data", "-s3", "-s3.port=9000"},
		Env: []string{"AWS_ACCESS_KEY_ID=yawltest", "AWS_SECRET_ACCESS_KEY=yawltest-secret"}}, &container.HostConfig{})
	for _, b := range boxes {
		b.boot(t)
		b.Reset(t)
	}
	return boxes[0], boxes[1], "172.30.0.20"
}

func pull(t *testing.T, ctx context.Context, c *client.Client, ref string) {
	if _, err := c.ImageInspect(ctx, ref); err == nil {
		return
	}
	rc, err := c.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, rc)
	rc.Close()
}

func (b *Box) boot(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, out := b.exec(ctx, "systemctl is-system-running --wait", nil)
	if s := strings.TrimSpace(out); s != "running" && s != "degraded" {
		if ctx.Err() != nil {
			s = "no answer in 90s"
		}
		t.Fatalf("systemd did not reach running in %s: %s", b.Name, s)
	}
	for {
		if code, _ := b.exec(ctx, "docker info >/dev/null 2>&1", nil); code == 0 {
			return
		}
		if ctx.Err() != nil {
			t.Fatalf("docker did not start in %s", b.Name)
		}
		time.Sleep(time.Second)
	}
}

func (b *Box) exec(ctx context.Context, cmd string, stdin io.Reader) (int, string) {
	ex, err := b.c.ExecCreate(ctx, b.Name, client.ExecCreateOptions{Cmd: []string{"sh", "-c", cmd},
		AttachStdin: stdin != nil, AttachStdout: true, AttachStderr: true})
	if err != nil {
		return -1, err.Error()
	}
	att, err := b.c.ExecAttach(ctx, ex.ID, client.ExecAttachOptions{})
	if err != nil {
		return -1, err.Error()
	}
	defer att.Close()
	if stdin != nil {
		go func() {
			io.Copy(att.Conn, stdin)
			att.CloseWrite()
		}()
	}
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { _, err := stdcopy.StdCopy(&out, &out, att.Reader); done <- err }()
	select {
	case <-done:
	case <-ctx.Done():
		att.Close()
		<-done
		return -1, out.String() + "\n" + ctx.Err().Error()
	}
	in, err := b.c.ExecInspect(ctx, ex.ID, client.ExecInspectOptions{})
	if err != nil {
		return -1, err.Error()
	}
	return in.ExitCode, out.String()
}

func (b *Box) Try(cmd string) (int, string) {
	limit := 15 * time.Minute
	if !runDeadline.IsZero() {
		limit = min(limit, time.Until(runDeadline))
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	return b.exec(ctx, cmd, nil)
}

func (b *Box) Run(t *testing.T, cmd string, stdin io.Reader) string {
	t.Helper()
	limit := 15 * time.Minute
	if !runDeadline.IsZero() {
		limit = min(limit, time.Until(runDeadline))
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	code, out := b.exec(ctx, cmd, stdin)
	if code != 0 {
		t.Fatalf("%s: %s: exit %d\n%s", b.Name, cmd, code, out)
	}
	return out
}

func (b *Box) Put(t *testing.T, path string, mode int64, data []byte) {
	t.Helper()
	b.Run(t, "mkdir -p "+path[:strings.LastIndex(path, "/")+1], nil)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: strings.TrimPrefix(path, "/"), Mode: mode, Size: int64(len(data))})
	tw.Write(data)
	tw.Close()
	if _, err := b.c.CopyToContainer(context.Background(), b.Name, client.CopyToContainerOptions{DestinationPath: "/", Content: &buf}); err != nil {
		t.Fatal(err)
	}
}

func (b *Box) API(t *testing.T, method, path string, in, out any) int {
	t.Helper()
	var body io.Reader
	data := ""
	if in != nil {
		j, _ := json.Marshal(in)
		body, data = bytes.NewReader(j), "-H 'Content-Type: application/json' --data-binary @- "
	}
	cmd := "f=$(mktemp); code=$(curl -s -o $f -w '%{http_code}' -b /root/yawl.jar -c /root/yawl.jar -H 'X-Yawl-Request: 1' " + data +
		"-X " + method + " 'http://127.0.0.1:7000" + path + "'); echo $code; cat $f; rm -f $f"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, res := b.exec(ctx, cmd, body)
	head, raw, _ := strings.Cut(res, "\n")
	code, _ := strconv.Atoi(strings.TrimSpace(head))
	if out != nil {
		json.Unmarshal([]byte(raw), out)
	}
	if code/100 != 2 {
		t.Logf("%s %s: %d %s", method, path, code, raw)
	}
	return code
}

func (b *Box) Reset(t *testing.T) {
	t.Helper()
	b.Run(t, "docker rm -f $(docker ps -aq) >/dev/null 2>&1; docker volume rm -f $(docker volume ls -q) >/dev/null 2>&1; docker network prune -f >/dev/null; true", nil)
}
