package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/cloud"
)

const (
	frontAddr = "localhost:7200"
	ghAddr    = "localhost:7201"
	cellsAddr = "localhost:7300"
	prefix    = "yawl-clouddev-"
)

type dockerCells struct {
	rt     *runtime.Docker
	binary string
	mu     sync.Mutex
	ports  map[string]int
	next   int
}

func (d *dockerCells) Ensure(context.Context) error { return nil }

func (d *dockerCells) Create(ctx context.Context, c cloud.Cell) error {
	d.mu.Lock()
	port, ok := d.ports[c.Name]
	if !ok {
		d.next++
		port = 7400 + d.next
		d.ports[c.Name] = port
	}
	d.mu.Unlock()
	d.Delete(ctx, c.Name)
	_, err := d.rt.Start(ctx, runtime.ContainerSpec{Name: prefix + c.Name, Image: "alpine:3", Network: "bridge",
		Mounts: []runtime.Mount{{Source: d.binary, Target: "/usr/local/bin/yawl", ReadOnly: true}},
		Cmd:    []string{"/usr/local/bin/yawl", "server", "--listen", "0.0.0.0:7000", "--data", "/data", "--etc", "/data/etc"},
		Env: []string{"YAWL_PUBLIC_URL=" + c.PublicURL, "YAWL_MANAGED=1", "YAWL_CLAIM_HASH=" + c.ClaimHash, "YAWL_INSTALL_URL=" + c.InstallURL,
			"YAWL_RELEASES=" + c.Releases, "YAWL_LIMITS=" + cloud.CellLimits},
		Ports: map[int]int{port: 7000}, PortIP: map[int]string{port: "127.0.0.1"}})
	return err
}

func (d *dockerCells) Delete(ctx context.Context, name string) error {
	d.rt.Stop(ctx, prefix+name, 2*time.Second)
	return d.rt.Remove(ctx, prefix+name)
}

func (d *dockerCells) State(ctx context.Context, name string) (string, error) {
	st, err := d.rt.Inspect(ctx, prefix+name)
	if err != nil || !st.Running {
		return "failed", nil
	}
	d.mu.Lock()
	port := d.ports[name]
	d.mu.Unlock()
	res, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/session", port))
	if err != nil {
		return "starting", nil
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		return "starting", nil
	}
	return "live", nil
}

func (d *dockerCells) SetImage(context.Context, string, string) error { return nil }

func fakeGitHub() http.Handler {
	people := map[string]string{"behzod": `{"id":7,"login":"behzod","email":"behzod@example.com"}`,
		"aziz": `{"id":8,"login":"aziz","email":"aziz@example.com"}`, "nodira": `{"id":9,"login":"nodira","email":"nodira@example.com"}`}
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		back, state := r.URL.Query().Get("redirect_uri"), r.URL.Query().Get("state")
		var b strings.Builder
		b.WriteString(`<!doctype html><body style="font:16px system-ui;max-width:420px;margin:80px auto"><h2>GitHub (local stand-in)</h2><p>Sign in to Yawl Cloud as:</p>`)
		for _, who := range []string{"behzod", "aziz", "nodira"} {
			fmt.Fprintf(&b, `<p><a href="%s?code=%s&state=%s">%s</a></p>`, html.EscapeString(back), who, url.QueryEscape(state), who)
		}
		w.Write([]byte(b.String()))
	})
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		fmt.Fprintf(w, `{"access_token":"tok-%s"}`, r.Form.Get("code"))
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		who := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer tok-")
		if people[who] == "" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(people[who]))
	})
	return mux
}

func cellsProxy(d *dockerCells) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, _, _ := strings.Cut(r.Host, ".")
		d.mu.Lock()
		port, ok := d.ports[name]
		d.mu.Unlock()
		if !ok {
			http.Error(w, "no cell "+name, http.StatusNotFound)
			return
		}
		target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
		httputil.NewSingleHostReverseProxy(target).ServeHTTP(w, r)
	})
}

func main() {
	binary := os.Getenv("CLOUDDEV_BINARY")
	if binary == "" {
		log.Fatal("set CLOUDDEV_BINARY to a linux build of yawl: GOOS=linux GOARCH=$(go env GOARCH) CGO_ENABLED=0 go build -o /tmp/yawl-linux ./cmd/yawl")
	}
	binary, _ = filepath.Abs(binary)
	rt, err := runtime.NewDocker()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cells := &dockerCells{rt: rt, binary: binary, ports: map[string]int{}}
	if cs, err := rt.List(ctx, nil); err == nil {
		for _, c := range cs {
			if strings.HasPrefix(c.Name, prefix) {
				rt.Stop(ctx, c.ID, time.Second)
				rt.Remove(ctx, c.ID)
			}
		}
	}
	data, _ := os.MkdirTemp("", "clouddev")
	front, err := cloud.NewFront(cloud.Config{PublicURL: "http://" + frontAddr, Data: data, CellHost: "{name}." + cellsAddr, CellScheme: "http",
		Image: "yawl:dev", InstallURL: "http://" + frontAddr + "/install.sh", Releases: "http://" + frontAddr + "/releases",
		GitHubID: "dev", GitHubSecret: "dev", GitHubWeb: "http://" + ghAddr, GitHubAPI: "http://" + ghAddr, AdminToken: "dev", MaxCells: 50, Cells: cells})
	if err != nil {
		log.Fatal(err)
	}
	for addr, h := range map[string]http.Handler{frontAddr: front, ghAddr: fakeGitHub(), cellsAddr: cellsProxy(cells)} {
		srv := &http.Server{Addr: addr, Handler: h}
		go func() {
			if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				log.Fatal(err)
			}
		}()
	}
	log.Printf("Yawl Cloud on http://%s (cells at http://<name>.%s); Ctrl+C stops it and removes the cells", frontAddr, cellsAddr)
	<-ctx.Done()
	for name := range cells.ports {
		cells.Delete(context.Background(), name)
	}
	os.RemoveAll(data)
}
