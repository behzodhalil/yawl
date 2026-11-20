//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func (b *box) api(method, path string, out any) int { return b.apiBody(method, path, nil, out) }

func (b *box) apiBody(method, path string, in, out any) int {
	b.t.Helper()
	raw, _ := os.ReadFile(filepath.Join(b.dir, "cli.json"))
	var cfg struct{ Token string }
	json.Unmarshal(raw, &cfg)
	var send io.Reader
	if in != nil {
		j, _ := json.Marshal(in)
		send = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, "http://"+listen+path, send)
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if out != nil {
		json.Unmarshal(body, out)
	}
	return res.StatusCode
}

func (b *box) deployIDs() []string {
	st := b.store()
	rows, _ := st.DB.Query(`SELECT id FROM deploys ORDER BY created_at, id`)
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids
}

func (b *box) liveDeploy() string {
	var id string
	b.store().DB.QueryRow(`SELECT current_deploy_id FROM services WHERE name = 'hello'`).Scan(&id)
	return id
}

func TestE2EServerRestoreAdopt(t *testing.T) {
	b := setup(t)
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
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	v1 := b.liveDeploy()
	if code := b.api("POST", "/api/settings/server-backup", nil); code != 204 {
		t.Fatalf("self-backup: %d", code)
	}
	b.commit("ok", "v2")
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	v2 := b.liveDeploy()

	l := startLoad(20)
	b.stopServer()
	waitFor(t, 15*time.Second, "server down", func() bool { _, err := http.Get("http://" + listen + "/api/session"); return err != nil })
	for _, d := range []string{"data", "etc"} {
		os.Rename(filepath.Join(b.dir, d), filepath.Join(b.dir, d+".old"))
	}
	var rout bytes.Buffer
	err = server.Restore(context.Background(), server.RestoreOpts{Data: filepath.Join(b.dir, "data"), Etc: filepath.Join(b.dir, "etc"),
		Endpoint: "http://127.0.0.1:19000", Bucket: "backups", Region: "us-east-1", Access: "yawltest", Secret: "yawltest-secret"},
		strings.NewReader(key+"\n"), &rout)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	t.Log(strings.TrimSpace(rout.String()))
	b.startServer()

	want := "e2e-hello runs deploy " + v2 + ", the server last knew " + v1 + "."
	var view struct {
		Adopting bool
		Lines    []string
		Seen     string
	}
	waitFor(t, 90*time.Second, "the agent to report", func() bool {
		b.api("GET", "/api/adopt", &view)
		return slices.Contains(view.Lines, want)
	})
	var queued struct {
		DeployID string `json:"deploy_id"`
	}
	b.api("POST", "/api/services/e2e/production/hello/deploys", &queued)
	time.Sleep(3 * time.Second)
	var d struct{ State string }
	b.api("GET", "/api/deploys/"+queued.DeployID, &d)
	if d.State != "queued" {
		t.Fatalf("a deploy ran while adopting: %s", d.State)
	}
	b.api("GET", "/api/adopt", &view)
	if code := b.apiBody("POST", "/api/adopt", map[string]string{"seen": view.Seen}, nil); code != 204 {
		t.Fatalf("adopt: %d", code)
	}
	if cur := b.liveDeploy(); cur != v2 {
		t.Fatalf("after adopt the current deploy is %s, want %s", cur, v2)
	}
	waitFor(t, 3*time.Minute, "the queued deploy to go live", func() bool {
		b.api("GET", "/api/deploys/"+queued.DeployID, &d)
		if d.State == "failed" {
			t.Fatal("the queued deploy failed")
		}
		return d.State == "live"
	})
	l.end()
	t.Logf("%d requests, %d failed", l.ok+l.failed, l.failed)
	if l.failed > 0 {
		t.Fatalf("%d failed requests across the restore: %v", l.failed, l.errs)
	}
	if ids := b.deployIDs(); len(ids) != 3 || !slices.Contains(ids, v2) || !slices.Contains(ids, queued.DeployID) {
		t.Fatalf("deploys %v (adopt must create only the one the backup missed)", ids)
	}
}

func TestE2EPanelHost(t *testing.T) {
	b := setup(t)
	b.stopServer()
	st := b.store()
	var id string
	st.DB.QueryRow(`SELECT id FROM servers WHERE name = 'e2e'`).Scan(&id)
	st.Q.SetSetting(context.Background(), db.SetSettingParams{Key: "panel.server_id", Value: id})
	b.startServer()
	var res *http.Response
	waitFor(t, 2*time.Minute, "the panel host to be routed", func() bool {
		req, _ := http.NewRequest("GET", "http://127.0.0.1/", nil)
		req.Host = "panel.127-0-0-1.sslip.io"
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		res = r
		return r.StatusCode == 200 && strings.Contains(string(body), `<div id="root">`)
	})
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("headers %v", res.Header)
	}
}
