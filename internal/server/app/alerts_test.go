package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/notify"
	"github.com/behzodhalil/yawl/internal/spec"
)

func every() map[string]notify.Message {
	l := "acme/production/api"
	return map[string]notify.Message{
		"deploy.failed":          DeployFailed(l, "dep_1", "3f0a1a3c9", "build", "npm ERR! Missing script: \"build\"\nmore", "9c41d2e77", false, "acme-api deploy failed: build exited 1."),
		"deploy.rolled_back":     DeployFailed(l, "dep_1", "3f0a1a3", "check", "health check answered 502", "nothing", true, "acme-api deploy rolled back: x."),
		"deploy.live":            DeployLive(l, "dep_1", "3f0a1a3", "2 minutes", "https://api.acme.uz", "acme-api deployed."),
		"deploy.auto_rollback":   AutoRollback(l, "9c41d2e", "failed 3 health checks within 5 minutes of going live", "x"),
		"service.down":           ServiceDown(l, spec.KindWeb, "GET /health answered 502", "acme-api is down: 502."),
		"service.recovered":      ServiceUp(l, "12 minutes", "acme-api recovered."),
		"service.oom":            ServiceOOM(l, spec.KindWeb, "512 MB", 2, "x"),
		"service.oom.close":      ServiceOOMOver(l, "x"),
		"service.stopped":        ServiceStopped(l, "x"),
		"server.offline":         ServerOffline("hetzner-1", "no answer for 2 minutes (limit 2 minutes)", "7 services", "x"),
		"server.online":          ServerOnline("hetzner-1", "23 minutes", "x"),
		"server.disk":            ServerDisk("hetzner-1", 91, 85, "x"),
		"server.disk.close":      ServerDiskOK("hetzner-1", 78, "x"),
		"server.memory":          ServerMemory("hetzner-1", "acme/production/api 1.2 GB", "x"),
		"server.memory.close":    ServerMemoryOK("hetzner-1", "x"),
		"server.load":            ServerLoad("hetzner-1", "6.4 over 10 minutes, 4 CPUs (limit 1.5 per CPU)", "acme/production/api 312% CPU", "x"),
		"server.load.close":      ServerLoadOK("hetzner-1", "2.1 now, 4 CPUs", "x"),
		"server.forecast":        ServerForecast("hetzner-1", "full in about 5 days (+2.1 GB a day)", "x"),
		"server.forecast.close":  ServerForecastOK("hetzner-1", "x"),
		"backup.failed":          BackupFailed("acme/production/postgres", "", `too many connections for role "app"`, "Nov 3, 08:01 UTC+5", "x"),
		"backup.recovered":       BackupOK("acme/production/postgres", "data", "x"),
		"backup.missed":          BackupMissed("acme/production/postgres", "", "never", true, "x"),
		"backup.copy_failed":     BackupCopyFailed(l, "", "r2-eu", "403 Forbidden", "x"),
		"panel.backup.failed":    PanelBackupFailed("access denied", "never", "x"),
		"panel.backup.missed":    PanelBackupMissed("Nov 3, 04:00 UTC+5", "x"),
		"panel.backup.recovered": PanelBackupOK("x"),
		"service.logs":           LogAlert(l, "Too many stderr lines", "6 lines in 5 minutes (limit 5)", "panic: nil map", "x"),
		"service.logs.close":     LogAlertOver(l, "Stderr lines under the alert for 30 minutes", "x"),
		"cron.failed":            CronFailed("acme/production/report", "exit 1 after 41 s", "x"),
		"cron.recovered":         CronOK("acme/production/report", "x"),
		"network.silent":         NetworkSilent("hel1", "hk1", "no handshake for 5 min (limit 5 min)", "51820", "x"),
		"network.recovered":      NetworkOK("hel1", "hk1", "hel1 reaches hk1 again", "x"),
		"trash.deleted":          Trashed(l, "owner@acme.uz", "Nov 7, 14:14 UTC+5", "x"),
		"trash.restored":         Restored(l, "owner@acme.uz", "x"),
		"trash.purged":           Purged(l, "x"),
		"firewall.ban":           Banned("hetzner-1", "203.0.113.9", "x"),
		"pitr.lag":               WALLag(l, "700 MB waiting to upload", "x"),
		"env.move.failed":        MoveFailed("acme/production", "hk1", "the agent did not answer", "x"),
		"env.move.done":          MoveDone("acme/production", "hk1", "x"),
		"env.revealed":           Revealed(l, "API_KEY", "owner@acme.uz", "x"),
		"watch.down":             WatcherDown("it has not answered for 5 minutes", "x"),
		"watch.up":               WatcherUp("x"),
	}
}

// emoji is a rune with emoji presentation, or one that turns a rune into
// one: what M7 forbids. U+2713, the ok mark, is text.
func emoji(r rune) bool {
	switch {
	case r == 0x2713:
		return false
	case r >= 0x1F000 && r <= 0x1FAFF, r >= 0x2600 && r <= 0x27BF, r >= 0x2B00 && r <= 0x2BFF, r == 0xFE0F:
		return true
	}
	return false
}

var badges = map[notify.State][]string{
	"fail": {"Failed", "Down", "Offline", "Rolled back"},
	"ok":   {"Deployed", "Available", "Online", "Completed", "Stopped"},
	"warn": {"Alert", "High", "Soon"},
}

func TestEveryKindEveryChannel(t *testing.T) {
	at := time.Date(2026, 11, 5, 14, 14, 0, 0, time.FixedZone("", 5*3600))
	if !emoji(0x2714) || !emoji(0x1F534) || emoji(0x24E7) || emoji(0x24D8) || emoji(0x2713) {
		t.Fatal("the emoji test itself is wrong")
	}
	for kind, m := range every() {
		m.At = at
		m = m.Linked("https://panel.acme.uz")
		if m.State == "" || m.Head == "" || m.Line == "" || m.Text == "" || len(m.Links) > 2 || (m.State == "ok" && m.Next != "") {
			t.Errorf("%s: %+v", kind, m)
		}
		found := false
		for _, b := range badges[m.State] {
			found = found || b == m.Badge
		}
		if !found {
			t.Errorf("%s: badge %q is not a %s word", kind, m.Badge, m.State)
		}
		for _, now := range []time.Time{at.Add(time.Minute), at.Add(25 * time.Hour)} {
			for ch, out := range notify.Drawn(m, now) {
				for i, r := range out {
					if emoji(r) {
						t.Errorf("%s on %s: emoji %U at %d", kind, ch, r, i)
					}
				}
			}
		}
	}
}

func TestPaths(t *testing.T) {
	for _, c := range [][2]string{
		{SvcPath("acme/production/api"), "/p/acme/production/s/api"},
		{SvcPath("acme/production/api", "deploys", "dep_1"), "/p/acme/production/s/api/deploys/dep_1"},
		{SvcPath("api"), ""},
		{serverPath("hel 1", "disk"), "/servers/hel%201/disk"},
		{envWord("acme/staging/api"), "Staging"},
		{fmt.Sprint(isSHA("9c41d2e77"), isSHA("nothing")), "true false"},
	} {
		if c[0] != c[1] {
			t.Errorf("%q, want %q", c[0], c[1])
		}
	}
}

func TestDatabaseLinks(t *testing.T) {
	for _, m := range []notify.Message{ServiceDown("a/p/db", spec.KindPostgres, "x", "x"), ServiceOOM("a/p/db", spec.KindPostgres, "", 1, "x")} {
		if len(m.Links) != 1 || m.Links[0].Label != "Open service" {
			t.Fatalf("%s: links %v", m.Head, m.Links)
		}
	}
}
