package metrics

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/behzodhalil/yawl/internal/proto"
)

func Facts(hb *proto.Heartbeat, proc, etc string) {
	if b, err := os.ReadFile(filepath.Join(etc, "os-release")); err == nil {
		hb.OSName, hb.OSID = ParseOSRelease(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(proc, "sys/kernel/osrelease")); err == nil {
		hb.Kernel = strings.TrimSpace(string(b))
	}
	hb.Cores = runtime.NumCPU()
	if b, err := os.ReadFile(filepath.Join(proc, "uptime")); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			up, _ := strconv.ParseFloat(f[0], 64)
			hb.UptimeS = int64(up)
		}
	}
}

func ParseOSRelease(text string) (name, id string) {
	for _, l := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok {
			continue
		}
		if u, err := strconv.Unquote(v); err == nil {
			v = u
		} else {
			v = strings.Trim(v, `'"`)
		}
		switch k {
		case "PRETTY_NAME":
			name = v
		case "ID":
			id = v
		}
	}
	return name, id
}

func NetBytes(text string) (rx, tx uint64) {
	for _, l := range strings.Split(text, "\n") {
		name, rest, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if virtual(name) {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(f[0], 10, 64)
		t, _ := strconv.ParseUint(f[8], 10, 64)
		rx, tx = rx+r, tx+t
	}
	return rx, tx
}

func virtual(name string) bool {
	if name == "lo" {
		return true
	}
	for _, p := range []string{"docker", "veth", "br-", "wg", "tailscale"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

type Net struct {
	rx, tx uint64
	at     int64
}

func (n *Net) Rate(rx, tx uint64, at int64) (r, t *int64) {
	prev := *n
	*n = Net{rx: rx, tx: tx, at: at}
	if prev.at == 0 || at <= prev.at || rx < prev.rx || tx < prev.tx {
		return nil, nil
	}
	s := float64(at-prev.at) / 1000
	dr, dt := int64(float64(rx-prev.rx)/s), int64(float64(tx-prev.tx)/s)
	return &dr, &dt
}

func (n *Net) Forget() { *n = Net{} }
