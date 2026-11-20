//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/proxy"
	"github.com/behzodhalil/yawl/internal/agent/runtime"
)

func (b *box) editYAML(old, new string) {
	t := b.t
	p := filepath.Join(b.repoDir, "yawl.yaml")
	y, _ := os.ReadFile(p)
	if !strings.Contains(string(y), old) {
		t.Fatalf("yawl.yaml has no %q", old)
	}
	os.WriteFile(p, []byte(strings.Replace(string(y), old, new, 1)), 0o644)
	if out, err := run(t, "", "apply", "--yes"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
}

func TestE2EReleaseAndRollback(t *testing.T) {
	b := setupApp(t, "pgnote")
	b.withDB()
	b.editYAML("    health_timeout: 30s\n", "    health_timeout: 30s\n    release: [/pgnote, migrate]\n")
	if _, err := run(t, "", "env", "set", "hello", "DATABASE_URL", "--ref", "notes-db.url"); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"v2", "v3"} {
		b.commit("ok", v)
		if out, err := run(t, "", "deploy", "hello"); err != nil || !strings.Contains(out, "snapshot") || !strings.Contains(out, "release") {
			t.Fatalf("deploy %s: %v\n%s", v, err, out)
		}
	}
	st := b.store()
	var second, path string
	st.DB.QueryRow(`SELECT d.id, s.output FROM deploys d JOIN deploy_steps s ON s.deploy_id = d.id AND s.step = 'snapshot'
		WHERE d.state = 'live' ORDER BY d.created_at DESC LIMIT 1`).Scan(&second, &path)
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 || !strings.HasPrefix(path, filepath.Join(b.dir, "agent", "snapshots")) {
		t.Fatalf("snapshot %q: %v", path, err)
	}
	if v, _ := get(); v != "no notes" {
		t.Fatalf("app served %q", v)
	}

	if _, err := run(t, "", "env", "set", "hello", "MIGRATE_FAIL=1"); err != nil {
		t.Fatal(err)
	}
	b.commit("ok", "v4")
	l := startLoad(20)
	_, err := run(t, "", "deploy", "hello")
	l.end()
	if err == nil || !strings.Contains(err.Error(), "Release command exited with code 1: migration failed") {
		t.Fatalf("deploy with a failing release: %v", err)
	}
	if l.failed > 0 {
		t.Fatalf("%d of %d requests failed while the release failed: %v", l.failed, l.ok+l.failed, l.errs)
	}

	out, err := run(t, "", "rollback", "hello")
	if err != nil {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	if !strings.Contains(out, "The database is unchanged: "+second+" ran a release command. Its snapshot is kept on e2e at "+path+".") {
		t.Fatalf("rollback output:\n%s", out)
	}
	if v, _ := get(); v != "no notes" {
		t.Fatalf("after rollback the app served %q", v)
	}
}

func TestE2EAutoRollback(t *testing.T) {
	b := setup(t)
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	b.commit("flaky", "v2")
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	if v, _ := get(); v != "v2" {
		t.Fatalf("served %q", v)
	}
	st := b.store()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		rows, _ := st.DB.Query(`SELECT kind, body_json FROM events ORDER BY created_at`)
		for rows.Next() {
			var k, body string
			rows.Scan(&k, &body)
			t.Logf("event %s %s", k, body)
		}
		rows.Close()
		rows, _ = st.DB.Query(`SELECT id, state, rollback, auto_rolled_back, prev_deploy_id, finished_at FROM deploys ORDER BY created_at`)
		for rows.Next() {
			var id, state, prev string
			var rb, arb int
			var fin *int64
			rows.Scan(&id, &state, &rb, &arb, &prev, &fin)
			t.Logf("deploy %s %s rollback=%d auto=%d prev=%s finished=%v", id, state, rb, arb, prev, fin)
		}
		rows.Close()
	})
	waitFor(t, 4*time.Minute, "the automatic rollback to v1", func() bool { v, _ := get(); return v == "v1" })
	l := startLoad(20)
	time.Sleep(5 * time.Second)
	l.end()
	if l.failed > 0 {
		t.Fatalf("%d failed requests after the rollback: %v", l.failed, l.errs)
	}
	waitFor(t, 30*time.Second, "the rollback message", func() bool {
		for _, m := range b.telegram.all() {
			if strings.Contains(m, "e2e-hello failed 3 health checks within 5 minutes of going live; rolled back to") {
				return true
			}
		}
		return false
	})
}

func TestE2ELogsSearchMasked(t *testing.T) {
	b := setup(t)
	b.withSecret()
	b.commit("print", "v1")
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	var out string
	waitFor(t, 20*time.Second, "the greeting in the logs", func() bool {
		var err error
		out, err = run(t, "", "logs", "hello", "-q", "GREETING IS")
		return err == nil && strings.Contains(out, "greeting is")
	})
	if strings.Contains(out, planted) || !strings.Contains(out, "greeting is ••••") {
		t.Fatalf("logs:\n%s", out)
	}
	if strings.Contains(out, "listening on") {
		t.Fatalf("-q let other lines through:\n%s", out)
	}
}

func TestE2EDomainWrongDNS(t *testing.T) {
	b := setup(t)
	b.editYAML("    health_timeout: 30s\n", "    health_timeout: 30s\n    domains: [shop.10.9.9.9.nip.io]\n")
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	var out string
	waitFor(t, 150*time.Second, "the domain's DNS state", func() bool {
		out, _ = run(t, "", "domains", "hello")
		return strings.Contains(out, "wrong")
	})
	if !strings.Contains(out, "shop.10.9.9.9.nip.io points to 10.9.9.9. Set an A record: shop.10.9.9.9.nip.io → 127.0.0.1.") {
		t.Fatalf("domains:\n%s", out)
	}
	rt, _ := runtime.NewDocker()
	routes, err := proxy.NewCaddy(rt, proxy.DefaultSocket).Routes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if routes["shop.10.9.9.9.nip.io"] != "" || routes[host] == "" {
		t.Fatalf("routes %v", routes)
	}
}
