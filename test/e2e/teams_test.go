//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/app"
)

func as(tok, method, path string, in any) (int, string) {
	var body io.Reader
	if in != nil {
		j, _ := json.Marshal(in)
		body = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, "http://"+listen+path, body)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, strings.TrimSpace(string(b))
}

func TestE2ETeams(t *testing.T) {
	b := setup(t)
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	claim, err := app.New(b.store()).ClaimLink(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cj, _ := json.Marshal(map[string]string{"token": claim, "email": "owner@acme.uz", "password": "correct horse battery"})
	if res, err := http.Post("http://"+listen+"/api/claim", "application/json", bytes.NewReader(cj)); err != nil || res.StatusCode != 200 {
		t.Fatalf("claim: %v %v", res, err)
	}
	var inv struct{ Token string }
	if code := b.apiBody("POST", "/api/invites", map[string]string{"role": "developer"}, &inv); code != 200 || inv.Token == "" {
		t.Fatalf("invite: %d", code)
	}
	accept, _ := json.Marshal(map[string]string{"token": inv.Token, "email": "dev@acme.uz", "password": "correct horse battery"})
	res, err := http.Post("http://"+listen+"/api/invites/accept", "application/json", bytes.NewReader(accept))
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("accept: %v %v", res, err)
	}
	cookie := strings.Split(res.Header.Get("Set-Cookie"), ";")[0]
	req, _ := http.NewRequest("POST", "http://"+listen+"/api/tokens", strings.NewReader(`{"name":"dev-laptop"}`))
	req.Header.Set("Cookie", cookie)
	req.Header.Set("X-Yawl-Request", "1")
	tr, err := http.DefaultClient.Do(req)
	if err != nil || tr.StatusCode != 200 {
		t.Fatalf("token: %v %v", tr, err)
	}
	var out struct{ Token string }
	json.NewDecoder(tr.Body).Decode(&out)
	dev := out.Token

	if code, body := as(dev, "POST", "/api/services/e2e/production/hello/deploys", map[string]string{}); code != 200 {
		t.Fatalf("a Developer deploys: %d %s", code, body)
	}
	if code, body := as(dev, "POST", "/api/services/e2e/production/hello/env/GREETING/reveal", map[string]string{"to": "x"}); code != 403 || body != "Developers cannot reveal secrets." {
		t.Fatalf("reveal: %d %q", code, body)
	}
	var who string
	b.store().DB.QueryRow(`SELECT actor FROM events WHERE kind = 'deploy.queued' ORDER BY created_at DESC LIMIT 1`).Scan(&who)
	if who != "API token dev-laptop (dev@acme.uz)" {
		t.Fatalf("deploy actor %q", who)
	}
	var members []struct{ ID, Email string }
	b.api("GET", "/api/members", &members)
	id := ""
	for _, m := range members {
		if m.Email == "dev@acme.uz" {
			id = m.ID
		}
	}
	if code := b.api("DELETE", "/api/members/"+id, nil); code != 204 {
		t.Fatalf("remove: %d", code)
	}
	if code, _ := as(dev, "GET", "/api/projects", nil); code != 401 {
		t.Fatalf("a removed member's token answers %d", code)
	}
}
