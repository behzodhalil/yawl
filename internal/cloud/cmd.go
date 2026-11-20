package cloud

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

func configFrom(args []string, getenv func(string) string) (Config, string, error) {
	fs := flag.NewFlagSet("cloud", flag.ContinueOnError)
	listen := fs.String("listen", "", "address to serve on (YAWL_LISTEN; 0.0.0.0:7000)")
	data := fs.String("data", "", "state directory (YAWL_CLOUD_DATA; /data)")
	if err := fs.Parse(args); err != nil {
		return Config{}, "", err
	}
	or := func(v, env, def string) string {
		if v != "" {
			return v
		}
		if e := getenv(env); e != "" {
			return e
		}
		return def
	}
	var missing []string
	need := func(env string) string {
		v := getenv(env)
		if v == "" {
			missing = append(missing, env)
		}
		return v
	}
	pub := strings.TrimRight(need("YAWL_CLOUD_PUBLIC_URL"), "/")
	cfg := Config{PublicURL: pub, Data: or(*data, "YAWL_CLOUD_DATA", "/data"), CellHost: need("YAWL_CLOUD_CELL_HOST"), Image: need("YAWL_CLOUD_IMAGE"),
		InstallURL: or("", "YAWL_CLOUD_INSTALL_URL", pub+"/install.sh"), Releases: or("", "YAWL_CLOUD_RELEASES", pub+"/releases"),
		GitHubID: need("YAWL_CLOUD_GITHUB_ID"), GitHubSecret: need("YAWL_CLOUD_GITHUB_SECRET"),
		GitHubWeb: or("", "YAWL_CLOUD_GITHUB_WEB", "https://github.com"), GitHubAPI: or("", "YAWL_CLOUD_GITHUB_API", "https://api.github.com"),
		AdminToken: need("YAWL_CLOUD_ADMIN_TOKEN"), InviteOnly: getenv("YAWL_CLOUD_INVITE_ONLY") == "1"}
	opURL, opToken := need("YAWL_CLOUD_OPERATOR_URL"), need("YAWL_CLOUD_OPERATOR_TOKEN")
	list := getenv("YAWL_CLOUD_SERVERS")
	server := getenv("YAWL_CLOUD_SERVER")
	if list == "" {
		server = need("YAWL_CLOUD_SERVER")
	}
	if len(missing) > 0 {
		return Config{}, "", fmt.Errorf("yawl cloud needs %s", strings.Join(missing, ", "))
	}
	if !strings.Contains(cfg.CellHost, "{name}") {
		return Config{}, "", errors.New("YAWL_CLOUD_CELL_HOST must hold {name}, like panel-{name}.198-51-100-20.sslip.io")
	}
	n, err := strconv.Atoi(or("", "YAWL_CLOUD_MAX_CELLS", "50"))
	if err != nil || n < 1 {
		return Config{}, "", errors.New("YAWL_CLOUD_MAX_CELLS is a number of cells")
	}
	cfg.MaxCells = n
	if cfg.Servers, err = serversFrom(list, server, n); err != nil {
		return Config{}, "", err
	}
	for _, s := range cfg.Servers {
		if s.IP == "" {
			return Config{}, "", fmt.Errorf("YAWL_CLOUD_SERVERS needs each cell server's public IPv4, like %s:%d@203.0.113.2; %s has none", s.Name, s.Cap, s.Name)
		}
	}
	cfg.Cells = Operator{Base: opURL, Token: opToken, Server: cfg.Servers[0].Name,
		Project: or("", "YAWL_CLOUD_PROJECT", "cells")}
	if tok, zone := getenv("YAWL_CLOUD_DNS_TOKEN"), getenv("YAWL_CLOUD_DNS_ZONE"); tok != "" || zone != "" {
		if tok == "" || zone == "" {
			return Config{}, "", errors.New("the cells' records need both YAWL_CLOUD_DNS_TOKEN and YAWL_CLOUD_DNS_ZONE (Cloudflare's zone id)")
		}
		cfg.DNS = Cloudflare{API: or("", "YAWL_CLOUD_DNS_API", "https://api.cloudflare.com/client/v4"), Token: tok, Zone: zone}
	}
	if err := dodoFrom(&cfg, getenv, or); err != nil {
		return Config{}, "", err
	}
	for _, c := range strings.Split(getenv("YAWL_CLOUD_INVITES"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			cfg.Invites = append(cfg.Invites, c)
		}
	}
	return cfg, or(*listen, "YAWL_LISTEN", "0.0.0.0:7000"), nil
}

func serversFrom(list, server string, max int) ([]CellServer, error) {
	if list == "" {
		return []CellServer{{Name: server, Cap: max}}, nil
	}
	var out []CellServer
	for _, e := range strings.Split(list, ",") {
		e = strings.TrimSpace(e)
		nameCap, ip, _ := strings.Cut(e, "@")
		name, c, _ := strings.Cut(nameCap, ":")
		n, err := strconv.Atoi(c)
		if a, perr := netip.ParseAddr(ip); name == "" || err != nil || n < 0 || ip != "" && (perr != nil || !a.Is4()) {
			return nil, fmt.Errorf("YAWL_CLOUD_SERVERS: %q is not name:cap or name:cap@ipv4, like cell-b:150@203.0.113.2", e)
		}
		out = append(out, CellServer{Name: name, IP: ip, Cap: n})
	}
	if server != "" && out[0].Name != server {
		return nil, fmt.Errorf("YAWL_CLOUD_SERVER %s must be the first of YAWL_CLOUD_SERVERS: its cells are there", server)
	}
	return out, nil
}

func Run(ctx context.Context, args []string) error {
	cfg, listen, err := configFrom(args, os.Getenv)
	if err != nil {
		return err
	}
	ectx, cancel := context.WithTimeout(ctx, time.Minute)
	err = cfg.Cells.Ensure(ectx)
	cancel()
	if err != nil {
		return fmt.Errorf("the operator's project for cells: %w", err)
	}
	h, err := NewFront(cfg)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: listen, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	slog.Info("yawl cloud listening", "addr", listen, "public", cfg.PublicURL)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func dodoFrom(cfg *Config, getenv func(string) string, or func(v, env, def string) string) error {
	if cfg.DodoKey = getenv("YAWL_CLOUD_DODO_API_KEY"); cfg.DodoKey == "" {
		return nil
	}
	cfg.DodoSecret, cfg.DodoProduct = getenv("YAWL_CLOUD_DODO_WEBHOOK_SECRET"), getenv("YAWL_CLOUD_DODO_PRODUCT")
	var missing []string
	for k, v := range map[string]string{"YAWL_CLOUD_DODO_WEBHOOK_SECRET": cfg.DodoSecret, "YAWL_CLOUD_DODO_PRODUCT": cfg.DodoProduct} {
		if v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("YAWL_CLOUD_DODO_API_KEY needs %s", strings.Join(missing, ", "))
	}
	mode := or("", "YAWL_CLOUD_DODO_MODE", "test")
	if mode != "test" && mode != "live" {
		return errors.New("YAWL_CLOUD_DODO_MODE is test or live")
	}
	cfg.DodoAPI = or("", "YAWL_CLOUD_DODO_API", "https://"+mode+".dodopayments.com")
	cfg.Price = or("", "YAWL_CLOUD_PRICE", "$3.99 a month")
	return nil
}
