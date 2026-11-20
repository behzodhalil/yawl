package runtime

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

type ContainerSpec struct {
	Name, Image, Network string
	Env                  []string
	Labels               map[string]string
	Cmd                  []string
	HostNetwork          bool
	Mounts               []Mount
	Aliases              []string
	Networks             []string
	PortIP               map[int]string
	Ports                map[int]int
	Files                []File
	CPU                  float64
	CapAdd               []string
	Devices              []string
	Memory               int64
}

type File struct {
	Path string
	Mode int64
	UID  int
	Data []byte
}

type Mount struct {
	Source, Target string
	Volume         bool
	ReadOnly       bool
}

type State struct {
	Running    bool
	Restarting bool
	Restarts   int
	ExitCode   int
	PID        int
	IP         netip.Addr
	StartedAt  time.Time
	Labels     map[string]string
	Name       string // without Docker's leading slash
	Env        []string
}

type Listen struct {
	Addr netip.Addr
	Port int
}

func (l Listen) Loopback() bool { return l.Addr.IsLoopback() }

type Runtime interface {
	EnsureNetwork(ctx context.Context, name string) error
	Start(ctx context.Context, s ContainerSpec) (id string, err error)
	Stop(ctx context.Context, id string, grace time.Duration) error
	Remove(ctx context.Context, id string) error
	Inspect(ctx context.Context, id string) (State, error)
	Tail(ctx context.Context, id string, lines int) (string, error)
	Listening(ctx context.Context, id string) ([]Listen, error)
	Pull(ctx context.Context, ref, tag, user, token string) (digest string, err error)
	PruneImages(ctx context.Context, repo string, keep []string) error
	VolumeExists(ctx context.Context, name string) (bool, error)
	RemoveVolume(ctx context.Context, name string) error
	RunOnce(ctx context.Context, s ContainerSpec, stdin io.Reader, stdout io.Writer) (exit int, stderr string, err error)
	NetworkInfo(ctx context.Context, name string) (netip.Prefix, netip.Addr, error)
	List(ctx context.Context, labels map[string]string) ([]Container, error)
	Stats(ctx context.Context, id string) (Stats, error)
	Logs(ctx context.Context, id string, since time.Time, fn func(LogLine)) error
	Prepare(ctx context.Context, image string) error
	PushAndUntag(ctx context.Context, src, ref, user, token string) error
	Follow(ctx context.Context, id string, since time.Time, fn func(LogLine)) error
	Exec(ctx context.Context, container string, cmd []string, cols, rows uint) (Shell, error)
	Cleanup(ctx context.Context, o CleanupOpts) (CleanupResult, error)
	UnusedVolumes(ctx context.Context) ([]string, error)
}

type Stats struct {
	CPU, System uint64
	CPUs        int
	Mem         uint64
}

var ErrNotFound = errors.New("no such container")

type Container struct {
	ID, Name, Image string
	Labels          map[string]string
	Created         time.Time
	Running         bool
}

type LogLine struct {
	Time   time.Time
	Text   string
	Stream string
}

// ParseProcNetTCP returns the LISTEN sockets (state 0A) in a /proc/net/tcp or tcp6 file.
func ParseProcNetTCP(r io.Reader) ([]Listen, error) {
	var out []Listen
	sc := bufio.NewScanner(r)
	sc.Scan()
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[3] != "0A" {
			continue
		}
		host, port, ok := strings.Cut(f[1], ":")
		if !ok {
			return nil, fmt.Errorf("bad address %q", f[1])
		}
		p, err := strconv.ParseUint(port, 16, 16)
		if err != nil {
			return nil, err
		}
		raw, err := hex.DecodeString(host)
		if err != nil || (len(raw) != 4 && len(raw) != 16) {
			return nil, fmt.Errorf("bad address %q", f[1])
		}
		// The kernel prints each 32-bit word in host (little-endian) order.
		for i := 0; i < len(raw); i += 4 {
			raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
		}
		addr, _ := netip.AddrFromSlice(raw)
		out = append(out, Listen{Addr: addr, Port: int(p)})
	}
	return out, sc.Err()
}

var (
	_ Runtime = (*Docker)(nil)
	_ Runtime = (*Fake)(nil)
)

type Shell interface {
	io.ReadWriteCloser
	Resize(ctx context.Context, cols, rows uint) error
}

type CleanupOpts struct {
	StoppedFor, CacheFor time.Duration
	AllCache             bool
}

type CleanupResult struct {
	Containers, Images int
	Freed              int64
}
