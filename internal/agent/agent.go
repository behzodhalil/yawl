package agent

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/analytics"
	"github.com/behzodhalil/yawl/internal/agent/backup"
	"github.com/behzodhalil/yawl/internal/agent/build"
	"github.com/behzodhalil/yawl/internal/agent/conn"
	"github.com/behzodhalil/yawl/internal/agent/exec"
	"github.com/behzodhalil/yawl/internal/agent/fw"
	"github.com/behzodhalil/yawl/internal/agent/metrics"
	"github.com/behzodhalil/yawl/internal/agent/proxy"
	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/agent/secrets"
	"github.com/behzodhalil/yawl/internal/agent/state"
	"github.com/behzodhalil/yawl/internal/agent/systemd"
	"github.com/behzodhalil/yawl/internal/agent/wg"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/update"
)

func Run(ctx context.Context, args []string, version string) error {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	data := fs.String("data", "/var/lib/yawl/agent", "agent state directory")
	name := fs.String("name", hostname(), "server name shown in Yawl")
	ip := fs.String("ip", "", "this machine's public IP (default: the address of the default route)")
	socket := fs.String("caddy-socket", proxy.DefaultSocket, "Caddy admin socket")
	releaseBase := fs.String("release-base", "", "where releases are downloaded from (tests)")
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	if len(pos) > 0 && pos[0] == "join" {
		if len(pos) != 3 {
			return errors.New("usage: yawl agent join <server url> <token>")
		}
		if *ip == "" {
			*ip = defaultIP()
		}
		if err := conn.Enroll(ctx, pos[1], pos[2], *name, *ip, *data); err != nil {
			return err
		}
		fmt.Printf("Joined as %s (%s). Start the agent with `yawl agent`.\n", *name, *ip)
		return nil
	}
	if len(pos) == 2 && pos[0] == "server-url" {
		u := strings.TrimRight(pos[1], "/")
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			return errors.New("usage: yawl agent server-url https://panel.example.com")
		}
		p := filepath.Join(*data, "server_url")
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("this agent has not joined (%w)", err)
		}
		if err := os.WriteFile(p, []byte(u), 0o600); err != nil {
			return err
		}
		fmt.Printf("The agent now dials %s. Restart it: systemctl restart yawl-agent\n", u)
		return nil
	}
	if len(pos) > 0 {
		return fmt.Errorf("unknown agent command %q", pos[0])
	}

	cfg, err := conn.LoadConfig(*data, version)
	if err != nil {
		return fmt.Errorf("not joined yet (%w); run `yawl agent join <server url> <token>`", err)
	}
	cfg.Stats = stats
	if exe, err := os.Executable(); err == nil {
		f := &Follow{Updater: func() (update.Updater, error) { return update.Default(*releaseBase) },
			Current: version, Exe: exe, Every: time.Hour, Exit: func() { os.Exit(0) },
			StatePath: filepath.Join(*data, "update-attempt")}
		cfg.Follow = func(ctx context.Context, v string, waitIdle conn.WaitUntilNoCommandInFlight) {
			f.Wait = waitIdle
			f.Check(ctx, v)
		}
	}
	wall := &fw.Firewall{NFT: fw.Nft{}, Dir: *data, Now: time.Now, SSHPorts: func() []int { return fw.SSHPortOf("/proc", "/etc/ssh/sshd_config") },
		UFW: func() bool { return fw.UFWFrom("/etc/ufw/ufw.conf") }, Proc: "/proc", AuthLog: "/var/log/auth.log"}
	if err := wall.Boot(); err != nil {
		slog.Warn("firewall: kept rules not applied at boot", "err", err)
	}
	go func() {
		for range time.Tick(5 * time.Second) {
			wall.Check()
		}
	}()
	go (&fw.Ban{Allowed: wall.SSHAllowed}).Tail(ctx, wall.AuthLog, time.Second, func(a netip.Addr) {
		if err := wall.NFT.Ban(a, time.Hour); err == nil {
			slog.Info("firewall: banned for SSH", "ip", a)
		}
	})
	st, err := state.Open(filepath.Join(*data, "agent.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	rt, err := runtime.NewDocker()
	if err != nil {
		return err
	}
	px := proxy.NewCaddy(rt, *socket)
	px.Image, px.Wildcard, px.DNSToken = os.Getenv("YAWL_CADDY_IMAGE"), os.Getenv("YAWL_WILDCARD"), os.Getenv("YAWL_CLOUDFLARE_TOKEN")
	if (px.Wildcard == "") != (px.DNSToken == "") {
		slog.Warn("caddy: YAWL_WILDCARD and YAWL_CLOUDFLARE_TOKEN go together; the wildcard stays off")
	}
	if err := px.Ensure(ctx); err != nil {
		return err
	}
	if err := px.EnsureWildcard(ctx); err != nil {
		slog.Warn("caddy: the wildcard certificate is not on; each host gets its own", "err", err)
	}
	keys, err := secrets.Load(*data)
	if err != nil {
		return fmt.Errorf("no secrets key (%w); this agent joined before secrets existed, re-join it", err)
	}
	d := exec.NewDispatcher(rt, px, dockerBuilder{rt})
	d.Keys = keys
	d.DBDir = filepath.Join(*data, "databases")
	d.SnapDir = filepath.Join(*data, "snapshots")
	backup.SpoolDir = filepath.Join(*data, "restore")
	d.ImportDir = filepath.Join(*data, "imports")
	os.MkdirAll(d.ImportDir, 0o700)
	d.WatchFile = filepath.Join(*data, "watch.json")
	d.StatusDir = filepath.Join(filepath.Dir(*socket), "status")
	d.Units = systemd.Units{}
	d.DrainFile = filepath.Join(*data, "drain.json")
	d.LoadDrain()
	go d.RunDrain(ctx)
	go d.RunLogWatch(ctx)
	go Signal(ctx, *data, cfg.ServerID, nil, time.Minute)
	d.CronFile, d.St, d.Report = filepath.Join(*data, "cron.json"), st, ReportCron(*data, cfg.ServerID, nil)
	go d.CronLoop(ctx)
	if err := px.EnableMetrics(ctx); err != nil {
		slog.Warn("caddy metrics", "err", err)
	}
	d.Ring = &metrics.Ring{N: 60}
	if _, err := os.Stat(filepath.Join(*data, "wg.key")); err == nil {
		k, err := wg.LoadOrCreateKey(*data)
		if err != nil {
			return err
		}
		d.Net = &wg.Mesh{Link: wg.NewLink(), Key: k}
		if err := wg.AllowNonlocalBind("/proc/sys/net/ipv4/ip_nonlocal_bind"); err != nil {
			slog.Warn("private network: a database bound to it may not start at boot", "err", err)
		}
	}
	d.FW = wall
	oom := &metrics.OOMCount{}
	go rt.WatchOOM(ctx, func(l map[string]string) {
		if p, e, s, ok := metrics.OOMService(l); ok {
			oom.Add(p, e, s)
		}
	})
	visits := analytics.New()
	go analytics.Keep(ctx, px.EnsureAccessLog, time.Minute)
	go analytics.Follow(ctx, px.AccessLog(), 5*time.Second, func(l string) { visits.Line(l, time.Now()) })
	go (&metrics.Sampler{RT: rt, Caddy: px.Metrics, Proc: "/proc", Disk: "/", OOM: oom, Visits: visits, Logs: d.TakeLogHits}).Loop(ctx, d.Ring)
	slog.Info("yawl agent connecting", "server", cfg.ServerURL, "id", cfg.ServerID)
	cfg.Terms = d.Terminals()
	return conn.Dial(ctx, cfg, st, d.Handle)
}

type dockerBuilder struct{ d *runtime.Docker }

func (b dockerBuilder) Build(ctx context.Context, a proto.BuildArgs, secrets map[string][]byte) (proto.BuildData, error) {
	got, warnings, err := build.Build(ctx, b.d.C, build.Request{Builder: a.Builder, Root: a.Root, NoCache: a.NoCache, Repo: a.Repo, Branch: a.Branch, Commit: a.Commit, Image: a.Image, Token: a.Token,
		Secrets: secrets, Keys: a.Keys, Env: a.Env})
	return proto.BuildData{Commit: got.Commit, Subject: got.Subject, Warnings: warnings}, err
}

func (b dockerBuilder) Inspect(ctx context.Context, a proto.InspectArgs) (proto.InspectData, error) {
	r, err := build.Inspect(ctx, build.Request{Repo: a.Repo, Branch: a.Branch, Token: a.Token, Keys: a.Keys, Env: a.Env})
	return proto.InspectData(r), err
}

func stats() proto.Heartbeat {
	var s syscall.Statfs_t
	hb := proto.Heartbeat{}
	if syscall.Statfs("/", &s) == nil && s.Blocks > 0 {
		hb.DiskUsedPct = int(100 - s.Bavail*100/s.Blocks)
	}
	metrics.Facts(&hb, "/proc", "/etc")
	return hb
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

func defaultIP() string {
	c, err := net.Dial("udp", "1.1.1.1:80")
	if err != nil {
		return ""
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}
