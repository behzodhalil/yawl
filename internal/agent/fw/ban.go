package fw

import (
	"context"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	banFailures = 5
	banWindow   = 10 * time.Minute
	banFor      = time.Hour
	seenMax     = 1024
)

// failed are the lines that count; the address is read from the end of the
// line, never after the user name, which the client chooses.
var (
	failed    = regexp.MustCompile(`(?:Failed (?:password|publickey) for |Invalid user |Connection closed by authenticating user )`)
	addrAtEnd = regexp.MustCompile(`([0-9a-fA-F:.]+) port \d+(?: ssh2)?(?: \[preauth\])?\s*$`)
)

type Ban struct {
	Allowed func(netip.Addr) bool

	mu   sync.Mutex
	seen map[netip.Addr][]time.Time
}

func (b *Ban) Line(text string, at time.Time) (netip.Addr, bool) {
	var addr netip.Addr
	if failed.MatchString(text) {
		if m := addrAtEnd.FindStringSubmatch(text); m != nil {
			if a, err := netip.ParseAddr(m[1]); err == nil {
				addr = a.Unmap()
			}
		}
	}
	if !addr.IsValid() || (b.Allowed != nil && b.Allowed(addr)) {
		return addr, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.seen == nil {
		b.seen = map[netip.Addr][]time.Time{}
	}
	var keep []time.Time
	for _, t := range b.seen[addr] {
		if at.Sub(t) < banWindow {
			keep = append(keep, t)
		}
	}
	keep = append(keep, at)
	if len(keep) >= banFailures {
		delete(b.seen, addr)
		return addr, true
	}
	b.seen[addr] = keep
	if len(b.seen) > seenMax {
		for a, ts := range b.seen {
			if at.Sub(ts[len(ts)-1]) >= banWindow {
				delete(b.seen, a)
			}
		}
	}
	return addr, false
}

func (b *Ban) Tail(ctx context.Context, path string, poll time.Duration, ban func(netip.Addr)) {
	var off int64 = -1
	var rest string
	for {
		if fi, err := os.Stat(path); err == nil {
			size := fi.Size()
			if off < 0 || size < off {
				if off < 0 {
					off = size
				} else {
					off = 0
				}
				rest = ""
			}
			if size > off {
				if f, err := os.Open(path); err == nil {
					buf := make([]byte, size-off)
					n, _ := f.ReadAt(buf, off)
					f.Close()
					off += int64(n)
					lines := strings.Split(rest+string(buf[:n]), "\n")
					rest = lines[len(lines)-1]
					for _, l := range lines[:len(lines)-1] {
						if a, now := b.Line(l, time.Now()); now {
							ban(a)
						}
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(poll):
		}
	}
}
