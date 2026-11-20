//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	dockerclient "github.com/moby/moby/client"

	"github.com/behzodhalil/yawl/internal/agent"
	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/cli"
	"github.com/behzodhalil/yawl/internal/server"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/join"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

const listen = "127.0.0.1:7070"

type box struct {
	t          *testing.T
	dir        string
	repo       *git.Repository
	repoDir    string
	stopServer context.CancelFunc
	telegram   *telegramFake
}

type telegramFake struct {
	mu   sync.Mutex
	msgs []string
}

func (f *telegramFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var m struct{ Text string }
	json.NewDecoder(r.Body).Decode(&m)
	f.mu.Lock()
	f.msgs = append(f.msgs, m.Text)
	f.mu.Unlock()
	io.WriteString(w, `{"ok":true}`)
}

func (f *telegramFake) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.msgs...)
}

func (b *box) startServer() {
	ctx, cancel := context.WithCancel(context.Background())
	b.stopServer = cancel
	go server.Run(ctx, []string{"--data", filepath.Join(b.dir, "data"), "--etc", filepath.Join(b.dir, "etc"), "--listen", listen}, "dev")
	waitFor(b.t, 10*time.Second, "server up", func() bool {
		res, err := http.Get("http://" + listen + "/api/status")
		if err == nil {
			res.Body.Close()
		}
		return err == nil
	})
}

func (b *box) store() *store.Store {
	st, err := store.Open(filepath.Join(b.dir, "data", "yawl.db"))
	if err != nil {
		b.t.Fatal(err)
	}
	b.t.Cleanup(func() { st.Close() })
	return st
}

func setup(t *testing.T) *box { return setupApp(t, "hello") }

func setupApp(t *testing.T, appName string) *box {
	dir, err := os.MkdirTemp("", "yawl-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Cleanup(func() { removeApps(t) })
	b := &box{t: t, dir: dir, telegram: &telegramFake{}}
	b.startServer()
	t.Cleanup(func() { b.stopServer() })

	st, ctx := b.store(), context.Background()
	tg := httptest.NewServer(b.telegram)
	t.Cleanup(tg.Close)
	v, err := vault.LoadOrCreate(filepath.Join(b.dir, "etc"))
	if err != nil {
		t.Fatal(err)
	}
	vault.SetSecret(ctx, st.Q, v, "telegram.token", []byte("T"))
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "telegram.chat_id", Value: "1"})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "telegram.base_url", Value: tg.URL})

	jt, err := join.NewToken(ctx, st.Q, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(dir, "agent")
	if err := agent.Run(ctx, []string{"join", "http://" + listen, jt, "--data", agentDir, "--name", "e2e", "--ip", "127.0.0.1"}, "e2e"); err != nil {
		t.Fatal(err)
	}
	actx, stopAgent := context.WithCancel(context.Background())
	t.Cleanup(stopAgent)
	go agent.Run(actx, []string{"--data", agentDir}, "e2e")

	tok, err := app.New(st).CreateToken(ctx, "e2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("YAWL_CONFIG", filepath.Join(dir, "cli.json"))
	if err := cli.Run(ctx, "login", []string{"http://" + listen, "--token", tok}); err != nil {
		t.Fatal(err)
	}

	b.repoDir = filepath.Join(dir, "app")
	os.MkdirAll(b.repoDir, 0o755)
	src := "../../testdata/apps/" + appName
	entries, _ := os.ReadDir(src)
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(src, e.Name()))
		os.WriteFile(filepath.Join(b.repoDir, e.Name()), data, 0o644)
	}
	b.repo, _ = git.PlainInit(b.repoDir, false)
	b.commit("ok", "v1")
	os.WriteFile(filepath.Join(b.repoDir, "yawl.yaml"), []byte(fmt.Sprintf(`project: e2e
environment: production
server: e2e
services:
  - name: hello
    kind: web
    repo: file://%s
    branch: master
    drain_timeout: 3s
    health_timeout: 30s
`, b.repoDir)), 0o644)
	t.Chdir(b.repoDir)
	if err := cli.Run(ctx, "apply", []string{"--yes"}); err != nil {
		t.Fatal(err)
	}
	return b
}

var optionalAppFiles = []string{"main.go", "Dockerfile", "go.mod", "package.json", "vite.config.js", "build.js"}

func (b *box) commit(mode, version string) {
	os.WriteFile(filepath.Join(b.repoDir, "MODE"), []byte(mode+"\n"), 0o644)
	os.WriteFile(filepath.Join(b.repoDir, "VERSION"), []byte(version+"\n"), 0o644)
	w, _ := b.repo.Worktree()
	for _, f := range []string{"MODE", "VERSION"} {
		if _, err := w.Add(f); err != nil {
			b.t.Fatal(err)
		}
	}
	for _, f := range optionalAppFiles {
		_, err := os.Stat(filepath.Join(b.repoDir, f))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			b.t.Fatal(err)
		}
		if _, err := w.Add(f); err != nil {
			b.t.Fatal(err)
		}
	}
	if _, err := w.Commit(mode+" "+version, &git.CommitOptions{Author: &object.Signature{Name: "e2e", Email: "e2e@yawl", When: time.Now()}}); err != nil {
		b.t.Fatal(err)
	}
}

func (b *box) deploy() error { return cli.Run(context.Background(), "deploy", []string{"hello"}) }

const host = "hello-production.127-0-0-1.sslip.io"

func get() (string, error) {
	req, _ := http.NewRequest("GET", "http://127.0.0.1/", nil)
	req.Host = host
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		return "", fmt.Errorf("status %d", res.StatusCode)
	}
	return strings.TrimSpace(string(body)), nil
}

type load struct {
	stop   chan struct{}
	done   chan struct{}
	ok     int
	failed int
	errs   []string
}

func startLoad(rps int) *load {
	l := &load{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(l.done)
		t := time.NewTicker(time.Second / time.Duration(rps))
		defer t.Stop()
		var mu sync.Mutex
		var wg sync.WaitGroup
		for {
			select {
			case <-l.stop:
				wg.Wait()
				return
			case <-t.C:
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := get()
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						l.failed++
						if len(l.errs) < 5 {
							l.errs = append(l.errs, err.Error())
						}
					} else {
						l.ok++
					}
				}()
			}
		}
	}()
	return l
}

func (l *load) end() { close(l.stop); <-l.done }

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestE2EGoodDeployZeroFailures(t *testing.T) {
	b := setup(t)
	if err := b.deploy(); err != nil {
		t.Fatalf("first deploy: %v", err)
	}
	if v, err := get(); v != "v1" {
		t.Fatalf("v1 not served: %q %v", v, err)
	}
	l := startLoad(50)
	b.commit("ok", "v2")
	err := b.deploy()
	time.Sleep(time.Second)
	l.end()
	if err != nil {
		t.Fatalf("second deploy: %v", err)
	}
	if l.failed != 0 {
		t.Fatalf("%d of %d requests failed during the switch: %v", l.failed, l.ok+l.failed, l.errs)
	}
	if v, _ := get(); v != "v2" {
		t.Fatalf("v2 not served: %q", v)
	}
	t.Logf("%d requests, 0 failed", l.ok)
}

func TestE2ECrashKeepsOld(t *testing.T) {
	b := setup(t)
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	l := startLoad(50)
	b.commit("crash", "v2")
	err := b.deploy()
	l.end()
	if err == nil || !strings.Contains(err.Error(), "jinja2") {
		t.Fatalf("want a failure naming jinja2, got %v", err)
	}
	if l.failed != 0 {
		t.Fatalf("%d requests failed: %v", l.failed, l.errs)
	}
	if v, _ := get(); v != "v1" {
		t.Fatalf("old version not served: %q", v)
	}
	waitFor(t, 15*time.Second, "telegram message", func() bool {
		for _, m := range b.telegram.all() {
			if strings.Contains(m, "jinja2") && strings.Contains(m, "The previous version is still live.") {
				return true
			}
		}
		return false
	})
}

func TestE2ELocalhostCause(t *testing.T) {
	b := setup(t)
	b.commit("localhost", "v1")
	err := b.deploy()
	if err == nil || !strings.Contains(err.Error(), "Your app listens on 127.0.0.1:10000. Bind to 0.0.0.0.") {
		t.Fatalf("got %v", err)
	}
}

func TestE2EServerDownAppsUp(t *testing.T) {
	b := setup(t)
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	st := b.store()
	count := func() int {
		var n int
		st.DB.QueryRow(`SELECT COUNT(*) FROM deploys`).Scan(&n)
		return n
	}
	before := count()
	b.stopServer()
	time.Sleep(time.Second)
	for i := 0; i < 20; i++ {
		if v, err := get(); v != "v1" {
			t.Fatalf("app down while server is down: %q %v", v, err)
		}
	}
	b.startServer()
	time.Sleep(3 * time.Second)
	if count() != before {
		t.Fatal("restarting the server created a deploy")
	}
	if v, _ := get(); v != "v1" {
		t.Fatalf("after restart: %q", v)
	}
}

const planted = "yawl-planted-7f3c9e"

func (b *box) withSecret() {
	ctx := context.Background()
	if err := cli.Run(ctx, "recovery-key", []string{"init"}); err != nil {
		b.t.Fatal(err)
	}
	if err := cli.Run(ctx, "env", []string{"set", "hello", "GREETING=" + planted}); err != nil {
		b.t.Fatal(err)
	}
}

func TestE2ESecretReachesApp(t *testing.T) {
	b := setup(t)
	b.withSecret()
	b.commit("env", "v1")
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	if v, err := get(); v != planted {
		t.Fatalf("app served %q (%v), want the sealed value", v, err)
	}
}

func TestE2EPlantedValueAbsent(t *testing.T) {
	var log bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedWriter{w: &log}, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	b := setup(t)
	b.withSecret()
	b.commit("env", "v1")
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	if v, _ := get(); v != planted {
		t.Fatal("setup: the value did not reach the app")
	}
	for _, f := range []string{"yawl.db", "yawl.db-wal", "yawl.db-shm"} {
		data, err := os.ReadFile(filepath.Join(b.dir, "data", f))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(planted)) {
			t.Fatalf("%s contains the secret", f)
		}
	}
	if strings.Contains(log.String(), planted) {
		t.Fatal("the server log contains the secret")
	}
}

func TestE2EDetectedGo(t *testing.T) {
	b := setupApp(t, "hello-go")
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	if v, err := get(); v != "v1" {
		t.Fatalf("detected app served %q (%v)", v, err)
	}
}

const buildSecret = "s3cr3t-build-value"

func TestE2EBuildSecret(t *testing.T) {
	var log bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedWriter{w: &log}, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	b := setupApp(t, "secret")
	ctx := context.Background()
	if err := cli.Run(ctx, "recovery-key", []string{"init"}); err != nil {
		t.Fatal(err)
	}
	if err := cli.Run(ctx, "env", []string{"set", "--build", "hello", "TOKEN=" + buildSecret}); err != nil {
		t.Fatal(err)
	}
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	if v, err := get(); v != "v1" {
		t.Fatalf("app served %q (%v)", v, err)
	}
	c, err := dockerclient.New(dockerclient.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := c.ImageSave(ctx, []string{"yawl/e2e-production-hello"}) // every tag: layers, config and history
	if err != nil {
		t.Fatal(err)
	}
	img, _ := io.ReadAll(rc)
	rc.Close()
	if len(img) == 0 || bytes.Contains(img, []byte(buildSecret)) {
		t.Fatalf("saved image (%d bytes) carries the build secret, or is empty", len(img))
	}
	for _, f := range []string{"yawl.db", "yawl.db-wal", "yawl.db-shm"} {
		data, err := os.ReadFile(filepath.Join(b.dir, "data", f))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(buildSecret)) {
			t.Fatalf("%s contains the build secret", f)
		}
	}
	if strings.Contains(log.String(), buildSecret) {
		t.Fatal("the server log contains the build secret")
	}
}

func TestE2ECrashMasksSecret(t *testing.T) {
	b := setup(t)
	b.withSecret()
	b.commit("crash", "v1")
	err := b.deploy()
	if err == nil || strings.Contains(err.Error(), planted) || !strings.Contains(err.Error(), "••••") {
		t.Fatalf("cause %v", err)
	}
	waitFor(t, 15*time.Second, "telegram message", func() bool {
		for _, m := range b.telegram.all() {
			if strings.Contains(m, planted) {
				t.Fatalf("telegram got the secret: %s", m)
			}
			if strings.Contains(m, "••••") {
				return true
			}
		}
		return false
	})
}

type lockedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func run(t *testing.T, in string, cmd string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cli.SetIO(&out, strings.NewReader(in))
	defer cli.SetIO(os.Stdout, os.Stdin)
	err := cli.Run(context.Background(), cmd, args)
	return out.String(), err
}

func (b *box) withDB() string {
	t := b.t
	dropDBs(t)
	t.Cleanup(func() { dropDBs(t) })
	f, _ := os.OpenFile(filepath.Join(b.repoDir, "yawl.yaml"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("  - name: notes-db\n    kind: postgres\n")
	f.Close()
	out, err := run(t, "", "recovery-key", "init")
	if err != nil {
		t.Fatal(err)
	}
	key := ""
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "yawl-recovery-") {
			key = l
		}
	}
	if _, err := run(t, "", "apply", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "", "deploy", "notes-db"); err != nil {
		t.Fatal(err)
	}
	return key
}

func removeApps(t *testing.T) {
	ctx := context.Background()
	c, err := dockerclient.New(dockerclient.FromEnv)
	if err != nil {
		return
	}
	for _, project := range []string{"e2e", "acme"} {
		res, err := c.ContainerList(ctx, dockerclient.ContainerListOptions{All: true, Filters: make(dockerclient.Filters).Add("label", "yawl.project="+project)})
		if err != nil {
			return
		}
		for _, ct := range res.Items {
			c.ContainerRemove(ctx, ct.ID, dockerclient.ContainerRemoveOptions{Force: true})
		}
	}
}

func dropDBs(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"notes-db", "notes-db-2"} {
		rt.Remove(ctx, "yawl-e2e-production-"+n)
		rt.C.VolumeRemove(ctx, "yawl-e2e-production-"+n+"-data", dockerclient.VolumeRemoveOptions{Force: true})
	}
}

func psql(t *testing.T, host, password, sql string) {
	t.Helper()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	exit, stderr, err := rt.RunOnce(context.Background(), runtime.ContainerSpec{Image: "postgres:17", Network: "yawl-e2e-production",
		Env: []string{"PGPASSWORD=" + password}, Cmd: []string{"psql", "-h", host, "-U", "app", "-d", "app", "-c", sql}}, nil, nil)
	if err != nil || exit != 0 {
		t.Fatalf("psql %q: exit %d %s %v", sql, exit, stderr, err)
	}
}

func s3(t *testing.T) {
	ctx := context.Background()
	rt, _ := runtime.NewDocker()
	id, err := rt.Start(ctx, runtime.ContainerSpec{Name: fmt.Sprintf("yawl-e2e-s3-%d", time.Now().UnixNano()), Image: "chrislusf/seaweedfs", HostNetwork: true,
		Env: []string{"AWS_ACCESS_KEY_ID=yawltest", "AWS_SECRET_ACCESS_KEY=yawltest-secret"},
		Cmd: []string{"server", "-dir=/data", "-ip=127.0.0.1", "-ip.bind=127.0.0.1", "-s3", "-s3.port=19000", "-master.port=19333", "-volume.port=19080",
			"-filer.port=19888", "-master.port.grpc=29333", "-volume.port.grpc=29080", "-filer.port.grpc=29888", "-s3.port.grpc=29000"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, id) })
	c, _ := minio.New("127.0.0.1:19000", &minio.Options{Creds: credentials.NewStaticV4("yawltest", "yawltest-secret", ""), Region: "us-east-1"})
	waitFor(t, 30*time.Second, "s3 up", func() bool { return c.MakeBucket(ctx, "backups", minio.MakeBucketOptions{Region: "us-east-1"}) == nil })
}

func TestE2EBackupRestore(t *testing.T) {
	var log bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedWriter{w: &log}, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	b := setupApp(t, "pgnote")
	key := b.withDB()
	if _, err := run(t, "", "env", "set", "hello", "DATABASE_URL", "--ref", "notes-db.url"); err != nil {
		t.Fatal(err)
	}
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	if v, err := get(); v != "no notes" {
		t.Fatalf("app served %q (%v)", v, err)
	}
	password, err := run(t, "", "env", "reveal", "notes-db", "PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	password = strings.TrimSpace(password)
	psql(t, "notes-db", password, "INSERT INTO notes VALUES ('from-before-backup')")
	if v, _ := get(); v != "from-before-backup" {
		t.Fatalf("after insert the app served %q", v)
	}

	s3(t)
	if err := server.Run(context.Background(), []string{"--data", filepath.Join(b.dir, "data"), "--etc", filepath.Join(b.dir, "etc"),
		"storage", "set", "http://127.0.0.1:19000", "backups", "yawltest", "yawltest-secret", "us-east-1"}, "dev"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "", "db", "backup", "notes-db")
	if err != nil {
		t.Fatalf("backup: %v\n%s", err, out)
	}
	id := strings.Fields(out)[1]
	psql(t, "notes-db", password, "DELETE FROM notes")
	if v, _ := get(); v != "no notes" {
		t.Fatalf("after delete the app served %q", v)
	}

	if out, err := run(t, key+"\n", "db", "restore", "notes-db", id, "--into", "notes-db-2"); err != nil {
		t.Fatalf("restore: %v\n%s", err, out)
	}
	if _, err := run(t, "", "env", "set", "hello", "DATABASE_URL", "--ref", "notes-db-2.url"); err != nil {
		t.Fatal(err)
	}
	l := startLoad(20)
	err = b.deploy()
	time.Sleep(time.Second)
	l.end()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d requests, %d failed", l.ok+l.failed, l.failed)
	if l.failed > 0 {
		t.Fatalf("%d failed requests during the switch: %v", l.failed, l.errs)
	}
	if v, _ := get(); v != "from-before-backup" {
		t.Fatalf("on the restored database the app served %q", v)
	}
	for _, f := range []string{"yawl.db", "yawl.db-wal", "yawl.db-shm"} {
		data, err := os.ReadFile(filepath.Join(b.dir, "data", f))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		for _, secret := range []string{password, "yawltest-secret", key} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatalf("%s contains a secret (%.6s…)", f, secret)
			}
		}
	}
	for _, secret := range []string{password, "yawltest-secret", key} {
		if strings.Contains(log.String(), secret) {
			t.Fatalf("the server log contains a secret (%.6s…)", secret)
		}
	}
}

func TestE2EMissedBackupMessage(t *testing.T) {
	b := setupApp(t, "pgnote")
	b.withDB()
	st := b.store()
	st.DB.Exec(`UPDATE services SET created_at = ? WHERE name = 'notes-db'`, time.Now().Add(-27*time.Hour).UnixMilli())
	waitFor(t, 75*time.Second, "missed-backup message", func() bool {
		for _, m := range b.telegram.all() {
			if strings.Contains(m, "notes-db: no backup has succeeded in 26 hours") && strings.Contains(m, "Backup storage is not set") {
				return true
			}
		}
		return false
	})
}
