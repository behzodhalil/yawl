package server

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	worker "github.com/behzodhalil/yawl/watch"

	"github.com/behzodhalil/yawl/internal/agentskill"
	"github.com/behzodhalil/yawl/internal/server/api"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/egress"
	"github.com/behzodhalil/yawl/internal/server/gateway"
	"github.com/behzodhalil/yawl/internal/server/gitea"
	"github.com/behzodhalil/yawl/internal/server/github"
	"github.com/behzodhalil/yawl/internal/server/gitlab"
	"github.com/behzodhalil/yawl/internal/server/join"
	"github.com/behzodhalil/yawl/internal/server/notify"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/server/watch"
	"github.com/behzodhalil/yawl/internal/server/workers"
	"github.com/behzodhalil/yawl/internal/update"
	"github.com/behzodhalil/yawl/web"
)

type opts struct {
	data, etc, listen, publicURL, releaseBase string
	panel                                     bool
	version                                   string
	managed                                   bool
	claimHash, installURL                     string
	limits                                    string
}

func flags(name string, args []string) (opts, []string, error) {
	var o opts
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&o.data, "data", "/var/lib/yawl", "state directory")
	fs.StringVar(&o.etc, "etc", "/etc/yawl", "key directory")
	fs.StringVar(&o.listen, "listen", "127.0.0.1:7000", "address to serve on")
	fs.StringVar(&o.publicURL, "public-url", "", "URL agents dial (default http://<listen>)")
	fs.StringVar(&o.releaseBase, "release-base", "", "where releases are downloaded from (tests)")
	fs.BoolVar(&o.panel, "panel", false, "join-token: for the agent on this machine, which routes the panel")
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return o, nil, err
		}
		if fs.NArg() == 0 {
			break
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	for _, e := range []struct {
		to        *string
		env, flag string
	}{{&o.listen, "YAWL_LISTEN", "listen"}, {&o.publicURL, "YAWL_PUBLIC_URL", "public-url"}, {&o.releaseBase, "YAWL_RELEASES", "release-base"},
		{&o.claimHash, "YAWL_CLAIM_HASH", ""}, {&o.installURL, "YAWL_INSTALL_URL", ""}, {&o.limits, "YAWL_LIMITS", ""}} {
		if v := os.Getenv(e.env); v != "" && (e.flag == "" || !flagSet(fs, e.flag)) {
			*e.to = v
		}
	}
	o.managed = os.Getenv("YAWL_MANAGED") == "1"
	if o.publicURL == "" {
		o.publicURL = "http://" + o.listen
	}
	return o, pos, nil
}

func defaultGateway(route string) netip.Addr {
	for _, line := range strings.Split(route, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 3 || f[1] != "00000000" {
			continue
		}
		b, err := hex.DecodeString(f[2])
		if err != nil || len(b) != 4 {
			continue
		}
		return netip.AddrFrom4([4]byte{b[3], b[2], b[1], b[0]}) // little endian
	}
	return netip.Addr{}
}

func flagSet(fs *flag.FlagSet, name string) (set bool) {
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

func ExitCode(err error) int {
	if errors.Is(err, app.ErrClaimed) {
		return 3
	}
	return 1
}

func Run(ctx context.Context, args []string, version string) error {
	if len(args) > 0 && args[0] == "restore" {
		return restoreCmd(ctx, args[1:])
	}
	o, pos, err := flags("server", args)
	if err != nil {
		return err
	}
	o.version = version
	if err := os.MkdirAll(o.data, 0o700); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(o.data, "yawl.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	a := app.New(st)
	if a.Limits, err = app.ParseLimits(o.limits); err != nil {
		return err
	}
	if u := os.Getenv("YAWL_BILLING_URL"); u != "" {
		a.Billing = &app.Billing{URL: u, Token: os.Getenv("YAWL_BILLING_TOKEN")}
	}

	switch {
	case len(pos) == 2 && pos[0] == "token" && pos[1] == "create":
		return errors.New("usage: yawl server token create <name>")
	case len(pos) == 3 && pos[0] == "token" && pos[1] == "create":
		tok, err := a.CreateToken(ctx, pos[2])
		if err != nil {
			return err
		}
		fmt.Println(tok)
		return nil
	case len(pos) >= 1 && pos[0] == "reset-2fa":
		if len(pos) != 2 {
			return errors.New("usage: yawl server reset-2fa <email>")
		}
		return resetTwoStep(ctx, a, pos[1])
	case len(pos) >= 1 && pos[0] == "reset-password":
		if len(pos) != 2 {
			return errors.New("usage: yawl server reset-password <email>")
		}
		return resetPassword(ctx, a, o.publicURL, pos[1])
	case len(pos) == 1 && pos[0] == "claim-link":
		tok, err := a.ClaimLink(ctx)
		if err != nil {
			return err
		}
		fmt.Println(claimURL(o.publicURL, tok))
		return nil
	case len(pos) == 1 && pos[0] == "join-token":
		newToken := join.NewToken
		if o.panel {
			newToken = join.NewPanelToken
		}
		tok, err := newToken(ctx, st.Q, time.Now())
		if err != nil {
			return err
		}
		fmt.Printf("yawl agent join %s %s\n", o.publicURL, tok)
		return nil
	case len(pos) >= 3 && pos[0] == "telegram" && pos[1] == "set":
		if len(pos) < 4 {
			return errors.New("usage: yawl server telegram set <bot token> <chat id> [base url]")
		}
		v, err := vault.LoadOrCreate(o.etc)
		if err != nil {
			return err
		}
		if err := vault.SetSecret(ctx, st.Q, v, "telegram.token", []byte(pos[2])); err != nil {
			return err
		}
		st.Q.SetSetting(ctx, dbSetting("telegram.chat_id", pos[3]))
		if len(pos) == 5 {
			st.Q.SetSetting(ctx, dbSetting("telegram.base_url", pos[4]))
		}
		fmt.Println("Telegram is set.")
		return nil
	case len(pos) >= 2 && pos[0] == "storage" && pos[1] == "set":
		if len(pos) < 6 || len(pos) > 7 {
			return errors.New("usage: yawl server storage set <endpoint> <bucket> <access key> <secret key> [region]")
		}
		v, err := vault.LoadOrCreate(o.etc)
		if err != nil {
			return err
		}
		in := api.Storage{Endpoint: pos[2], Bucket: pos[3], AccessKey: pos[4], SecretKey: pos[5]}
		if len(pos) == 7 {
			in.Region = pos[6]
		}
		if err := api.SaveStorage(ctx, st.Q, v, in); err != nil {
			return err
		}
		fmt.Println("Backup storage is set.")
		return nil
	case len(pos) > 0:
		return fmt.Errorf("unknown server command %q", pos[0])
	}
	return serve(ctx, o, st, a)
}

func serve(ctx context.Context, o opts, st *store.Store, a *app.App) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	log := slog.Default()
	ca, err := join.LoadOrCreateCA(o.etc)
	if err != nil {
		return err
	}
	v, err := vault.LoadOrCreate(o.etc)
	if err != nil {
		return err
	}
	var cellGateway netip.Addr
	if o.managed {
		hosts, err := egress.HostAddrs(os.Getenv("YAWL_HOST_ADDRS"))
		if err != nil {
			return err
		}
		egress.Host = hosts
		egress.Control = egress.Public // every http.Client without its own Transport dials through this one
		http.DefaultTransport.(*http.Transport).DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: egress.Public}).DialContext
		if b, err := os.ReadFile("/proc/net/route"); err == nil {
			cellGateway = defaultGateway(string(b))
		}
	}
	gw := gateway.New(st.Q, ca, log)
	gw.Version = o.version
	gh := &github.Client{API: setting(ctx, st, "github.api_url", "https://api.github.com"),
		Web: setting(ctx, st, "github.web_url", "https://github.com"), HTTP: &http.Client{Timeout: 30 * time.Second}, Now: time.Now}
	exe, _ := os.Executable()
	self := &workers.SelfBackup{St: st, V: v, Data: o.data, Etc: o.etc, Now: time.Now}
	tokens := forgeTokens{gh: github.Tokens{C: gh, Q: st.Q, V: v}, gl: gitlab.Tokens{Q: st.Q, V: v}, gt: gitea.Tokens{Q: st.Q, V: v}}
	mux := http.NewServeMux()
	mux.Handle("POST /agent/join", join.HandlerLimited(st, ca, a.Limits.Servers, gw.Drop))
	mux.Handle("/agent", gw.Handler())
	mux.Handle("GET /agent/imports/{id}", api.ImportFile(a))
	mux.Handle("/agent/", http.NotFoundHandler())
	apiH := api.New(api.Deps{App: a, GW: gw, PublicURL: o.publicURL, Vault: v,
		TelegramBase: setting(ctx, st, "telegram.base_url", notify.TelegramAPI), Tokens: tokens, RunSelfBackup: self.Run, Terms: gatewayTerms{gw},
		ImportDir: filepath.Join(o.data, "imports"), Managed: o.managed, InstallURL: o.installURL, Releases: o.releaseBase, TrustedProxy: cellGateway,
		Version: o.version, Exe: exe, Restart: func() { time.AfterFunc(time.Second, stop) },
		Updater: func() (update.Updater, error) { return update.Default(o.releaseBase) }})
	mux.Handle("/api/", apiH)
	mux.Handle("/auth/sso/", apiH)
	ghh := api.GitHub(a, v, gh, o.publicURL)
	mux.Handle("/github/", ghh)
	mux.Handle("POST /api/github/connect", ghh)
	mux.Handle("GET /api/github", ghh)
	mux.Handle("GET /api/github/repos", ghh)
	mux.Handle("/gitlab/", api.GitLabHook(a, v))
	mux.Handle("/gitea/", api.GiteaHook(a, v))
	mux.HandleFunc("GET /llms.txt", agentskill.ServeText)
	mux.Handle("/", web.Handler())
	srv := &http.Server{Addr: o.listen, Handler: api.Headers(mux), ReadHeaderTimeout: 10 * time.Second}

	runner := workers.NewRunner(st, gw, log)
	runner.Tokens = tokens
	runner.PublicURL = o.publicURL
	runner.ImportDir = filepath.Join(o.data, "imports")
	go every(ctx, 10*time.Minute, func() { runner.SweepUploads(ctx, time.Now()) })
	cleanup := &workers.Cleanup{St: st, GW: gw, Now: time.Now}
	go every(ctx, time.Minute, func() { cleanup.Tick(ctx) })
	runner.RestoreKeys, runner.V = a, v
	outbox := &workers.Outbox{St: st, Notifier: workers.TelegramFromSettings(st, v), Extras: workers.ExtrasFromSettings(st, v), PublicURL: o.publicURL,
		Watch: func(ctx context.Context) (watch.Client, bool) { return watch.ClientFromSettings(ctx, st.Q, v) }}
	go every(ctx, time.Second, func() { runner.Tick(ctx) })
	go every(ctx, 5*time.Second, func() { outbox.Tick(ctx, time.Now()) })
	probes := &workers.Watch{St: st, GW: gw, App: a}
	go every(ctx, 30*time.Second, func() {
		if err := probes.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("watch", "err", err)
		}
	})
	domains := &workers.Domains{St: st, GW: gw}
	go every(ctx, time.Minute, func() {
		if err := domains.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("domains", "err", err)
		}
	})
	go every(ctx, time.Minute, func() {
		if err := self.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("panel backup", "err", err)
		}
	})
	move := &workers.Move{St: st, App: a, GW: gw, V: v, Now: time.Now}
	go every(ctx, 2*time.Second, func() {
		if err := move.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("environment move", "err", err)
		}
	})
	panel := &workers.Panel{St: st, GW: gw, Upstream: o.listen, PublicURL: o.publicURL, Now: time.Now}
	go every(ctx, time.Minute, func() {
		if err := panel.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("panel route", "err", err)
		}
	})
	watcher := &workers.Watcher{St: st, V: v, GW: gw, Code: worker.Code, Now: time.Now}
	go every(ctx, time.Minute, func() {
		if err := watcher.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("watcher", "err", err)
		}
	})
	statusPages := &workers.StatusPages{St: st, GW: gw, V: v, Now: time.Now}
	go every(ctx, time.Minute, func() {
		if err := statusPages.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("status pages", "err", err)
		}
	})
	cronWatch := &workers.CronWatch{St: st, GW: gw, Now: time.Now}
	go every(ctx, time.Minute, func() {
		if err := cronWatch.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("cron jobs", "err", err)
		}
	})
	metrics := &workers.Metrics{St: st, GW: gw, Now: time.Now, PanelURL: o.publicURL}
	go every(ctx, time.Minute, func() {
		if err := metrics.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("metrics", "err", err)
		}
	})
	trash := &workers.Trash{St: st, GW: gw, App: a, V: v, Now: time.Now}
	go every(ctx, time.Minute, func() {
		if err := trash.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("trash", "err", err)
		}
	})
	prune := &workers.Prune{St: st, Now: time.Now}
	go every(ctx, time.Minute, func() {
		if err := prune.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("audit prune", "err", err)
		}
	})
	previews := &workers.Previews{St: st, App: a, GH: forgeComments{gh: github.Comments{C: gh, Q: st.Q, V: v}, gl: gitlab.Comments{Q: st.Q, V: v}, gt: gitea.Comments{Q: st.Q, V: v}}, Now: time.Now, PanelURL: o.publicURL}
	go every(ctx, time.Minute, func() {
		if err := previews.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("previews", "err", err)
		}
	})
	statuses := &workers.CommitStatus{St: st, PanelURL: o.publicURL, Now: time.Now,
		S: forgeStatuses{gh: github.Statuses{C: gh, Q: st.Q, V: v}, gl: gitlab.Statuses{Q: st.Q, V: v}, gt: gitea.Statuses{Q: st.Q, V: v}}}
	go every(ctx, 5*time.Second, func() {
		if err := statuses.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("commit status", "err", err)
		}
	})
	pitr := &workers.PITR{St: st, GW: gw, V: v, Now: time.Now}
	go every(ctx, time.Minute, func() {
		if err := pitr.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("pitr", "err", err)
		}
	})
	firewall := &workers.Firewall{St: st, GW: gw, App: a, Now: time.Now}
	go every(ctx, time.Minute, func() {
		if err := firewall.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("firewall", "err", err)
		}
	})
	network := &workers.Network{St: st, GW: gw, Now: time.Now}
	go every(ctx, time.Minute, func() {
		if err := network.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("network", "err", err)
		}
	})
	backups := &workers.Backups{St: st, GW: gw, V: v, Now: time.Now}
	go every(ctx, 30*time.Second, func() {
		if err := backups.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Error("backups", "err", err)
		}
	})

	if o.claimHash != "" {
		if err := a.ClaimFromHash(ctx, o.claimHash); err != nil {
			return err
		}
	}
	if has, err := a.HasOwner(ctx); err == nil && !has {
		if !a.ClaimPending(ctx) {
			tok, err := a.ClaimLink(ctx)
			if err != nil {
				return err
			}
			log.Info("Claim this panel: " + claimURL(o.publicURL, tok))
		} else {
			log.Info("This panel has no owner yet. Run yawl server claim-link for the link.")
		}
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("yawl server listening", "addr", o.listen)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = srv.Shutdown(shut)
	gw.Close()
	runner.Wait()
	return err
}

func resetPassword(ctx context.Context, a *app.App, public, email string) error {
	tok, err := a.PasswordResetLink(ctx, email)
	if err != nil {
		return err
	}
	app.Event(ctx, a.St.Q, a.Now().UnixMilli(), "audit", "reset-password", "server command",
		map[string]any{"text": "made a password reset link for a member", "path": "yawl server reset-password", "status": 200})
	fmt.Println(strings.TrimRight(public, "/") + "/reset#" + tok)
	return nil
}

// claimURL carries the token in the fragment, so no access log ever holds it.
func claimURL(public, tok string) string { return strings.TrimRight(public, "/") + "/claim#" + tok }

func every(ctx context.Context, d time.Duration, fn func()) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		fn()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func setting(ctx context.Context, st *store.Store, key, def string) string {
	if v, err := st.Q.GetSetting(ctx, key); err == nil && v != "" {
		return v
	}
	return def
}

func dbSetting(k, v string) db.SetSettingParams { return db.SetSettingParams{Key: k, Value: v} }

func resetTwoStep(ctx context.Context, a *app.App, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	o, err := a.St.Q.GetOwnerByEmail(ctx, email)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("No member with the email %s.", email)
	} else if err != nil {
		return err
	}
	if err := a.ResetTwoStep(ctx, o.ID); err != nil {
		return err
	}
	app.Event(ctx, a.St.Q, a.Now().UnixMilli(), "audit", "reset-2fa", "server command",
		map[string]any{"text": "turned off two-step sign in for " + email, "path": "yawl server reset-2fa", "status": 200})
	fmt.Printf("Two-step sign in is off for %s. Their sessions have ended.\n", email)
	return nil
}
