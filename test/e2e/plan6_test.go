//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/seal"
	"github.com/behzodhalil/yawl/internal/server/app"
)

type panel struct {
	t  *testing.T
	hc *http.Client
}

func (p panel) do(method, path string, body any, header bool) (int, []byte) {
	p.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, "http://"+listen+path, r)
	if header {
		req.Header.Set("X-Yawl-Request", "1")
	}
	res, err := p.hc.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func (p panel) ok(method, path string, body, out any) {
	p.t.Helper()
	code, b := p.do(method, path, body, true)
	if code/100 != 2 {
		p.t.Fatalf("%s %s: %d %s", method, path, code, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			p.t.Fatalf("%s %s: %v %s", method, path, err, b)
		}
	}
}

func TestE2EDashboardFlow(t *testing.T) {
	b := setup(t)
	b.commit("env", "v2")
	st, ctx := b.store(), context.Background()
	tok, err := app.New(st).ClaimLink(ctx)
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	p := panel{t: t, hc: &http.Client{Jar: jar, Timeout: 3 * time.Minute}}
	const email, password = "owner@acme.uz", "correct horse battery"
	p.ok("POST", "/api/claim", map[string]string{"token": tok, "email": email, "password": password}, nil)

	if code, b := p.do("POST", "/api/projects", map[string]string{"name": "acme", "server": "e2e"}, false); code != 403 {
		t.Fatalf("a cookie write without the header: %d %s", code, b)
	}
	pub, _, _ := seal.GenerateKey()
	p.ok("PUT", "/api/recovery-key", map[string]string{"pubkey": pub.String()}, nil)
	p.ok("POST", "/api/projects", map[string]string{"name": "acme", "server": "e2e"}, nil)

	var ins struct {
		ReadsPort bool     `json:"reads_port"`
		Missing   []string `json:"missing"`
		Health    string   `json:"health_path"`
	}
	repo := "file://" + b.repoDir
	p.ok("POST", "/api/inspect", map[string]string{"project": "acme", "env": "production", "repo": repo, "branch": "master"}, &ins)
	if !ins.ReadsPort || !slices.Contains(ins.Missing, "GREETING") {
		t.Fatalf("inspect: %+v", ins)
	}

	form := map[string]any{"name": "web", "kind": "web", "repo": repo, "branch": "master", "port": 10000, "health_path": ins.Health,
		"health_timeout": 30, "drain_timeout": 3, "auto_deploy": false, "release": []string{}, "domains": []string{}, "allow": []string{}}
	var plan struct{ Changes []struct{ Op, Name string } }
	p.ok("POST", "/api/environments/acme/production/services?dry=1", form, &plan)
	if len(plan.Changes) != 1 || plan.Changes[0].Op != "create" || plan.Changes[0].Name != "web" {
		t.Fatalf("plan %+v", plan)
	}
	p.ok("POST", "/api/environments/acme/production/services", form, nil)

	var keys struct {
		Agent    string `json:"agent_pubkey"`
		Recovery string `json:"recovery_pubkey"`
	}
	p.ok("GET", "/api/services/acme/production/web/keys", nil, &keys)
	agentKey, _ := seal.ParsePublicKey(keys.Agent)
	recKey, _ := seal.ParsePublicKey(keys.Recovery)
	const greeting = "hello-from-the-dashboard-42"
	aBox, _ := seal.Seal(agentKey, []byte(greeting))
	rBox, _ := seal.Seal(recKey, []byte(greeting))
	p.ok("PUT", "/api/services/acme/production/web/env/GREETING", map[string]any{"agent_box": aBox, "recovery_box": rBox, "build": false}, nil)

	var dep struct {
		ID string `json:"deploy_id"`
	}
	p.ok("POST", "/api/services/acme/production/web/deploys", map[string]string{"commit": ""}, &dep)
	waitFor(t, 4*time.Minute, "the dashboard's deploy to go live", func() bool {
		var v struct{ State, Cause string }
		p.ok("GET", "/api/deploys/"+dep.ID, nil, &v)
		if v.State == "failed" {
			t.Fatalf("deploy failed: %s", v.Cause)
		}
		return v.State == "live"
	})
	req, _ := http.NewRequest("GET", "http://127.0.0.1/", nil)
	req.Host = "web-production.127-0-0-1.sslip.io"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.TrimSpace(string(body)) != greeting {
		t.Fatalf("the app answered %d %q", res.StatusCode, body)
	}

	for _, path := range []string{"/", "/p/acme/production", "/settings/telegram"} {
		code, b := p.do("GET", path, nil, false)
		if code != 200 || !strings.Contains(string(b), `<div id="root">`) {
			t.Fatalf("panel %s: %d %.200s", path, code, b)
		}
	}
	if code, _ := p.do("GET", "/api/nope", nil, false); code != 404 {
		t.Fatalf("/api/nope: %d", code)
	}
	var who struct{ Actor string }
	st.DB.QueryRow(`SELECT actor FROM events WHERE kind = 'deploy.queued' AND subject_id = ?`, dep.ID).Scan(&who.Actor)
	if who.Actor != email {
		t.Fatalf("deploy actor %q", who.Actor)
	}

	if os.Getenv("YAWL_E2E_HOLD") == "1" {
		t.Logf("panel: http://%s  email: %s  password: %s", listen, email, password)
		fmt.Fprintf(os.Stderr, "\nHOLD: panel at http://%s, sign in as %s / %s\n", listen, email, password)
		if dl, ok := t.Deadline(); ok {
			time.Sleep(time.Until(dl) - time.Minute)
		}
	}
}
