package exec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

type fakeUnits struct{ started []string }

func (f *fakeUnits) Start(_ context.Context, name string) error {
	f.started = append(f.started, name)
	return nil
}
func (f *fakeUnits) State(context.Context, string) string { return "inactive" }

func TestOSUpdates(t *testing.T) {
	root := t.TempDir()
	put := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte(s), 0o644)
	}
	put("var/lib/update-notifier/updates-available", "\n12 updates can be applied immediately.\n5 of these updates are standard security updates.\nTo see these additional updates run: apt list --upgradable\n")
	put("var/run/reboot-required", "*** System restart required ***\n")
	put("var/run/reboot-required.pkgs", "linux-image-6.8.0-48-generic\nlinux-base\n")
	put("etc/apt/apt.conf.d/20auto-upgrades", "APT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \"1\";\n")
	put("usr/bin/unattended-upgrade", "")
	put("var/log/unattended-upgrades/unattended-upgrades.log", "2026-10-30 06:12:01,123 INFO Starting unattended upgrades script\n2026-10-30 06:12:40,001 INFO All upgrades installed\n")
	units := &fakeUnits{}
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	d.HostRoot, d.Units = root, units
	res := d.Handle(context.Background(), cmd(t, proto.CmdOSStatus, struct{}{}))
	var st proto.OSStatus
	json.Unmarshal(res.Data, &st)
	if !res.OK || st.Updates != 12 || st.Security != 5 || !st.Reboot || len(st.RebootPkgs) != 2 || !st.Auto || !st.Installer ||
		st.Log != "2026-10-30 06:12:01,123 INFO Starting unattended upgrades script\n2026-10-30 06:12:40,001 INFO All upgrades installed" {
		t.Fatalf("%+v %+v", res, st)
	}
	stamps := []string{"update-stamp", "upgrade-stamp", "unattended-upgrades-stamp", "download-upgradeable-stamp"}
	for _, st := range stamps {
		put("var/lib/apt/periodic/"+st, "")
	}
	res = d.Handle(context.Background(), cmd(t, proto.CmdOSUpgrade, struct{}{}))
	if !res.OK || len(units.started) != 2 || units.started[0] != "apt-daily.service" || units.started[1] != "apt-daily-upgrade.service" {
		t.Fatalf("%+v %v", res, units.started)
	}
	for _, st := range stamps {
		if _, err := os.Stat(filepath.Join(root, "var/lib/apt/periodic", st)); err == nil {
			t.Errorf("%s stayed", st)
		}
	}

	bare := t.TempDir()
	d.HostRoot, units.started = bare, nil
	res = d.Handle(context.Background(), cmd(t, proto.CmdOSStatus, struct{}{}))
	st = proto.OSStatus{}
	json.Unmarshal(res.Data, &st)
	if st.Updates != -1 || st.Security != -1 || st.Installer || st.Reboot {
		t.Fatalf("%+v", st)
	}
	res = d.Handle(context.Background(), cmd(t, proto.CmdOSUpgrade, struct{}{}))
	if res.OK || res.Cause != "unattended-upgrades is not installed on this server; install it once with: sudo apt-get install unattended-upgrades" || len(units.started) != 0 {
		t.Fatalf("%+v %v", res, units.started)
	}
}

func TestOSStatusLogTail(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "var/log/unattended-upgrades/unattended-upgrades.log")
	os.MkdirAll(filepath.Dir(p), 0o755)
	f, _ := os.Create(p)
	f.Truncate(64 << 20)
	f.Seek(0, 2)
	for i := 0; i < 30; i++ {
		f.WriteString("\n2026-10-30 06:12:40,001 INFO line " + strconv.Itoa(i))
	}
	f.Close()
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	d.HostRoot = root
	var before, after goruntime.MemStats
	goruntime.ReadMemStats(&before)
	st := d.osStatus(context.Background())
	goruntime.ReadMemStats(&after)
	lines := strings.Split(st.Log, "\n")
	if len(lines) != 20 || lines[19] != "2026-10-30 06:12:40,001 INFO line 29" {
		t.Fatalf("%q", st.Log)
	}
	if n := after.TotalAlloc - before.TotalAlloc; n > 4<<20 {
		t.Fatalf("read %d bytes for a 20 line tail", n)
	}
}

type hungUnits struct{}

func (hungUnits) Start(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() }
func (hungUnits) State(ctx context.Context, _ string) string {
	<-ctx.Done()
	return ""
}

func TestOSStatusHungSystemd(t *testing.T) {
	old := unitWait
	unitWait = 100 * time.Millisecond
	defer func() { unitWait = old }()
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	d.HostRoot, d.Units = t.TempDir(), hungUnits{}
	done := make(chan struct{})
	go func() { d.osStatus(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the status waited on systemd")
	}
}
