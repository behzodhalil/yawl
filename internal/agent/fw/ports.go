package fw

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/behzodhalil/yawl/internal/proto"
)

func Listening(proc string) ([]proto.FwPort, error) {
	owners := socketOwners(proc)
	var out []proto.FwPort
	for _, f := range []struct{ file, proto, state string }{
		{"tcp", "tcp", "0A"}, {"tcp6", "tcp", "0A"}, {"udp", "udp", "07"}, {"udp6", "udp", "07"},
	} {
		file, err := os.Open(filepath.Join(proc, "net", f.file))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(file)
		sc.Scan()
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 10 || fields[3] != f.state || !strings.HasSuffix(fields[2], ":0000") {
				continue
			}
			addr, port, ok := hexAddr(fields[1])
			if !ok {
				continue
			}
			out = append(out, proto.FwPort{Proto: f.proto, Port: port, Address: addr.String(), Process: owners[fields[9]]})
		}
		file.Close()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Proto != out[j].Proto {
			return out[i].Proto < out[j].Proto
		}
		return out[i].Port < out[j].Port
	})
	return out, nil
}

// hexAddr reads /proc/net's "0100007F:1B58": the address in host order, 32
// bits at a time, then the port.
func hexAddr(s string) (netip.Addr, int, bool) {
	a, p, ok := strings.Cut(s, ":")
	raw, err := hex.DecodeString(a)
	port, err2 := strconv.ParseUint(p, 16, 16)
	if !ok || err != nil || err2 != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.Addr{}, 0, false
	}
	for i := 0; i < len(raw); i += 4 {
		binary.BigEndian.PutUint32(raw[i:], binary.LittleEndian.Uint32(raw[i:]))
	}
	addr, _ := netip.AddrFromSlice(raw)
	return addr.Unmap(), int(port), true
}

func socketOwners(proc string) map[string]string {
	out := map[string]string{}
	fds, _ := filepath.Glob(filepath.Join(proc, "[0-9]*", "fd", "*"))
	for _, fd := range fds {
		link, err := os.Readlink(fd)
		if err != nil || !strings.HasPrefix(link, "socket:[") {
			continue
		}
		inode := strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")
		if _, done := out[inode]; done {
			continue
		}
		comm, _ := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(fd)), "comm"))
		out[inode] = strings.TrimSpace(string(comm))
	}
	return out
}

func Classify(ports []proto.FwPort, a proto.FirewallArgs, sshPorts []int) []proto.FwPort {
	open := []proto.FwRule{{Proto: "tcp", Port: 80}, {Proto: "tcp", Port: 443}, {Proto: "udp", Port: 443}}
	for _, p := range sshPorts {
		open = append(open, proto.FwRule{Proto: "tcp", Port: p, From: a.SSHFrom})
	}
	if len(a.Peers) > 0 {
		open = append(open, proto.FwRule{Proto: "udp", Port: 51820, From: a.Peers})
	}
	open = append(append(open, a.Kept...), a.Allow...)
	out := []proto.FwPort{}
	for _, p := range ports {
		if ip, err := netip.ParseAddr(p.Address); err == nil && ip.IsLoopback() {
			continue
		}
		p.Exposure = "everyone"
		if a.On {
			p.Exposure = "closed"
			for _, r := range open {
				if r.Port != p.Port || (r.Proto != p.Proto && r.Proto != "both") {
					continue
				}
				if len(r.From) == 0 {
					p.Exposure = "everyone"
					break
				}
				p.Exposure = "some"
			}
		}
		out = append(out, p)
	}
	return out
}
