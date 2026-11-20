//go:build hetzner

package hetzner

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"golang.org/x/crypto/ssh"

	"github.com/behzodhalil/yawl/internal/seal"
)

const greeting = "hello-from-hetzner-acceptance"

type box struct {
	t   *testing.T
	ssh *ssh.Client
	ip  string
	hc  *http.Client
	pan string
}

// runDeadline ends the run well before go test's -timeout, which panics and
// skips t.Cleanup: the server would be left running and billing.
var runDeadline time.Time

const (
	runTimeWithoutTestDeadline     = 70 * time.Minute
	cleanupMarginBeforeTestTimeout = 20 * time.Minute
)

func runDeadlineFor(t *testing.T) time.Time {
	if d, ok := t.Deadline(); ok {
		return d.Add(-cleanupMarginBeforeTestTimeout)
	}
	return time.Now().Add(runTimeWithoutTestDeadline)
}

func (b *box) run(cmd string, stdin io.Reader) string {
	b.t.Helper()
	s, err := b.ssh.NewSession()
	if err != nil {
		b.t.Fatal(err)
	}
	defer s.Close()
	s.Stdin = stdin
	var out bytes.Buffer
	s.Stdout, s.Stderr = &out, &out
	if err := s.Start(cmd); err != nil {
		b.t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Wait() }()
	limit := min(15*time.Minute, time.Until(runDeadline))
	select {
	case err = <-done:
	case <-time.After(limit):
		s.Close()
		b.t.Fatalf("%s: no answer in %s", cmd, limit)
	}
	if err != nil {
		b.t.Fatalf("%s: %v\n%s", cmd, err, out.String())
	}
	return out.String()
}

func (b *box) upload(local, remote string, mode int) {
	f, err := os.Open(local)
	if err != nil {
		b.t.Fatal(err)
	}
	defer f.Close()
	b.run(fmt.Sprintf("install -D -m %o /dev/stdin %s", mode, remote), f)
}

func (b *box) panel(method, path string, in, out any) int {
	b.t.Helper()
	var body io.Reader
	if in != nil {
		j, _ := json.Marshal(in)
		body = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, b.pan+path, body)
	req.Header.Set("X-Yawl-Request", "1")
	res, err := b.hc.Do(req)
	if err != nil {
		return 0
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if out != nil {
		json.Unmarshal(raw, out)
	}
	if res.StatusCode/100 != 2 {
		b.t.Logf("%s %s: %d %s", method, path, res.StatusCode, raw)
	}
	return res.StatusCode
}

func wait(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	if !runDeadline.IsZero() && end.After(runDeadline) {
		end = runDeadline
	}
	for !ok() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(3 * time.Second)
	}
}

func helloRepo(t *testing.T) []byte {
	dir := t.TempDir()
	src := "../../testdata/apps/hello"
	es, _ := os.ReadDir(src)
	for _, e := range es {
		b, _ := os.ReadFile(filepath.Join(src, e.Name()))
		os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644)
	}
	os.WriteFile(filepath.Join(dir, "MODE"), []byte("env\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("v1\n"), 0o644)
	r, _ := git.PlainInit(dir, false)
	w, _ := r.Worktree()
	w.AddGlob(".")
	if _, err := w.Commit("v1", &git.CommitOptions{All: true, Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		rel, _ := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return err
		}
		fi, _ := d.Info()
		h, _ := tar.FileInfoHeader(fi, "")
		h.Name = rel
		tw.WriteHeader(h)
		if !d.IsDir() {
			b, _ := os.ReadFile(p)
			tw.Write(b)
		}
		return nil
	})
	tw.Close()
	return buf.Bytes()
}

func TestAcceptance(t *testing.T) {
	token := os.Getenv("HCLOUD_TOKEN")
	if token == "" {
		t.Skip("HCLOUD_TOKEN is not set: this run creates a Hetzner server, so it needs your token")
	}
	dist := os.Getenv("YAWL_HZ_DIST")
	a, bv := os.Getenv("YAWL_HZ_A"), os.Getenv("YAWL_HZ_B")
	if dist == "" || a == "" || bv == "" {
		t.Fatal("run through `make hetzner`, which builds the two releases")
	}
	runDeadline = runDeadlineFor(t)
	ctx, cancel := context.WithDeadline(context.Background(), runDeadline)
	defer cancel()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	sshPub, _ := ssh.NewPublicKey(pub)
	name := "yawl-accept-" + strconv.FormatInt(time.Now().Unix(), 10)
	srv, err := Provision(ctx, Client{Base: API, Token: token}, t, ServerOpts{Name: name, Type: "cx22", Image: "ubuntu-24.04",
		Location: "hel1", PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	t.Logf("server %s at %s", name, srv.IP)
	b := &box{t: t, ip: srv.IP}
	wait(t, 3*time.Minute, "ssh", func() bool {
		c, err := ssh.Dial("tcp", srv.IP+":22", &ssh.ClientConfig{User: "root", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second}) // a box made seconds ago: no known host key to check
		if err == nil {
			b.ssh = c
		}
		return err == nil
	})
	defer b.ssh.Close()

	b.upload(filepath.Join(dist, "v"+a, "yawl-linux-amd64"), "/tmp/yawl", 0o755)
	b.upload("../../install/install.sh", "/tmp/install.sh", 0o755)
	b.run("mkdir -p /srv/hello && tar --no-same-owner -x -C /srv/hello", bytes.NewReader(helloRepo(t)))
	out := b.run("YAWL_BINARY=/tmp/yawl YAWL_PUBLIC_IP="+srv.IP+" sh /tmp/install.sh", nil)
	m := regexp.MustCompile(`Claim this panel: (https://\S+)/claim#(\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no claim link:\n%s", out)
	}
	jar, _ := cookiejar.New(nil)
	b.hc, b.pan = &http.Client{Jar: jar, Timeout: 2 * time.Minute}, m[1]
	wait(t, 5*time.Minute, "the panel's certificate", func() bool {
		res, err := http.Get(b.pan + "/api/session")
		if err == nil {
			res.Body.Close()
		}
		return err == nil
	})
	if b.panel("POST", "/api/claim", map[string]string{"token": m[2], "email": "owner@acme.uz", "password": "correct horse battery"}, nil) != 200 {
		t.Fatal("claim")
	}
	recPub, recPriv, _ := seal.GenerateKey()
	b.panel("PUT", "/api/recovery-key", map[string]string{"pubkey": recPub.String()}, nil)
	host := strings.TrimSpace(b.run("hostname", nil))
	b.panel("POST", "/api/projects", map[string]string{"name": "acme", "server": host}, nil)
	form := map[string]any{"name": "web", "kind": "web", "repo": "file:///srv/hello", "branch": "master", "port": 10000, "health_path": "/",
		"health_timeout": 60, "drain_timeout": 5, "auto_deploy": false, "release": []string{}, "domains": []string{}, "allow": []string{}}
	b.panel("POST", "/api/environments/acme/production/services", form, nil)
	var keys struct {
		Agent string `json:"agent_pubkey"`
	}
	b.panel("GET", "/api/services/acme/production/web/keys", nil, &keys)
	ak, _ := seal.ParsePublicKey(keys.Agent)
	aBox, _ := seal.Seal(ak, []byte(greeting))
	rBox, _ := seal.Seal(recPub, []byte(greeting))
	b.panel("PUT", "/api/services/acme/production/web/env/GREETING", map[string]any{"agent_box": aBox, "recovery_box": rBox}, nil)
	var dep struct {
		ID string `json:"deploy_id"`
	}
	b.panel("POST", "/api/services/acme/production/web/deploys", map[string]string{}, &dep)
	app := "https://web-production." + strings.ReplaceAll(srv.IP, ".", "-") + ".sslip.io/"
	wait(t, 10*time.Minute-time.Since(start), "the app on HTTPS", func() bool {
		res, err := http.Get(app)
		if err != nil {
			return false
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return strings.TrimSpace(string(body)) == greeting
	})
	t.Logf("acceptance 1: fresh VPS to the app on HTTPS in %s", time.Since(start).Round(time.Second))

	b.run("mkdir -p /srv/rel/v"+bv, nil)
	for _, f := range []string{"yawl-linux-amd64", "SHA256SUMS", "SHA256SUMS.sig"} {
		b.upload(filepath.Join(dist, "v"+bv, f), "/srv/rel/v"+bv+"/"+f, 0o644)
	}
	b.run("nohup python3 -m http.server 8099 --bind 127.0.0.1 --directory /srv/rel >/dev/null 2>&1 &", nil)
	b.run(`sed -i 's|^ExecStart=\(.*\)$|ExecStart=\1 --release-base http://127.0.0.1:8099|' /etc/systemd/system/yawl-server.service /etc/systemd/system/yawl-agent.service && systemctl daemon-reload && systemctl restart yawl-server yawl-agent`, nil)
	wait(t, time.Minute, "the panel after the restart", func() bool { return b.panel("GET", "/api/update", nil, nil) == 200 })
	stop, failed, total := load(app, 10)
	if code := b.panel("POST", "/api/update", map[string]string{"version": bv}, nil); code != 202 {
		t.Fatalf("update: %d", code)
	}
	wait(t, 3*time.Minute, "the panel on "+bv, func() bool {
		var u struct{ Current string }
		b.panel("GET", "/api/update", nil, &u)
		return u.Current == bv
	})
	wait(t, 3*time.Minute, "the agent on "+bv, func() bool {
		var ss []struct {
			AgentVersion string `json:"agent_version"`
		}
		b.panel("GET", "/api/servers", nil, &ss)
		return len(ss) == 1 && ss[0].AgentVersion == bv
	})
	stop()
	t.Logf("acceptance 5: %d requests during the update, %d failed", *total, *failed)
	if *failed > 0 {
		t.Fatal("failed requests during the update")
	}

	var before []any
	b.panel("GET", "/api/services/acme/production/web/deploys", nil, &before)
	b.run("systemctl stop yawl-server", nil)
	for range 10 {
		if res, err := http.Get(app); err != nil || res.StatusCode != 200 {
			t.Fatal("the app stopped answering with the server down")
		}
	}
	b.run("systemctl start yawl-server", nil)
	wait(t, time.Minute, "the panel back", func() bool { return b.panel("GET", "/api/session", nil, nil) == 200 })
	time.Sleep(30 * time.Second)
	var after []any
	b.panel("GET", "/api/services/acme/production/web/deploys", nil, &after)
	if len(after) != len(before) {
		t.Fatalf("a restart deployed: %d deploys, was %d", len(after), len(before))
	}

	if n := strings.TrimSpace(b.run("cat /var/lib/yawl/yawl.db* | grep -a -c '"+greeting+"' || true", nil)); n != "0" {
		t.Fatalf("the database holds the secret (%s)", n)
	}
	if n := strings.TrimSpace(b.run("journalctl -u yawl-server | grep -c '"+greeting+"' || true", nil)); n != "0" {
		t.Fatalf("the journal holds the secret (%s)", n)
	}

	for _, n := range []string{"web2", "web3"} {
		form["name"] = n
		b.panel("POST", "/api/environments/acme/production/services", form, nil)
		b.panel("POST", "/api/services/acme/production/"+n+"/deploys", map[string]string{}, nil)
	}
	db := map[string]any{"name": "db", "kind": "postgres", "version": 17, "backup_at": "03:00", "auto_deploy": true,
		"release": []string{}, "domains": []string{}, "allow": []string{}}
	b.panel("POST", "/api/environments/acme/production/services", db, nil)
	b.panel("POST", "/api/services/acme/production/db/deploys", map[string]string{}, nil)
	wait(t, 10*time.Minute, "four services live", func() bool {
		var ps []struct {
			Envs []struct{ Services []struct{ State string } }
		}
		b.panel("GET", "/api/projects", nil, &ps)
		live := 0
		for _, p := range ps {
			for _, e := range p.Envs {
				for _, s := range e.Services {
					if s.State == "live" {
						live++
					}
				}
			}
		}
		return live == 4
	})
	rss, _ := strconv.Atoi(strings.TrimSpace(b.run("ps -o rss= -C yawl | awk '{ s += $1 } END { print s }'", nil)))
	t.Logf("acceptance 8: yawl resident memory %d MB", rss/1024)
	if rss > 300*1024 {
		t.Fatalf("yawl uses %d MB", rss/1024)
	}

	b.panel("POST", "/api/environments", map[string]string{"project": "acme", "env": "staging", "server": host}, nil)
	form["name"] = "web"
	b.panel("POST", "/api/environments/acme/staging/services", form, nil)
	b.panel("PUT", "/api/environments/acme/staging/env/GREETING", map[string]any{"agent_box": aBox, "recovery_box": rBox}, nil)
	b.panel("POST", "/api/services/acme/staging/web/deploys", map[string]string{}, nil)
	two, err := Provision(ctx, Client{Base: API, Token: token}, t, ServerOpts{Name: name + "-two", Type: "cx22", Image: "ubuntu-24.04",
		Location: "fsn1", PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))})
	if err != nil {
		t.Fatal(err)
	}
	b2 := &box{t: t, ip: two.IP}
	wait(t, 3*time.Minute, "ssh to box two", func() bool {
		c, err := ssh.Dial("tcp", two.IP+":22", &ssh.ClientConfig{User: "root", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second})
		if err == nil {
			b2.ssh = c
		}
		return err == nil
	})
	defer b2.ssh.Close()
	b2.upload(filepath.Join(dist, "v"+bv, "yawl-linux-amd64"), "/usr/local/bin/yawl", 0o755)
	b2.run("DEBIAN_FRONTEND=noninteractive apt-get update -q && DEBIAN_FRONTEND=noninteractive apt-get install -y -q docker.io git", nil)
	var jt struct{ Command string }
	b.panel("POST", "/api/join-token", nil, &jt)
	b2.run("mkdir -p /srv/hello && tar --no-same-owner -x -C /srv/hello", bytes.NewReader(helloRepo(t)))
	b2.run("/usr/local/bin/"+jt.Command+" --name two --ip "+two.IP+" && systemd-run --unit yawl-agent /usr/local/bin/yawl agent", nil)
	wait(t, 2*time.Minute, "box two online", func() bool {
		var ss []struct {
			Name          string
			Offline       bool
			LastHeartbeat int64 `json:"last_heartbeat"`
		}
		b.panel("GET", "/api/servers", nil, &ss)
		for _, s := range ss {
			if s.Name == "two" && !s.Offline && s.LastHeartbeat > 0 {
				return true
			}
		}
		return false
	})
	var plan struct {
		Lines    []string `json:"lines"`
		ToPubkey string   `json:"to_pubkey"`
		Vars     []struct {
			ID          string `json:"id"`
			RecoveryBox string `json:"recovery_box"`
			UpdatedAt   int64  `json:"updated_at"`
		} `json:"vars"`
	}
	if code := b.panel("GET", "/api/environments/acme/staging/move?to=two", nil, &plan); code != 200 {
		t.Fatalf("move plan: %d", code)
	}
	t.Logf("move plan:\n%s", strings.Join(plan.Lines, "\n"))
	to, _ := seal.ParsePublicKey(plan.ToPubkey)
	boxes := map[string]any{}
	for _, v := range plan.Vars {
		val, err := seal.Open(recPub, recPriv, v.RecoveryBox)
		if err != nil {
			t.Fatal(err)
		}
		nb, _ := seal.Seal(to, val)
		boxes[v.ID] = map[string]any{"agent_box": nb, "updated_at": v.UpdatedAt}
	}
	if code := b.panel("POST", "/api/environments/acme/staging/move", map[string]any{"to": "two", "boxes": boxes}, nil); code != 200 {
		t.Fatalf("move: %d", code)
	}
	wait(t, 15*time.Minute, "the move done and box one cleaned", func() bool {
		var m struct {
			State, Cause string
			Cleaned      bool
		}
		b.panel("GET", "/api/environments/acme/staging/moves/latest", nil, &m)
		if m.State == "failed" {
			t.Fatalf("the move failed: %s", m.Cause)
		}
		return m.State == "done" && m.Cleaned
	})
	moved := "https://web-staging." + strings.ReplaceAll(two.IP, ".", "-") + ".sslip.io/"
	wait(t, 5*time.Minute, "staging on box two", func() bool {
		res, err := http.Get(moved)
		if err != nil {
			return false
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return strings.TrimSpace(string(body)) == greeting
	})
	if left := strings.TrimSpace(b.run("docker ps -a --format '{{.Names}}' | grep -c staging || true", nil)); left != "0" {
		t.Fatalf("box one still has %s staging containers", left)
	}
	t.Log("journey four: staging moved to box two, its secret re-sealed, box one cleaned")
}

func load(url string, rps int) (stop func(), failed, total *int) {
	failed, total = new(int), new(int)
	done, quit := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		tk := time.NewTicker(time.Second / time.Duration(rps))
		defer tk.Stop()
		c := &http.Client{Timeout: 10 * time.Second}
		for {
			select {
			case <-quit:
				return
			case <-tk.C:
				*total++
				res, err := c.Get(url)
				if err != nil || res.StatusCode != 200 {
					*failed++
				}
				if err == nil {
					res.Body.Close()
				}
			}
		}
	}()
	return func() { close(quit); <-done }, failed, total
}
