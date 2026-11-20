package runtime

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/behzodhalil/yawl/internal/spec"
	"github.com/moby/moby/api/types/registry"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

type Docker struct {
	C       *client.Client
	netMu   sync.Mutex // Docker allows two networks with one name; never race to create one
	ensured map[string]time.Time
}

func NewDocker() (*Docker, error) {
	c, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}
	return &Docker{C: c}, nil
}

func (d *Docker) EnsureNetwork(ctx context.Context, name string) error {
	d.netMu.Lock()
	defer d.netMu.Unlock()
	if d.ensured == nil {
		d.ensured = map[string]time.Time{}
	}
	d.ensured[name] = time.Now()
	if _, err := d.C.NetworkInspect(ctx, name, client.NetworkInspectOptions{}); err == nil {
		return nil
	}
	_, err := d.C.NetworkCreate(ctx, name, client.NetworkCreateOptions{Driver: "bridge"})
	if err != nil {
		if _, again := d.C.NetworkInspect(ctx, name, client.NetworkInspectOptions{}); again == nil {
			return nil
		}
	}
	return err
}

func (d *Docker) Start(ctx context.Context, s ContainerSpec) (string, error) {
	hc := &container.HostConfig{
		LogConfig:     container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "5"}},
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		Resources:     resources(s),
	}
	for _, m := range s.Mounts {
		t := mount.TypeBind
		if m.Volume {
			t = mount.TypeVolume
		}
		hc.Mounts = append(hc.Mounts, mount.Mount{Type: t, Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly})
	}
	cfg := &container.Config{Image: s.Image, Env: s.Env, Labels: s.Labels, Cmd: s.Cmd}
	for host, port := range s.Ports {
		p, err := network.ParsePort(fmt.Sprintf("%d/tcp", port))
		if err != nil {
			return "", err
		}
		if cfg.ExposedPorts == nil {
			cfg.ExposedPorts, hc.PortBindings = network.PortSet{}, network.PortMap{}
		}
		cfg.ExposedPorts[p] = struct{}{}
		b := network.PortBinding{HostPort: strconv.Itoa(host)}
		if ip, ok := s.PortIP[host]; ok {
			addr, err := netip.ParseAddr(ip)
			if err != nil {
				return "", fmt.Errorf("port %d: bind address %q: %w", host, ip, err)
			}
			b.HostIP = addr
		}
		hc.PortBindings[p] = append(hc.PortBindings[p], b)
	}
	nc := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{s.Network: {Aliases: s.Aliases}}}
	for _, n := range s.Networks {
		nc.EndpointsConfig[n] = &network.EndpointSettings{}
	}
	hc.CapAdd = s.CapAdd
	if s.HostNetwork {
		hc.NetworkMode = "host"
		nc = nil
	}
	if err := d.pullIfMissing(ctx, s.Image); err != nil {
		return "", err
	}
	res, err := d.C.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:             s.Name,
		Config:           cfg,
		HostConfig:       hc,
		NetworkingConfig: nc,
	})
	if err != nil {
		return "", err
	}
	if len(s.Files) > 0 {
		if _, err := d.C.CopyToContainer(ctx, res.ID, client.CopyToContainerOptions{DestinationPath: "/", Content: tarOf(s.Files)}); err != nil {
			return res.ID, fmt.Errorf("copy files into %s: %w", s.Name, err)
		}
	}
	if _, err := d.C.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
		return res.ID, err
	}
	return res.ID, nil
}

func tarOf(files []File) io.Reader {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	dirs := map[string]bool{}
	for _, f := range files {
		dir := strings.TrimPrefix(filepath.Dir(f.Path), "/")
		if !dirs[dir] {
			dirs[dir] = true
			tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: dir + "/", Mode: 0o700, Uid: f.UID, Gid: f.UID})
		}
		tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: strings.TrimPrefix(f.Path, "/"), Mode: f.Mode, Uid: f.UID, Gid: f.UID, Size: int64(len(f.Data))})
		tw.Write(f.Data)
	}
	tw.Close()
	return &buf
}

func (d *Docker) NetworkInfo(ctx context.Context, name string) (netip.Prefix, netip.Addr, error) {
	res, err := d.C.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if err != nil {
		return netip.Prefix{}, netip.Addr{}, err
	}
	for _, c := range res.Network.IPAM.Config {
		if c.Subnet.Addr().Is4() && c.Gateway.IsValid() {
			return c.Subnet, c.Gateway, nil
		}
	}
	return netip.Prefix{}, netip.Addr{}, fmt.Errorf("network %s has no IPv4 subnet", name)
}

func (d *Docker) Prepare(ctx context.Context, image string) error { return d.pullIfMissing(ctx, image) }

func (d *Docker) pullIfMissing(ctx context.Context, ref string) error {
	if _, err := d.C.ImageInspect(ctx, ref); err == nil {
		return nil
	}
	rc, err := d.C.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	defer rc.Close()
	_, err = io.Copy(io.Discard, rc)
	return err
}

func (d *Docker) Stop(ctx context.Context, id string, grace time.Duration) error {
	secs := int(grace.Seconds())
	_, err := d.C.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &secs})
	if cerrdefs.IsNotFound(err) {
		return fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	return err
}

func (d *Docker) Remove(ctx context.Context, id string) error {
	_, err := d.C.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	return err
}

func (d *Docker) Inspect(ctx context.Context, id string) (State, error) {
	res, err := d.C.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return State{}, err
	}
	c := res.Container
	st := State{Restarts: c.RestartCount, Name: strings.TrimPrefix(c.Name, "/")}
	if c.Config != nil {
		st.Labels = c.Config.Labels
		st.Env = c.Config.Env
	}
	if c.State != nil {
		st.Running, st.ExitCode, st.PID, st.Restarting = c.State.Running, c.State.ExitCode, c.State.Pid, c.State.Restarting
		st.StartedAt, _ = time.Parse(time.RFC3339Nano, c.State.StartedAt)
	}
	if c.NetworkSettings != nil {
		for _, ep := range c.NetworkSettings.Networks {
			if ep != nil && ep.IPAddress.IsValid() {
				st.IP = ep.IPAddress
				break
			}
		}
	}
	return st, nil
}

func (d *Docker) Tail(ctx context.Context, id string, lines int) (string, error) {
	rc, err := d.C.ContainerLogs(ctx, id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Tail: strconv.Itoa(lines)})
	if err != nil {
		return "", err
	}
	defer rc.Close()
	var out bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &out, rc); err != nil && !errors.Is(err, io.EOF) {
		return out.String(), err
	}
	return out.String(), nil
}

func (d *Docker) List(ctx context.Context, labels map[string]string) ([]Container, error) {
	f := make(client.Filters)
	for k, v := range labels {
		f = f.Add("label", k+"="+v)
	}
	res, err := d.C.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		return nil, err
	}
	var out []Container
	for _, c := range res.Items {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		out = append(out, Container{ID: c.ID, Name: name, Image: c.Image, Labels: c.Labels, Created: time.Unix(c.Created, 0), Running: c.State == container.StateRunning})
	}
	return out, nil
}

func (d *Docker) Logs(ctx context.Context, id string, since time.Time, fn func(LogLine)) error {
	return d.logs(ctx, id, since, false, fn)
}

func (d *Docker) Follow(ctx context.Context, id string, since time.Time, fn func(LogLine)) error {
	return d.logs(ctx, id, since, true, fn)
}

func (d *Docker) logs(ctx context.Context, id string, since time.Time, follow bool, fn func(LogLine)) error {
	o := client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Timestamps: true, Follow: follow}
	if !since.IsZero() {
		o.Since = fmt.Sprintf("%d.%09d", since.Unix(), since.Nanosecond())
	}
	rc, err := d.C.ContainerLogs(ctx, id, o)
	if err != nil {
		return err
	}
	defer rc.Close()
	return readLogs(rc, since, fn)
}

func readLogs(r io.Reader, since time.Time, fn func(LogLine)) error {
	emit := func(stream string) *lineWriter {
		return &lineWriter{max: 256 << 10, fn: func(line string) {
			ts, text, _ := strings.Cut(line, " ")
			t, err := time.Parse(time.RFC3339Nano, ts)
			if err != nil || !t.After(since) { // Docker's since is inclusive
				return
			}
			fn(LogLine{Time: t, Text: text, Stream: stream})
		}}
	}
	_, err := stdcopy.StdCopy(emit(""), emit("stderr"), r)
	return err
}

type lineWriter struct {
	line []byte
	max  int
	fn   func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		part, rest, nl := bytes.Cut(p, []byte("\n"))
		w.line = append(w.line, part[:min(len(part), max(w.max-len(w.line), 0))]...)
		if !nl {
			break
		}
		w.fn(string(bytes.TrimSuffix(w.line, []byte("\r"))))
		w.line, p = w.line[:0], rest
	}
	return n, nil
}

func (d *Docker) Listening(ctx context.Context, id string) ([]Listen, error) {
	st, err := d.Inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	if st.PID == 0 {
		return nil, errors.New("container is not running")
	}
	var all []Listen
	for _, f := range []string{"tcp", "tcp6"} {
		file, err := os.Open(fmt.Sprintf("/proc/%d/net/%s", st.PID, f))
		if err != nil {
			return nil, err
		}
		l, err := ParseProcNetTCP(file)
		file.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, l...)
	}
	return all, nil
}

func (d *Docker) PruneImages(ctx context.Context, repo string, keep []string) error {
	res, err := d.C.ImageList(ctx, client.ImageListOptions{Filters: make(client.Filters).Add("reference", repo)})
	if err != nil {
		return err
	}
	for _, im := range res.Items {
		for _, tag := range im.RepoTags {
			if strings.HasPrefix(tag, repo+":") && !slices.Contains(keep, tag) {
				// Removing by tag only untags an image other tags still name; one a
				// container still uses is refused by Docker, which is the right outcome.
				d.C.ImageRemove(ctx, tag, client.ImageRemoveOptions{PruneChildren: true})
			}
		}
	}
	return nil
}

func (d *Docker) RunOnce(ctx context.Context, s ContainerSpec, stdin io.Reader, stdout io.Writer) (int, string, error) {
	if err := d.pullIfMissing(ctx, s.Image); err != nil {
		return 0, "", err
	}
	// No log driver: a one-off's stdout is a database dump, which must never
	// land in plaintext in Docker's log files or the journal.
	hc := &container.HostConfig{LogConfig: container.LogConfig{Type: "none"}, Resources: resources(s)}
	for _, m := range s.Mounts {
		t := mount.TypeBind
		if m.Volume {
			t = mount.TypeVolume
		}
		hc.Mounts = append(hc.Mounts, mount.Mount{Type: t, Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly})
	}
	nc := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{s.Network: {Aliases: s.Aliases}}}
	for _, n := range s.Networks {
		nc.EndpointsConfig[n] = &network.EndpointSettings{}
	}
	if s.HostNetwork {
		hc.NetworkMode, nc = "host", nil
	} else if s.Network == "" {
		hc.NetworkMode, nc = "none", nil
	}
	res, err := d.C.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: s.Name,
		Config: &container.Config{Image: s.Image, Env: s.Env, Labels: s.Labels, Cmd: s.Cmd,
			OpenStdin: true, StdinOnce: true, AttachStdin: true, AttachStdout: true, AttachStderr: true},
		HostConfig:       hc,
		NetworkingConfig: nc,
	})
	if err != nil {
		return 0, "", err
	}
	defer d.C.ContainerRemove(context.WithoutCancel(ctx), res.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	att, err := d.C.ContainerAttach(ctx, res.ID, client.ContainerAttachOptions{Stream: true, Stdin: true, Stdout: true, Stderr: true})
	if err != nil {
		return 0, "", err
	}
	defer att.Close()
	wait := d.C.ContainerWait(ctx, res.ID, client.ContainerWaitOptions{Condition: container.WaitConditionNextExit})
	if _, err := d.C.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
		return 0, "", err
	}
	stdinErr := make(chan error, 1)
	go func() {
		if stdin != nil {
			if _, err := io.Copy(att.Conn, stdin); err != nil {
				// Input that broke off is no end of input: the program is stopped
				// before it reads an EOF and finishes half the data (Plan 45 review C1).
				stdinErr <- err
				d.C.ContainerRemove(context.WithoutCancel(ctx), res.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
				return
			}
		}
		if cw, ok := att.Conn.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
	}()
	cut := func() error {
		select {
		case err := <-stdinErr:
			return fmt.Errorf("reading the input: %w", err)
		default:
			return nil
		}
	}
	errBuf := &tailBuffer{max: 4 << 10}
	if stdout == nil {
		stdout = io.Discard
	}
	copyErr := make(chan error, 1)
	go func() { _, err := stdcopy.StdCopy(stdout, errBuf, att.Reader); copyErr <- err }()
	for {
		select {
		case err := <-copyErr:
			if err == nil || errors.Is(err, io.EOF) {
				copyErr = nil
				continue
			}
			// Nobody reads stdout any more, so the container would block on a
			// full pipe forever, holding whatever it holds (pg_dump: locks).
			d.C.ContainerRemove(context.WithoutCancel(ctx), res.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
			return 0, errBuf.String(), err
		case r := <-wait.Result:
			if copyErr != nil {
				if err := <-copyErr; err != nil && !errors.Is(err, io.EOF) {
					return 0, errBuf.String(), err
				}
			}
			if err := cut(); err != nil {
				return int(r.StatusCode), errBuf.String(), err
			}
			if r.Error != nil && r.Error.Message != "" {
				return int(r.StatusCode), errBuf.String(), errors.New(r.Error.Message)
			}
			return int(r.StatusCode), errBuf.String(), nil
		case err := <-wait.Error:
			att.Close()
			if copyErr != nil {
				<-copyErr
			}
			if cerr := cut(); cerr != nil {
				return 0, errBuf.String(), cerr
			}
			return 0, errBuf.String(), err
		}
	}
}

type tailBuffer struct {
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if over := len(t.b) - t.max; over > 0 {
		t.b = t.b[over:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.b) }

func (d *Docker) VolumeExists(ctx context.Context, name string) (bool, error) {
	_, err := d.C.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (d *Docker) Stats(ctx context.Context, id string) (Stats, error) {
	res, err := d.C.ContainerStats(ctx, id, client.ContainerStatsOptions{}) // one-shot: no second sample a second later
	if err != nil {
		return Stats{}, err
	}
	defer res.Body.Close()
	var st container.StatsResponse
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		return Stats{}, err
	}
	return statsOf(st), nil
}

func statsOf(st container.StatsResponse) Stats {
	n := int(st.CPUStats.OnlineCPUs)
	if n == 0 {
		n = len(st.CPUStats.CPUUsage.PercpuUsage)
	}
	cache := st.MemoryStats.Stats["inactive_file"]
	if cache == 0 {
		cache = st.MemoryStats.Stats["total_inactive_file"]
	}
	mem := uint64(0)
	if st.MemoryStats.Usage > cache { // the page cache left out, as `docker stats` does
		mem = st.MemoryStats.Usage - cache
	}
	return Stats{CPU: st.CPUStats.CPUUsage.TotalUsage, System: st.CPUStats.SystemUsage, CPUs: n, Mem: mem}
}

// RemoveVolume removes a volume and the stopped containers that still name it
// (an old version is stopped, not removed, and Docker refuses the volume while
// any container references it). A running one is refused.
func (d *Docker) RemoveVolume(ctx context.Context, name string) error {
	res, err := d.C.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: make(client.Filters).Add("volume", name)})
	if err != nil {
		return err
	}
	for _, c := range res.Items {
		if c.State == "running" || c.State == "restarting" {
			return fmt.Errorf("volume %s is in use by a running container (%s)", name, strings.TrimPrefix(strings.Join(c.Names, ","), "/"))
		}
	}
	for _, c := range res.Items {
		if _, err := d.C.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
			return err
		}
	}
	_, err = d.C.VolumeRemove(ctx, name, client.VolumeRemoveOptions{Force: true})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// resources are a container's limits (design record 23). Memory is also the
// swap limit (L4): an app at its limit is killed and restarted, not slowed.
func resources(s ContainerSpec) container.Resources {
	r := container.Resources{NanoCPUs: int64(s.CPU * 1e9)}
	for _, d := range s.Devices {
		r.Devices = append(r.Devices, container.DeviceMapping{PathOnHost: d, PathInContainer: d, CgroupPermissions: "rwm"})
	}
	if s.Memory > 0 {
		r.Memory, r.MemorySwap = s.Memory, s.Memory
	}
	return r
}

func (d *Docker) WatchOOM(ctx context.Context, on func(labels map[string]string)) {
	f := make(client.Filters).Add("type", "container").Add("event", "oom")
	for ctx.Err() == nil {
		res := d.C.Events(ctx, client.EventsListOptions{Filters: f})
	read:
		for {
			select {
			case m := <-res.Messages:
				on(m.Actor.Attributes)
			case <-res.Err:
				break read
			case <-ctx.Done():
				return
			}
		}
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
	}
}

func (d *Docker) PushAndUntag(ctx context.Context, src, ref, user, token string) error {
	if _, err := d.C.ImageTag(ctx, client.ImageTagOptions{Source: src, Target: ref}); err != nil {
		return err
	}
	defer d.C.ImageRemove(ctx, src, client.ImageRemoveOptions{})
	defer d.C.ImageRemove(ctx, ref, client.ImageRemoveOptions{})
	opts := client.ImagePushOptions{RegistryAuth: base64.URLEncoding.EncodeToString([]byte("{}"))} // Docker wants one, even empty
	if user != "" {
		b, _ := json.Marshal(registry.AuthConfig{Username: user, Password: token, ServerAddress: spec.RegistryHost(ref)})
		opts.RegistryAuth = base64.URLEncoding.EncodeToString(b)
	}
	rc, err := d.C.ImagePush(ctx, ref, opts)
	if err != nil {
		return err
	}
	defer rc.Close()
	return rc.Wait(ctx) // a refusal arrives inside the 200 stream, as with a pull
}

func (d *Docker) Pull(ctx context.Context, ref, tag, user, token string) (string, error) {
	opts := client.ImagePullOptions{}
	if user != "" {
		b, _ := json.Marshal(registry.AuthConfig{Username: user, Password: token, ServerAddress: spec.RegistryHost(ref)})
		opts.RegistryAuth = base64.URLEncoding.EncodeToString(b)
	}
	rc, err := d.C.ImagePull(ctx, ref, opts)
	if err != nil {
		return "", err
	}
	pulled := ""
	for m, err := range rc.JSONMessages(ctx) {
		switch {
		case err != nil:
			return "", err
		case m.Error != nil: // a failure mid-pull arrives inside a 200 stream (review C1)
			return "", m.Error
		}
		if dg, ok := strings.CutPrefix(m.Status, "Digest: "); ok {
			pulled = dg
		}
	}
	img, err := d.C.ImageInspect(ctx, ref)
	if err != nil {
		return "", err
	}
	if _, err := d.C.ImageTag(ctx, client.ImageTagOptions{Source: img.ID, Target: tag}); err != nil {
		return "", err
	}
	d.C.ImageRemove(ctx, ref, client.ImageRemoveOptions{})
	return pullDigest(ref, pulled, img.RepoDigests), nil
}

// pullDigest is the digest to record for a pull of ref (review M1, plan 22):
// a pinned ref's own; else the one the pull reported; else the RepoDigest of
// exactly ref's repository, as one image can carry several.
func pullDigest(ref, pulled string, repoDigests []string) string {
	if _, dg, ok := strings.Cut(ref, "@"); ok {
		return dg
	}
	if pulled != "" {
		return pulled
	}
	name := familiar(spec.ImageName(ref))
	for _, rd := range repoDigests {
		if n, dg, _ := strings.Cut(rd, "@"); familiar(n) == name {
			return dg
		}
	}
	return ""
}

func familiar(n string) string {
	n = strings.TrimPrefix(strings.TrimPrefix(n, "docker.io/"), "index.docker.io/")
	return strings.TrimPrefix(n, "library/")
}

func (d *Docker) Exec(ctx context.Context, container string, cmd []string, cols, rows uint) (Shell, error) {
	size := client.ConsoleSize{Height: rows, Width: cols}
	ex, err := d.C.ExecCreate(ctx, container, client.ExecCreateOptions{TTY: true, AttachStdin: true, AttachStdout: true, AttachStderr: true,
		ConsoleSize: size, Cmd: cmd, Env: []string{"TERM=xterm-256color"}})
	if err != nil {
		return nil, err
	}
	at, err := d.C.ExecAttach(ctx, ex.ID, client.ExecAttachOptions{TTY: true, ConsoleSize: size})
	if err != nil {
		return nil, err
	}
	return &dockerShell{c: d.C, id: ex.ID, h: at.HijackedResponse}, nil
}

type dockerShell struct {
	c  *client.Client
	id string
	h  client.HijackedResponse
}

func (s *dockerShell) Read(p []byte) (int, error)  { return s.h.Reader.Read(p) }
func (s *dockerShell) Write(p []byte) (int, error) { return s.h.Conn.Write(p) }

// Close ends the shell and everything it started: Docker has no call to stop
// an exec, and closing the stream alone leaves the shell running (review I1).
func (s *dockerShell) Close() error {
	s.h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, err := s.c.ExecInspect(ctx, s.id, client.ExecInspectOptions{})
	if err != nil || !in.Running || in.PID <= 1 {
		return nil
	}
	signalSession(in.PID, syscall.SIGHUP) // a shell's polite end: it hangs up its jobs
	time.AfterFunc(time.Second, func() { signalSession(in.PID, syscall.SIGKILL) })
	return nil
}

// signalSession signals every process in the session sid leads; a TTY exec
// is its own session's leader. It reads /proc, so it runs no program (P6).
func signalSession(sid int, sig syscall.Signal) {
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		// pid (comm) state ppid pgrp session ...: comm may hold spaces, so count from the last ')'
		f := strings.Fields(string(b[bytes.LastIndexByte(b, ')')+1:]))
		if len(f) > 3 && f[3] == strconv.Itoa(sid) {
			syscall.Kill(pid, sig)
		}
	}
}
func (s *dockerShell) Resize(ctx context.Context, cols, rows uint) error {
	_, err := s.c.ExecResize(ctx, s.id, client.ExecResizeOptions{Height: rows, Width: cols})
	return err
}

func (d *Docker) Cleanup(ctx context.Context, o CleanupOpts) (CleanupResult, error) {
	out, err := d.pruneStopped(ctx, o.StoppedFor)
	if err != nil {
		return out, err
	}
	im, err := d.C.ImagePrune(ctx, client.ImagePruneOptions{Filters: make(client.Filters).Add("dangling", "true")})
	if err != nil {
		return out, fmt.Errorf("images: %w", err)
	}
	out.Images, out.Freed = len(im.Report.ImagesDeleted), out.Freed+int64(im.Report.SpaceReclaimed)
	bo := client.BuildCachePruneOptions{All: o.AllCache}
	if !o.AllCache {
		bo.Filters = make(client.Filters).Add("until", o.CacheFor.String())
	}
	bc, err := d.C.BuildCachePrune(ctx, bo)
	if err != nil {
		return out, fmt.Errorf("build cache: %w", err)
	}
	out.Freed += int64(bc.Report.SpaceReclaimed)
	if err := d.pruneNetworks(ctx, time.Now().Add(-time.Hour)); err != nil {
		return out, fmt.Errorf("networks: %w", err)
	}
	return out, nil
}

func (d *Docker) pruneNetworks(ctx context.Context, cutoff time.Time) error {
	d.netMu.Lock()
	defer d.netMu.Unlock()
	cs, err := d.C.ContainerList(ctx, client.ContainerListOptions{All: true}) // Docker removes a network a stopped container is on
	if err != nil {
		return err
	}
	used := map[string]bool{}
	for _, c := range cs.Items {
		if c.NetworkSettings != nil {
			for n := range c.NetworkSettings.Networks {
				used[n] = true
			}
		}
	}
	ns, err := d.C.NetworkList(ctx, client.NetworkListOptions{Filters: make(client.Filters).Add("name", "yawl-")})
	if err != nil {
		return err
	}
	for _, n := range ns.Items {
		if !strings.HasPrefix(n.Name, "yawl-") || used[n.Name] || n.Created.After(cutoff) || d.ensured[n.Name].After(cutoff) {
			continue
		}
		d.C.NetworkRemove(ctx, n.ID, client.NetworkRemoveOptions{}) // one a container joined meanwhile stays: Docker refuses while it runs
	}
	return nil
}

// pruneStopped removes stopped containers carrying yawl.project and created
// longer ago than age (Docker's until counts from creation): Yawl's own,
// never another's on the machine.
func (d *Docker) pruneStopped(ctx context.Context, age time.Duration) (CleanupResult, error) {
	cs, err := d.C.ContainerPrune(ctx, client.ContainerPruneOptions{Filters: make(client.Filters).Add("label", "yawl.project").Add("until", age.String())})
	if err != nil {
		return CleanupResult{}, fmt.Errorf("containers: %w", err)
	}
	return CleanupResult{Containers: len(cs.Report.ContainersDeleted), Freed: int64(cs.Report.SpaceReclaimed)}, nil
}

func (d *Docker) UnusedVolumes(ctx context.Context) ([]string, error) {
	res, err := d.C.VolumeList(ctx, client.VolumeListOptions{Filters: make(client.Filters).Add("dangling", "true")})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range res.Items {
		if strings.HasPrefix(v.Name, "yawl-") || strings.HasPrefix(v.Name, "yawl_") {
			out = append(out, v.Name)
		}
	}
	slices.Sort(out)
	return out, nil
}
