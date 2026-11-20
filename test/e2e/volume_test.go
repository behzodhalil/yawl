//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/seal"
	"github.com/behzodhalil/yawl/internal/server"
	dockerclient "github.com/moby/moby/client"
)

const noteVolume = "yawl_e2e_production_hello_notes"

func file(t *testing.T, body string) string {
	t.Helper()
	method := "GET"
	if body != "" {
		method = "PUT"
	}
	req, _ := http.NewRequest(method, "http://127.0.0.1/file", strings.NewReader(body))
	req.Host = host
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "error: " + err.Error()
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		return fmt.Sprintf("status %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	return string(b)
}

func TestE2EVolume(t *testing.T) {
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	drop := func() {
		removeApps(t)
		rt.C.VolumeRemove(context.Background(), noteVolume, dockerclient.VolumeRemoveOptions{Force: true})
	}
	drop()
	b := setup(t)
	t.Cleanup(drop)
	b.editYAML("    health_timeout: 30s\n", "    health_timeout: 30s\n    volumes:\n      - {name: notes, path: /data}\n")
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	if got := file(t, "first"); got != "" {
		t.Fatalf("write: %q", got)
	}
	b.commit("ok", "v2")
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	if v, _ := get(); v != "v2" {
		t.Fatalf("not the new version: %q", v)
	}
	if got := file(t, ""); got != "first" {
		t.Fatalf("after a deploy the note is %q", got)
	}

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
	s3(t)
	if err := server.Run(context.Background(), []string{"--data", filepath.Join(b.dir, "data"), "--etc", filepath.Join(b.dir, "etc"),
		"storage", "set", "http://127.0.0.1:19000", "backups", "yawltest", "yawltest-secret", "us-east-1"}, "dev"); err != nil {
		t.Fatal(err)
	}
	var bk struct{ ID string }
	if code := b.apiBody("POST", "/api/services/e2e/production/hello/backups", map[string]string{"volume": "notes"}, &bk); code != 200 {
		t.Fatalf("back up now: %d", code)
	}
	waitFor(t, 120*time.Second, "the volume backup to finish", func() bool {
		var state, cause string
		b.store().DB.QueryRow(`SELECT state, cause FROM backups WHERE id = ?`, bk.ID).Scan(&state, &cause)
		if state == "failed" {
			t.Fatalf("backup failed: %s", cause)
		}
		return state == "ok"
	})

	if got := file(t, "second"); got != "" {
		t.Fatalf("overwrite: %q", got)
	}
	var keys struct {
		AgentPubkey string `json:"agent_pubkey"`
	}
	b.api("GET", "/api/services/e2e/production/hello/keys", &keys)
	pk, err := seal.ParsePublicKey(keys.AgentPubkey)
	if err != nil {
		t.Fatal(err)
	}
	box, err := seal.Seal(pk, []byte(key))
	if err != nil {
		t.Fatal(err)
	}
	var rs struct{ Deploy string }
	if code := b.apiBody("POST", "/api/services/e2e/production/hello/restore", map[string]string{"backup": bk.ID, "recovery_box": box}, &rs); code != 200 {
		t.Fatalf("restore: %d", code)
	}
	waitFor(t, 120*time.Second, "the restore deploy to go live", func() bool {
		var state, cause string
		b.store().DB.QueryRow(`SELECT state, cause FROM deploys WHERE id = ?`, rs.Deploy).Scan(&state, &cause)
		if state == "failed" || state == "rolled_back" {
			t.Fatalf("restore deploy %s: %s", state, cause)
		}
		return state == "live"
	})
	if got := file(t, ""); got != "first" {
		t.Fatalf("after the restore the note is %q", got)
	}
	if v, _ := get(); v != "v2" {
		t.Fatalf("after the restore the app serves %q", v)
	}
}
