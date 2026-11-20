//go:build boxes

package boxes

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/behzodhalil/yawl/internal/seal"
)

const greeting = "hello-from-the-boxes"

const (
	runTimeWithoutTestDeadline     = 45 * time.Minute
	cleanupMarginBeforeTestTimeout = 15 * time.Minute
)

func runDeadlineFor(t *testing.T) time.Time {
	if d, ok := t.Deadline(); ok {
		return d.Add(-cleanupMarginBeforeTestTimeout)
	}
	return time.Now().Add(runTimeWithoutTestDeadline)
}

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
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

func answers(b *Box, host string) bool {
	code, out := b.Try("curl -s -m 5 -H 'Host: " + host + "' http://127.0.0.1/")
	return code == 0 && strings.TrimSpace(out) == greeting
}

func live(t *testing.T, one, b *Box, svc, host string) {
	t.Helper()
	waitFor(t, 10*time.Minute, host, func() bool {
		var deps []struct{ State, Cause string }
		one.API(t, "GET", "/api/services/"+svc+"/deploys", nil, &deps)
		if len(deps) > 0 && deps[0].State == "failed" {
			t.Fatalf("%s: the deploy failed: %s", svc, deps[0].Cause)
		}
		return answers(b, host)
	})
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

func TestBoxes(t *testing.T) {
	dist, a, bv := os.Getenv("YAWL_BOX_DIST"), os.Getenv("YAWL_BOX_A"), os.Getenv("YAWL_BOX_B")
	if dist == "" || a == "" || bv == "" {
		t.Skip("run through `make boxes`, which builds the two releases")
	}
	runDeadline = runDeadlineFor(t)
	ctx := context.Background()
	one, two, s3 := Up(t, ctx)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, b := range []*Box{one, two} {
			dctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			_, out := b.exec(dctx, "docker ps -a --format '{{.Names}} {{.Status}}'; journalctl -u yawl-server -u yawl-agent -u yawl-agent.service --no-pager | tail -60", nil)
			cancel()
			t.Logf("%s:\n%s", b.Name, out)
		}
		var deps any
		one.API(t, "GET", "/api/services/acme/production/web/deploys", nil, &deps)
		t.Logf("production web deploys: %v", deps)
	})
	arch := strings.TrimSpace(one.Run(t, "dpkg --print-architecture", nil))
	release := func(v, f string) []byte {
		b, err := os.ReadFile(filepath.Join(dist, "v"+v, f))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	inst, _ := os.ReadFile("../../install/install.sh")
	one.Put(t, "/tmp/yawl", 0o755, release(a, "yawl-linux-"+arch))
	one.Put(t, "/tmp/install.sh", 0o755, inst)
	repo := helloRepo(t)
	one.Run(t, "mkdir -p /srv/hello && tar --no-same-owner -x -C /srv/hello", bytes.NewReader(repo))
	start := time.Now()
	installer := "YAWL_BINARY=/tmp/yawl YAWL_PUBLIC_IP=172.30.0.11 sh /tmp/install.sh"
	out := one.Run(t, installer, nil)
	m := regexp.MustCompile(`Claim this panel: https://panel\.172-30-0-11\.sslip\.io/claim#(\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no claim link:\n%s", out)
	}
	if s := one.Run(t, "systemctl is-active yawl-server yawl-agent", nil); s != "active\nactive\n" {
		t.Fatalf("units: %q", s)
	}
	if one.API(t, "POST", "/api/claim", map[string]string{"token": m[1], "email": "owner@acme.uz", "password": "correct horse battery"}, nil) != 200 {
		t.Fatal("claim")
	}
	recPub, recPriv, _ := seal.GenerateKey()
	one.API(t, "PUT", "/api/recovery-key", map[string]string{"pubkey": recPub.String()}, nil)
	one.API(t, "POST", "/api/projects", map[string]string{"name": "acme", "server": "one"}, nil)
	form := map[string]any{"name": "web", "kind": "web", "repo": "file:///srv/hello", "branch": "master", "port": 10000, "health_path": "/",
		"health_timeout": 60, "drain_timeout": 5, "auto_deploy": false, "release": []string{}, "domains": []string{}, "allow": []string{}}
	one.API(t, "POST", "/api/environments/acme/production/services", form, nil)
	var keys struct {
		Agent string `json:"agent_pubkey"`
	}
	one.API(t, "GET", "/api/services/acme/production/web/keys", nil, &keys)
	ak, err := seal.ParsePublicKey(keys.Agent)
	if err != nil {
		t.Fatalf("agent key: %v", err)
	}
	aBox, _ := seal.Seal(ak, []byte(greeting))
	rBox, _ := seal.Seal(recPub, []byte(greeting))
	one.API(t, "PUT", "/api/services/acme/production/web/env/GREETING", map[string]any{"agent_box": aBox, "recovery_box": rBox}, nil)
	one.API(t, "POST", "/api/services/acme/production/web/deploys", map[string]string{}, nil)
	prodHost := "web-production.172-30-0-11.sslip.io"
	live(t, one, one, "acme/production/web", prodHost)
	t.Logf("install: installer to the app answering in %s", time.Since(start).Round(time.Second))
	if n := strings.TrimSpace(one.Run(t, "cat /var/lib/yawl/yawl.db* | grep -a -c '"+greeting+"' || true", nil)); n != "0" {
		t.Fatalf("the database holds the secret (%s)", n)
	}
	if n := strings.TrimSpace(one.Run(t, "journalctl -u yawl-server | grep -c '"+greeting+"' || true", nil)); n != "0" {
		t.Fatalf("the journal holds the secret (%s)", n)
	}

	one.Run(t, "sed -i 's|--listen 127.0.0.1:7000|--listen 0.0.0.0:7000|' /etc/systemd/system/yawl-server.service && systemctl daemon-reload && systemctl restart yawl-server", nil)
	one.Run(t, "rm /etc/systemd/system/yawl-agent.service && systemctl daemon-reload", nil)
	if code, out := one.Try(installer); code != 0 {
		t.Fatalf("the installer run twice exits %d:\n%s", code, out)
	} else if !strings.Contains(out, "This panel already has an owner: https://panel.172-30-0-11.sslip.io") ||
		!strings.Contains(out, "kept the existing units") || strings.Contains(out, "Could not make a claim link") {
		t.Fatalf("the second install:\n%s", out)
	}
	if code, _ := one.Try("grep -q -- '--listen 0.0.0.0:7000' /etc/systemd/system/yawl-server.service"); code != 0 {
		t.Fatal("the second install lost the unit's edit")
	}
	if s := one.Run(t, "systemctl is-active yawl-server yawl-agent", nil); s != "active\nactive\n" {
		t.Fatalf("units after the second install: %q", s)
	}
	waitFor(t, time.Minute, "the app after the second install", func() bool { return answers(one, prodHost) })

	waitFor(t, time.Minute, "the bucket", func() bool {
		_, code := one.Try("curl -s -o /dev/null -w '%{http_code}' --aws-sigv4 aws:amz:us-east-1:s3 --user yawltest:yawltest-secret -X PUT http://" + s3 + ":9000/backups")
		return code == "200" || code == "409"
	})
	if one.API(t, "PUT", "/api/settings/storage", map[string]string{"endpoint": "http://" + s3 + ":9000", "bucket": "backups", "region": "us-east-1",
		"access_key": "yawltest", "secret_key": "yawltest-secret"}, nil) != 204 {
		t.Fatal("storage")
	}
	waitFor(t, time.Minute, "the panel on 0.0.0.0:7000", func() bool { return one.API(t, "GET", "/api/servers", nil, nil) == 200 })
	var jt struct{ Command string }
	one.API(t, "POST", "/api/join-token", nil, &jt)
	f := strings.Fields(jt.Command)
	if len(f) != 5 {
		t.Fatalf("join command %q", jt.Command)
	}
	two.Put(t, "/usr/local/bin/yawl", 0o755, release(a, "yawl-linux-"+arch))
	two.Run(t, "mkdir -p /srv/hello && tar --no-same-owner -x -C /srv/hello", bytes.NewReader(repo))
	two.Run(t, "yawl agent join http://172.30.0.11:7000 "+f[4]+" --name two --ip 172.30.0.12 && "+
		"systemd-run --unit yawl-agent -p Restart=always /usr/local/bin/yawl agent --release-base http://172.30.0.11:8099", nil)
	waitFor(t, 2*time.Minute, "box two online", func() bool {
		var ss []struct {
			Name          string
			Offline       bool
			LastHeartbeat int64 `json:"last_heartbeat"`
		}
		one.API(t, "GET", "/api/servers", nil, &ss)
		for _, s := range ss {
			if s.Name == "two" && !s.Offline && s.LastHeartbeat > 0 {
				return true
			}
		}
		return false
	})

	one.API(t, "POST", "/api/environments", map[string]string{"project": "acme", "env": "staging", "server": "one"}, nil)
	one.API(t, "POST", "/api/environments/acme/staging/services", form, nil)
	one.API(t, "PUT", "/api/environments/acme/staging/env/GREETING", map[string]any{"agent_box": aBox, "recovery_box": rBox}, nil)
	one.API(t, "POST", "/api/environments/acme/staging/services", map[string]any{"name": "db", "kind": "postgres", "version": 17, "backup_at": "03:00",
		"auto_deploy": true, "release": []string{}, "domains": []string{}, "allow": []string{}}, nil)
	one.API(t, "POST", "/api/services/acme/staging/db/deploys", map[string]string{}, nil)
	one.API(t, "POST", "/api/services/acme/staging/web/deploys", map[string]string{}, nil)
	live(t, one, one, "acme/staging/web", "web-staging.172-30-0-11.sslip.io")
	const dbc = "yawl-acme-staging-db"
	// its deploy live, not only pg_isready: the image's first-start server answers that too, then restarts
	waitFor(t, 5*time.Minute, "staging's database deploy", func() bool {
		var ds []struct{ State string }
		one.API(t, "GET", "/api/services/acme/staging/db/deploys", nil, &ds)
		return len(ds) > 0 && ds[0].State == "live"
	})
	waitFor(t, 5*time.Minute, "staging's database", func() bool { code, _ := one.Try("docker exec " + dbc + " pg_isready -U app"); return code == 0 })
	row := "written-" + time.Now().Format(time.RFC3339Nano)
	one.Run(t, "docker exec "+dbc+" psql -U app -d app -c \"CREATE TABLE note(v text); INSERT INTO note VALUES ('"+row+"')\"", nil)

	var plan struct {
		Lines    []string `json:"lines"`
		ToPubkey string   `json:"to_pubkey"`
		Restore  bool     `json:"restore"`
		Vars     []struct {
			ID          string `json:"id"`
			RecoveryBox string `json:"recovery_box"`
			UpdatedAt   int64  `json:"updated_at"`
		} `json:"vars"`
	}
	if code := one.API(t, "GET", "/api/environments/acme/staging/move?to=two", nil, &plan); code != 200 {
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
	keyBox := ""
	if plan.Restore {
		keyBox, _ = seal.Seal(to, []byte(seal.EncodeRecovery(recPriv)))
	}
	moveStart := time.Now()
	if code := one.API(t, "POST", "/api/environments/acme/staging/move", map[string]any{"to": "two", "boxes": boxes, "recovery_box": keyBox}, nil); code != 200 {
		t.Fatalf("move: %d", code)
	}
	waitFor(t, 20*time.Minute, "the move done and box one cleaned", func() bool {
		var m struct {
			State, Step, Cause string
			Cleaned            bool
		}
		one.API(t, "GET", "/api/environments/acme/staging/moves/latest", nil, &m)
		if m.State == "failed" {
			t.Fatalf("the move failed at %s: %s", m.Step, m.Cause)
		}
		return m.State == "done" && m.Cleaned
	})
	waitFor(t, 5*time.Minute, "staging on box two", func() bool { return answers(two, "web-staging.172-30-0-12.sslip.io") })
	if got := strings.TrimSpace(two.Run(t, "docker exec "+dbc+" psql -U app -d app -tAc 'SELECT v FROM note'", nil)); got != row {
		t.Fatalf("staging's database on box two holds %q, want %q", got, row)
	}
	if left := strings.TrimSpace(one.Run(t, "docker ps -a --format '{{.Names}}' | grep -c acme-staging || true", nil)); left != "0" {
		t.Fatalf("box one still has %s staging containers", left)
	}
	if !strings.Contains(one.Run(t, "docker volume ls -q", nil), dbc+"-data") {
		t.Fatal("box one lost staging's database volume")
	}
	t.Logf("move: staging and its database moved to box two in %s", time.Since(moveStart).Round(time.Second))

	netStart := time.Now()
	waitFor(t, 3*time.Minute, "a WireGuard handshake both ways", func() bool {
		var ss []struct {
			Peers []struct {
				LastHandshake int64 `json:"last_handshake"`
			}
		}
		one.API(t, "GET", "/api/servers", nil, &ss)
		n := 0
		for _, s := range ss {
			for _, p := range s.Peers {
				if p.LastHandshake > 0 {
					n++
				}
			}
		}
		return n >= 2
	})
	var refd struct{ Note string }
	if code := one.API(t, "PUT", "/api/services/acme/production/web/env/DATABASE_URL", map[string]string{"ref": "staging/db.url"}, &refd); code != 200 ||
		!strings.Contains(refd.Note, "db shares its address with one") {
		t.Fatalf("reference: %d %q", code, refd.Note)
	}
	waitFor(t, 5*time.Minute, "staging's database sharing its address", func() bool {
		var ds []struct{ State string }
		one.API(t, "GET", "/api/services/acme/staging/db/deploys", nil, &ds)
		return len(ds) > 0 && ds[0].State == "live"
	})
	one.API(t, "POST", "/api/services/acme/production/web/deploys", map[string]string{}, nil)
	waitFor(t, 5*time.Minute, "production's web deploy with the reference live", func() bool {
		var ds []struct{ State, Cause string }
		one.API(t, "GET", "/api/services/acme/production/web/deploys", nil, &ds)
		if len(ds) > 0 && (ds[0].State == "failed" || ds[0].State == "rolled_back") {
			t.Fatalf("production's web deploy %s: %s", ds[0].State, ds[0].Cause)
		}
		return len(ds) > 0 && ds[0].State == "live"
	})
	live(t, one, one, "acme/production/web", prodHost)
	url := strings.TrimSpace(one.Run(t, "docker exec $(docker ps -q --filter label=yawl.project=acme --filter label=yawl.env=production --filter label=yawl.service=web | head -n 1) printenv DATABASE_URL", nil))
	if !strings.Contains(url, "@10.73.0.") || !strings.Contains(url, "sslmode=require") {
		t.Fatalf("production's DATABASE_URL is not the private address: %.40s…", url)
	}
	if got := strings.TrimSpace(one.Run(t, "docker run --rm --network host postgres:17 psql '"+url+"' -tAc 'SELECT v FROM note'", nil)); got != row {
		t.Fatalf("over the private network box one read %q, want %q", got, row)
	}
	if ports := two.Run(t, "docker port "+dbc, nil); !strings.Contains(ports, "10.73.0.") || strings.Contains(ports, "0.0.0.0") || strings.Contains(ports, "172.30.0.12") {
		t.Fatalf("staging's database is published beyond the private network:\n%s", ports)
	}
	t.Logf("network: handshake to a row read over WireGuard in %s", time.Since(netStart).Round(time.Second))

	for _, f := range []string{"yawl-linux-" + arch, "SHA256SUMS", "SHA256SUMS.sig"} {
		one.Put(t, "/srv/rel/v"+bv+"/"+f, 0o644, release(bv, f))
	}
	one.Run(t, "nohup python3 -m http.server 8099 --bind 0.0.0.0 --directory /srv/rel >/dev/null 2>&1 &", nil)
	one.Run(t, `sed -i 's|^ExecStart=\(.*\)$|ExecStart=\1 --release-base http://127.0.0.1:8099|' /etc/systemd/system/yawl-server.service /etc/systemd/system/yawl-agent.service && systemctl daemon-reload && systemctl restart yawl-server yawl-agent`, nil)
	waitFor(t, time.Minute, "the panel after the restart", func() bool { return one.API(t, "GET", "/api/update", nil, nil) == 200 })
	waitFor(t, time.Minute, "the app after the restart", func() bool { return answers(one, prodHost) })
	one.Run(t, `rm -f /tmp/load.stop; nohup sh -c 'n=0; f=0; while [ ! -f /tmp/load.stop ]; do `+
		`c=$(curl -s -o /dev/null -m 5 -w "%{http_code}" -H "Host: `+prodHost+`" http://127.0.0.1/); `+
		`n=$((n+1)); [ "$c" = 200 ] || f=$((f+1)); echo "$n $f" > /tmp/load.tmp; mv /tmp/load.tmp /tmp/load.count; sleep 0.1; done; `+
		`echo "$n $f" > /tmp/load.final' >/dev/null 2>&1 &`, nil)
	count := func(file string) (total, failed int) {
		fmt.Sscan(one.Run(t, "cat "+file, nil), &total, &failed)
		return
	}
	waitFor(t, 30*time.Second, "the load running", func() bool { code, _ := one.Try("test -s /tmp/load.count"); return code == 0 })
	before, _ := count("/tmp/load.count")
	if code := one.API(t, "POST", "/api/update", map[string]string{"version": bv}, nil); code != 202 {
		t.Fatalf("update: %d", code)
	}
	waitFor(t, 3*time.Minute, "the panel on "+bv, func() bool {
		var u struct{ Current string }
		one.API(t, "GET", "/api/update", nil, &u)
		return u.Current == bv
	})
	waitFor(t, 3*time.Minute, "both agents on "+bv, func() bool {
		var ss []struct {
			AgentVersion string `json:"agent_version"`
		}
		one.API(t, "GET", "/api/servers", nil, &ss)
		return len(ss) == 2 && ss[0].AgentVersion == bv && ss[1].AgentVersion == bv
	})
	one.Run(t, "touch /tmp/load.stop", nil)
	waitFor(t, 30*time.Second, "the load to stop", func() bool { code, _ := one.Try("test -s /tmp/load.final"); return code == 0 })
	total, failed := count("/tmp/load.final")
	t.Logf("update: %d requests during the update, %d failed", total-before, failed)
	if total-before < 20 || failed > 0 {
		t.Fatalf("update under load: %d of %d requests failed, %d during the update", failed, total, total-before)
	}
}
