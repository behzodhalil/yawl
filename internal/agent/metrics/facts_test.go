package metrics

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestParseOSRelease(t *testing.T) {
	name, id := ParseOSRelease("NAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nID=ubuntu\nID_LIKE=debian\n")
	if name != "Ubuntu 24.04.1 LTS" || id != "ubuntu" {
		t.Fatalf("%q %q", name, id)
	}
	if name, id := ParseOSRelease("PRETTY_NAME='Debian GNU/Linux 12 (bookworm)'\nID=\"debian\""); name != "Debian GNU/Linux 12 (bookworm)" || id != "debian" {
		t.Fatalf("%q %q", name, id)
	}
	if name, id := ParseOSRelease(""); name != "" || id != "" {
		t.Fatalf("%q %q", name, id)
	}
}

func TestNetBytes(t *testing.T) {
	rx, tx := NetBytes(read(t, "netdev"))
	if rx != 1_500_000 || tx != 700_000 {
		t.Fatalf("rx %d tx %d", rx, tx)
	}
}

func TestNetRate(t *testing.T) {
	var n Net
	if r, tx := n.Rate(1000, 500, 60_000); r != nil || tx != nil {
		t.Fatal("first read has no base")
	}
	r, tx := n.Rate(61_000, 6_500, 120_000)
	if r == nil || *r != 1000 || *tx != 100 {
		t.Fatalf("%v %v", r, tx)
	}
	if r, tx := n.Rate(10, 10, 180_000); r != nil || tx != nil {
		t.Fatal("a reset is a gap")
	}
	if r, _ := n.Rate(6010, 610, 240_000); r == nil || *r != 100 {
		t.Fatalf("after a reset the next minute counts from the new base: %v", r)
	}
	n.Forget()
	if r, _ := n.Rate(9000, 900, 300_000); r != nil {
		t.Fatal("after a failed read the next has no base")
	}
}

func TestSamplerNet(t *testing.T) {
	proc := t.TempDir()
	os.MkdirAll(filepath.Join(proc, "net"), 0o755)
	write := func(rx int) {
		os.WriteFile(filepath.Join(proc, "net", "dev"), []byte("h\nh\n  eth0: "+strconv.Itoa(rx)+" 0 0 0 0 0 0 0 "+strconv.Itoa(rx/2)+" 0 0 0 0 0 0 0\n"), 0o644)
	}
	s := &Sampler{RT: &runtime.Fake{}, Proc: proc, Disk: t.TempDir()}
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	write(0)
	if m := s.Sample(context.Background(), at).Machine; m.NetRx != nil {
		t.Fatalf("first minute: %v", *m.NetRx)
	}
	write(600_000)
	m := s.Sample(context.Background(), at.Add(time.Minute)).Machine
	if m.NetRx == nil || *m.NetRx != 10_000 || *m.NetTx != 5_000 {
		t.Fatalf("%+v", m)
	}
}

func TestFacts(t *testing.T) {
	proc, etc := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(proc, "sys", "kernel"), 0o755)
	os.WriteFile(filepath.Join(proc, "sys", "kernel", "osrelease"), []byte("6.8.0-48-generic\n"), 0o644)
	os.WriteFile(filepath.Join(proc, "uptime"), []byte("3600.52 7000.00\n"), 0o644)
	os.WriteFile(filepath.Join(etc, "os-release"), []byte("PRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nID=ubuntu\n"), 0o644)
	var hb proto.Heartbeat
	Facts(&hb, proc, etc)
	if hb.OSName != "Ubuntu 24.04.1 LTS" || hb.OSID != "ubuntu" || hb.Kernel != "6.8.0-48-generic" || hb.UptimeS != 3600 || hb.Cores < 1 {
		t.Fatalf("%+v", hb)
	}
	var none proto.Heartbeat
	Facts(&none, t.TempDir(), t.TempDir())
	if none.OSName != "" || none.Kernel != "" || none.UptimeS != 0 {
		t.Fatalf("%+v", none)
	}
}
