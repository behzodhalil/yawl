package api

import (
	"fmt"
	"strings"
	"testing"
)

func TestTemplatesAPI(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, three)
	_, b := call(t, srv, tok, "GET", "/api/templates", "")
	if !strings.Contains(string(b), `"id":"grafana-loki"`) || !strings.Contains(string(b), `"memory":"1GB"`) || !strings.Contains(string(b), `"icons":["\u003csvg`) {
		t.Fatalf("%s", b)
	}
	if !strings.Contains(string(b), `"category":"backend","new":true`) || !strings.Contains(string(b), `"services":[{"name":"pocketbase","kind":"web","volumes":[{"name":"data","path":"/pb_data"}]}]`) {
		t.Fatalf("cards %s", b)
	}
	if res, _ := call(t, srv, tok, "GET", "/api/templates/nope", ""); res.StatusCode != 404 {
		t.Fatalf("unknown %d", res.StatusCode)
	}
	_, b = call(t, srv, tok, "GET", "/api/templates/umami", "")
	if !strings.Contains(string(b), `"name":"umami-db"`) || !strings.Contains(string(b), `"kind":"ref"`) {
		t.Fatalf("%s", b)
	}
	res, b := call(t, srv, tok, "POST", "/api/environments/acme/production/templates/umami?dry=1", "")
	if res.StatusCode != 200 || !strings.Contains(string(b), `"name":"umami"`) || !strings.Contains(string(b), `"name":"umami-db"`) {
		t.Fatalf("plan %d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/environments/acme/production/templates/umami", ""); res.StatusCode != 200 {
		t.Fatalf("install %d %s", res.StatusCode, b)
	}
	res, b = call(t, srv, tok, "POST", "/api/environments/acme/production/templates/pocketbase", `{"domain":"pb.acme.dev"}`)
	if res.StatusCode != 200 {
		t.Fatalf("pocketbase %d %s", res.StatusCode, b)
	}
	if f, _ := a.CurrentFile(t.Context(), "acme", "production"); !strings.Contains(fmt.Sprint(f.Services), "[pb.acme.dev]") {
		t.Fatalf("domain not set: %+v", f.Services)
	}
	if res, b := call(t, srv, tok, "POST", "/api/environments/acme/production/templates/umami?dry=1", ""); res.StatusCode != 409 || !strings.Contains(string(b), "umami-db already exists in acme/production.") {
		t.Fatalf("second install %d %s", res.StatusCode, b)
	}
}

func TestFormAndTemplateRefuseTakenFreeHost(t *testing.T) {
	srv, tok, a := setupApp(t)
	mustApply(t, a, "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/a\"}\n  - {name: umami, kind: web, image: \"nginx:1\", port: 80}\n")
	mustApply(t, a, "project: zeta\nenvironment: production\nserver: hel1\nservices: []\n")
	res, b := call(t, srv, tok, "POST", "/api/environments/zeta/production/services?new=1&dry=1", `{"name":"api","kind":"web","repo":"https://x/other"}`)
	if res.StatusCode != 409 || !strings.Contains(string(b), "api-production.1-2-3-4.sslip.io is already acme/production/api's; name this service or environment differently.") {
		t.Fatalf("form %d %s", res.StatusCode, b)
	}
	res, b = call(t, srv, tok, "POST", "/api/environments/zeta/production/templates/umami", "")
	if res.StatusCode != 409 || !strings.Contains(string(b), "umami-production.1-2-3-4.sslip.io is already acme/production/umami's;") {
		t.Fatalf("template %d %s", res.StatusCode, b)
	}
}
