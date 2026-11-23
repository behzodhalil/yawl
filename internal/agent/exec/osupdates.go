package exec

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
)

type Units interface {
	Start(ctx context.Context, name string) error
	State(ctx context.Context, name string) string
}

var (
	updatesRe  = regexp.MustCompile(`(\d+) updates? can be applied immediately`)
	securityRe = regexp.MustCompile(`(\d+) of these updates (?:is|are) (?:a )?(?:standard )?security updates?`)
	autoRe     = regexp.MustCompile(`APT::Periodic::Unattended-Upgrade\s+"1"`)
)

const upgradeUnit = "apt-daily-upgrade.service"

var unitWait = 10 * time.Second

func (d *Dispatcher) host(p string) string {
	root := d.HostRoot
	if root == "" {
		root = "/"
	}
	return filepath.Join(root, p)
}

func (d *Dispatcher) read(p string) (string, bool) {
	b, err := os.ReadFile(d.host(p))
	return string(b), err == nil
}

func (d *Dispatcher) tail(p string, n int64) (string, bool) {
	f, err := os.Open(d.host(p))
	if err != nil {
		return "", false
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > n {
		f.Seek(fi.Size()-n, io.SeekStart)
	}
	b, err := io.ReadAll(io.LimitReader(f, n))
	return string(b), err == nil
}

func count(re *regexp.Regexp, s string, have bool) int {
	if !have {
		return -1
	}
	if m := re.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

func (d *Dispatcher) osStatus(ctx context.Context) proto.OSStatus {
	avail, have := d.read("var/lib/update-notifier/updates-available")
	st := proto.OSStatus{Updates: count(updatesRe, avail, have), Security: count(securityRe, avail, have)}
	_, st.Reboot = d.read("var/run/reboot-required")
	if pk, ok := d.read("var/run/reboot-required.pkgs"); ok {
		st.RebootPkgs = strings.Fields(pk)
	}
	auto, _ := d.read("etc/apt/apt.conf.d/20auto-upgrades")
	st.Auto = autoRe.MatchString(auto)
	_, st.Installer = d.read("usr/bin/unattended-upgrade")
	if log, ok := d.tail("var/log/unattended-upgrades/unattended-upgrades.log", 64<<10); ok {
		lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
		st.Log = strings.Join(lines[max(0, len(lines)-20):], "\n")
	}
	if d.Units != nil {
		uctx, cancel := context.WithTimeout(ctx, unitWait)
		defer cancel()
		s := d.Units.State(uctx, upgradeUnit)
		st.Running = s == "activating" || s == "active"
	}
	return st
}

func (d *Dispatcher) osUpgrade(ctx context.Context) proto.Result {
	if _, ok := d.read("usr/bin/unattended-upgrade"); !ok {
		return proto.Result{Cause: "unattended-upgrades is not installed on this server; install it once with: sudo apt-get install unattended-upgrades"}
	}
	if d.Units == nil {
		return proto.Result{Cause: "this agent cannot reach systemd"}
	}
	// apt.systemd.daily does nothing on a day it already ran: its stamps go first
	for _, st := range []string{"update-stamp", "upgrade-stamp", "unattended-upgrades-stamp", "download-upgradeable-stamp"} {
		os.Remove(d.host("var/lib/apt/periodic/" + st))
	}
	uctx, cancel := context.WithTimeout(ctx, unitWait)
	defer cancel()
	for _, u := range []string{"apt-daily.service", upgradeUnit} { // the second is ordered after the first
		if err := d.Units.Start(uctx, u); err != nil {
			return proto.Result{Cause: "systemd did not start " + u + ": " + err.Error()}
		}
	}
	return proto.Result{OK: true}
}
