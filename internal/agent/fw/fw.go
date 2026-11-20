package fw

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
)

const TrialWindow = 2 * time.Minute

var ErrNoNft = errors.New("The firewall needs nftables (Debian 12, Ubuntu 22.04 or later).")

type NFT interface {
	Apply(rules []Rule) error
	Delete() error
	Present() bool
	Available() bool
	Ban(a netip.Addr, d time.Duration) error
	Unban(a netip.Addr) error
	Bans() ([]proto.FwBan, error)
}

type applied struct {
	Version string             `json:"version"`
	Args    proto.FirewallArgs `json:"args"`
}

type trial struct {
	applied
	Until    int64   `json:"until"`
	Previous applied `json:"previous"`
}

type Firewall struct {
	NFT      NFT
	Dir      string
	Now      func() time.Time
	SSHPorts func() []int
	UFW      func() bool
	Proc     string
	AuthLog  string

	mu sync.Mutex
}

func (f *Firewall) path(name string) string { return filepath.Join(f.Dir, name) }

func (f *Firewall) load(name string, v any) bool {
	b, err := os.ReadFile(f.path(name))
	return err == nil && json.Unmarshal(b, v) == nil
}

func (f *Firewall) save(name string, v any) error {
	b, _ := json.Marshal(v)
	tmp := f.path(name + ".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path(name))
}

func (f *Firewall) metered() bool {
	_, err := os.Stat(f.AuthLog)
	return f.AuthLog == "" || err != nil
}

func (f *Firewall) put(a applied) error {
	if !a.Args.On {
		return f.NFT.Delete()
	}
	rules, err := Plan(a.Args, f.SSHPorts(), f.metered())
	if err != nil {
		return err
	}
	bans, _ := f.NFT.Bans()
	if err := f.NFT.Apply(rules); err != nil {
		return err
	}
	for _, b := range bans {
		a, err := netip.ParseAddr(b.IP)
		if left := time.UnixMilli(b.Until).Sub(f.Now()); err == nil && left > 0 {
			if err := f.NFT.Ban(a, left); err != nil {
				slog.Warn("firewall: a ban could not be put back", "ip", a, "err", err)
			}
		}
	}
	return nil
}

func (f *Firewall) current() (applied, *trial) {
	var t trial
	if f.load("fw.trial.json", &t) {
		return t.applied, &t
	}
	var k applied
	f.load("fw.kept.json", &k)
	return k, nil
}

func (f *Firewall) Apply(a proto.FirewallArgs) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.NFT.Available() {
		return ErrNoNft
	}
	want := applied{Version: a.Version, Args: a}
	if a.On {
		if _, err := Plan(a, f.SSHPorts(), false); err != nil {
			return err
		}
	}
	cur, tr := f.current()
	if cur.Version == want.Version && (tr == nil || a.Trial) {
		return nil
	}
	if err := f.put(want); err != nil {
		return err
	}
	if a.Trial {
		prev := cur
		if tr != nil {
			prev = tr.Previous
		}
		return f.save("fw.trial.json", trial{applied: want, Until: f.Now().Add(TrialWindow).UnixMilli(), Previous: prev})
	}
	os.Remove(f.path("fw.trial.json"))
	return f.keep(want)
}

func (f *Firewall) keep(a applied) error {
	if !a.Args.On {
		os.Remove(f.path("fw.kept.json"))
		return nil
	}
	return f.save("fw.kept.json", a)
}

func (f *Firewall) Keep(version string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, tr := f.current()
	if tr == nil {
		if cur.Version == version {
			return nil
		}
		return errors.New("There is no change waiting to be kept; it went back.")
	}
	if tr.Version != version {
		return errors.New("A newer change is waiting; keep that one.")
	}
	if err := f.keep(tr.applied); err != nil {
		return err
	}
	return os.Remove(f.path("fw.trial.json"))
}

func (f *Firewall) Check() {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, tr := f.current()
	if tr == nil {
		if cur.Args.On && !f.NFT.Present() {
			if err := f.put(cur); err != nil {
				slog.Warn("firewall: the kept rules could not be put back", "err", err)
			}
		}
		return
	}
	if f.Now().UnixMilli() < tr.Until {
		return
	}
	if err := f.put(tr.Previous); err != nil {
		slog.Warn("firewall: the rules from before could not be put back; trying again", "err", err)
		return
	}
	os.Remove(f.path("fw.trial.json"))
}

func (f *Firewall) Boot() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	os.Remove(f.path("fw.trial.json"))
	if !f.NFT.Available() {
		return nil
	}
	var k applied
	if !f.load("fw.kept.json", &k) {
		return f.NFT.Delete() // the table outlives the agent: a trial on top of off must go (review C1)
	}
	if err := f.put(k); err != nil {
		f.NFT.Delete()
		return err
	}
	return nil
}

func (f *Firewall) Status() proto.FirewallStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, tr := f.current()
	s := proto.FirewallStatus{On: cur.Args.On, Available: f.NFT.Available(), Version: cur.Version, Bans: []proto.FwBan{}, Ports: []proto.FwPort{}}
	if tr != nil {
		s.TrialUntil = tr.Until
	}
	if f.UFW != nil {
		s.UFW = f.UFW()
	}
	if s.On {
		if bans, err := f.NFT.Bans(); err == nil {
			s.Bans = append(s.Bans, bans...)
		}
	}
	if f.Proc != "" {
		if ports, err := Listening(f.Proc); err == nil {
			s.Ports = Classify(ports, cur.Args, f.SSHPorts())
		}
	}
	return s
}

func (f *Firewall) SSHAllowed(a netip.Addr) bool {
	f.mu.Lock()
	cur, _ := f.current()
	f.mu.Unlock()
	for _, s := range cur.Args.SSHFrom {
		if p, err := netip.ParsePrefix(s); err == nil && p.Contains(a) {
			return true
		}
		if b, err := netip.ParseAddr(s); err == nil && b == a {
			return true
		}
	}
	return false
}

func SSHPortOf(proc, config string) []int {
	var out []int
	if ports, err := Listening(proc); err == nil {
		for _, p := range ports {
			if p.Proto == "tcp" && p.Process == "sshd" {
				out = append(out, p.Port)
			}
		}
	}
	if len(out) == 0 {
		return []int{SSHPortFrom(config)}
	}
	return slices.Compact(out)
}

func SSHPortFrom(path string) int {
	if p := portIn(path, 0); p > 0 {
		return p
	}
	return 22
}

// portIn reads Port and Include lines in order, as sshd does (first wins).
func portIn(path string, depth int) int {
	file, err := os.Open(path)
	if err != nil || depth > 4 {
		return 0
	}
	defer file.Close()
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "port":
			if p, err := strconv.Atoi(fields[1]); err == nil && p > 0 && p < 65536 {
				return p
			}
		case "include":
			for _, pat := range fields[1:] {
				if !filepath.IsAbs(pat) {
					pat = filepath.Join("/etc/ssh", pat)
				}
				matches, _ := filepath.Glob(pat)
				for _, m := range matches {
					if p := portIn(m, depth+1); p > 0 {
						return p
					}
				}
			}
		}
	}
	return 0
}

func UFWFrom(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == "ENABLED=yes" {
			return true
		}
	}
	return false
}

func (f *Firewall) Unban(ip string) error {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return fmt.Errorf("%q is not an IP address", ip)
	}
	return f.NFT.Unban(a.Unmap())
}
