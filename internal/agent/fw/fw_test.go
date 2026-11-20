package fw

import (
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
)

type fakeNFT struct {
	applied []string
	none    bool
	bans    []proto.FwBan
	gone    bool
	now     *time.Time
}

func (f *fakeNFT) Apply(rules []Rule) error {
	var b strings.Builder
	for _, r := range rules {
		b.WriteString(r.String() + "\n")
	}
	f.applied = append(f.applied, b.String())
	f.bans = nil
	return nil
}
func (f *fakeNFT) Delete() error   { f.applied = append(f.applied, "DELETE"); return nil }
func (f *fakeNFT) Present() bool   { return !f.gone }
func (f *fakeNFT) Available() bool { return !f.none }
func (f *fakeNFT) Ban(a netip.Addr, d time.Duration) error {
	f.bans = append(f.bans, proto.FwBan{IP: a.String(), Until: f.now.Add(d).UnixMilli()})
	return nil
}
func (f *fakeNFT) Unban(netip.Addr) error       { return nil }
func (f *fakeNFT) Bans() ([]proto.FwBan, error) { return f.bans, nil }
func (f *fakeNFT) last() string {
	if len(f.applied) == 0 {
		return ""
	}
	return f.applied[len(f.applied)-1]
}

func newFW(t *testing.T) (*Firewall, *fakeNFT, *time.Time) {
	now := time.Unix(1_800_000_000, 0)
	n := &fakeNFT{now: &now}
	return &Firewall{NFT: n, Dir: t.TempDir(), Now: func() time.Time { return now }, SSHPorts: func() []int { return []int{22} }}, n, &now
}

func args(version string, trial bool, port int) proto.FirewallArgs {
	return proto.FirewallArgs{On: true, Version: version, Trial: trial, Allow: []proto.FwRule{{Proto: "tcp", Port: port}}}
}

func TestTrialRevertsAlone(t *testing.T) {
	f, n, now := newFW(t)
	if err := f.Apply(args("v1", false, 8080)); err != nil {
		t.Fatal(err)
	}
	if err := f.Apply(args("v2", true, 9090)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(n.last(), "9090") || f.Status().TrialUntil != now.Add(2*time.Minute).UnixMilli() {
		t.Fatalf("trial not applied: %+v", f.Status())
	}
	f.Check()
	if !strings.Contains(n.last(), "9090") {
		t.Fatal("reverted before the window")
	}
	*now = now.Add(2*time.Minute + time.Second)
	f.Check()
	if !strings.Contains(n.last(), "8080") || f.Status().Version != "v1" || f.Status().TrialUntil != 0 {
		t.Fatalf("not reverted: %q %+v", n.last(), f.Status())
	}

	f.Apply(args("v3", true, 7070))
	if err := f.Keep("v3"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	f.Check()
	if !strings.Contains(n.last(), "7070") || f.Status().Version != "v3" {
		t.Fatalf("kept trial went back: %+v", f.Status())
	}
	if err := f.Keep("v9"); err == nil {
		t.Fatal("kept an unknown version")
	}
}

func TestSameVersionNotReapplied(t *testing.T) {
	f, n, _ := newFW(t)
	f.Apply(args("v1", false, 8080))
	f.Apply(args("v1", false, 8080))
	if len(n.applied) != 1 {
		t.Fatalf("applied %d times; a reapply would empty the SSH ban set", len(n.applied))
	}
}

func TestTrialFromOff(t *testing.T) {
	f, n, now := newFW(t)
	f.Apply(args("v1", true, 8080))
	*now = now.Add(3 * time.Minute)
	f.Check()
	if n.last() != "DELETE" || f.Status().On {
		t.Fatalf("a first trial did not go back to off: %q", n.last())
	}
}

func TestBootAppliesKept(t *testing.T) {
	f, n, _ := newFW(t)
	f.Apply(args("v1", false, 8080))
	f.Apply(args("v2", true, 9090))
	g := &Firewall{NFT: &fakeNFT{}, Dir: f.Dir, Now: f.Now, SSHPorts: f.SSHPorts}
	if err := g.Boot(); err != nil {
		t.Fatal(err)
	}
	gn := g.NFT.(*fakeNFT)
	if !strings.Contains(gn.last(), "8080") || g.Status().TrialUntil != 0 {
		t.Fatalf("boot: %q", gn.last())
	}
	if _, err := os.Stat(filepath.Join(f.Dir, "fw.trial.json")); !os.IsNotExist(err) {
		t.Fatal("the trial file survived boot")
	}
	_ = n
	empty := &Firewall{NFT: &fakeNFT{}, Dir: t.TempDir(), Now: f.Now, SSHPorts: f.SSHPorts}
	if err := empty.Boot(); err != nil || strings.Join(empty.NFT.(*fakeNFT).applied, ",") != "DELETE" {
		t.Fatal("boot without rules did more than make sure the table is gone")
	}
}

func TestOffDeletes(t *testing.T) {
	f, n, _ := newFW(t)
	f.Apply(args("v1", false, 8080))
	if err := f.Apply(proto.FirewallArgs{On: false, Version: "off"}); err != nil {
		t.Fatal(err)
	}
	if n.last() != "DELETE" || f.Status().On {
		t.Fatalf("%q %+v", n.last(), f.Status())
	}
}

func TestNoNft(t *testing.T) {
	f, n, _ := newFW(t)
	n.none = true
	if err := f.Apply(args("v1", false, 8080)); err == nil || err.Error() != "The firewall needs nftables (Debian 12, Ubuntu 22.04 or later)." {
		t.Fatalf("%v", err)
	}
}

func TestSSHPort(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sshd_config")
	os.WriteFile(p, []byte("# Port 2200\nPermitRootLogin no\nPort 2222\n"), 0o644)
	if got := SSHPortFrom(p); got != 2222 {
		t.Fatalf("%d", got)
	}
	if got := SSHPortFrom(filepath.Join(dir, "none")); got != 22 {
		t.Fatalf("default %d", got)
	}
}

func TestStatusPortsAndBans(t *testing.T) {
	f, n, now := newFW(t)
	f.Proc = "testdata/proc"
	n.bans = []proto.FwBan{{IP: "192.0.2.9", Until: now.Add(time.Minute).UnixMilli()}}
	f.Apply(proto.FirewallArgs{On: true, Version: "v1", SSHFrom: []string{"192.0.2.0/24"}})
	s := f.Status()
	exp := map[int]string{}
	for _, p := range s.Ports {
		exp[p.Port] = p.Exposure
	}
	if exp[22] != "some" || exp[6379] != "closed" || exp[51820] != "closed" || len(s.Bans) != 1 || s.Bans[0].Until != now.Add(time.Minute).UnixMilli() {
		t.Fatalf("%+v", s)
	}
	if !f.SSHAllowed(netip.MustParseAddr("192.0.2.7")) || f.SSHAllowed(netip.MustParseAddr("198.51.100.1")) {
		t.Fatal("SSHAllowed does not follow SSHFrom")
	}
}

func TestMeterWithoutAuthLog(t *testing.T) {
	f, n, _ := newFW(t)
	f.AuthLog = filepath.Join(f.Dir, "auth.log")
	f.Apply(args("v1", false, 8080))
	if !strings.Contains(n.last(), "meter @ssh_meter4") {
		t.Fatal("no auth.log, yet no meter")
	}
	os.WriteFile(f.AuthLog, nil, 0o644)
	f.Apply(args("v2", false, 8080))
	if strings.Contains(n.last(), "meter") {
		t.Fatal("auth.log there, yet a meter")
	}
}

func TestUFWFrom(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ufw.conf")
	os.WriteFile(p, []byte("# comment\nENABLED=yes\nLOGLEVEL=low\n"), 0o644)
	if !UFWFrom(p) {
		t.Fatal("enabled ufw not seen")
	}
	os.WriteFile(p, []byte("ENABLED=no\n"), 0o644)
	if UFWFrom(p) || UFWFrom(filepath.Join(dir, "none")) {
		t.Fatal("disabled or absent ufw seen as on")
	}
}

func TestBootWithoutKeptDeletes(t *testing.T) {
	f, _, _ := newFW(t)
	f.Apply(args("v1", true, 8080))
	g := &Firewall{NFT: &fakeNFT{}, Dir: f.Dir, Now: f.Now, SSHPorts: f.SSHPorts}
	if err := g.Boot(); err != nil {
		t.Fatal(err)
	}
	if gn := g.NFT.(*fakeNFT); gn.last() != "DELETE" {
		t.Fatalf("boot left the trial's table: %q", gn.last())
	}
}

func TestCheckRestoresFlushedTable(t *testing.T) {
	f, n, _ := newFW(t)
	f.Apply(args("v1", false, 8080))
	n.gone = true
	f.Check()
	if len(n.applied) != 2 || !strings.Contains(n.last(), "8080") {
		t.Fatalf("not put back: %d %q", len(n.applied), n.last())
	}
}

func TestSSHPortOf(t *testing.T) {
	proc := t.TempDir()
	os.CopyFS(proc, os.DirFS("testdata/proc"))
	os.MkdirAll(filepath.Join(proc, "123", "fd"), 0o755)
	os.WriteFile(filepath.Join(proc, "123", "comm"), []byte("sshd\n"), 0o644)
	os.Symlink("socket:[3333]", filepath.Join(proc, "123", "fd", "3"))
	os.Symlink("socket:[1111]", filepath.Join(proc, "123", "fd", "4"))
	tcp6, _ := os.OpenFile(filepath.Join(proc, "net", "tcp6"), os.O_APPEND|os.O_WRONLY, 0)
	tcp6.WriteString("   1: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 6666 1 0000000000000000 100 0 0 10 0\n")
	tcp6.Close()
	os.Symlink("socket:[6666]", filepath.Join(proc, "123", "fd", "5"))
	dir := t.TempDir()
	cfg := filepath.Join(dir, "sshd_config")
	os.MkdirAll(filepath.Join(dir, "sshd_config.d"), 0o755)
	os.WriteFile(cfg, []byte("Include "+filepath.Join(dir, "sshd_config.d", "*.conf")+"\nPermitRootLogin no\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "sshd_config.d", "50-cloud.conf"), []byte("Port 2222\n"), 0o644)
	if got := SSHPortOf(proc, cfg); !slices.Equal(got, []int{22, 6379}) {
		t.Fatalf("listeners: %v", got)
	}
	if got := SSHPortOf(t.TempDir(), cfg); !slices.Equal(got, []int{2222}) {
		t.Fatalf("include: %v", got)
	}
}

func TestApplyKeepsBans(t *testing.T) {
	f, n, now := newFW(t)
	until := now.Add(30 * time.Minute).UnixMilli()
	n.bans = []proto.FwBan{{IP: "192.0.2.9", Until: until}, {IP: "2001:db8::9", Until: now.Add(-time.Minute).UnixMilli()}}
	f.Apply(args("v1", false, 8080))
	*now = now.Add(10 * time.Minute)
	f.Apply(args("v2", true, 9090))
	if want := []proto.FwBan{{IP: "192.0.2.9", Until: until}}; !reflect.DeepEqual(n.bans, want) {
		t.Fatalf("bans %+v, want %+v", n.bans, want)
	}
}
