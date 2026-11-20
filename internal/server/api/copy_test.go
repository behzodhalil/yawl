package api

import (
	"strings"
	"testing"
)

func TestAPICopyService(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n")
	call(t, srv, tok, "POST", "/api/apply", "project: acme\nenvironment: staging\nserver: hel1\nservices: []\n")
	res, b := call(t, srv, tok, "POST", "/api/services/acme/production/api/copy?dry=1", `{"project":"acme","env":"staging"}`)
	if res.StatusCode != 200 || !strings.Contains(string(b), "copy api to acme/staging") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/services/acme/production/api/copy", `{"project":"acme","env":"staging","move":true}`); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var live int
	testDB.QueryRow(`SELECT COUNT(*) FROM services WHERE name = 'api' AND deleted_at IS NULL`).Scan(&live)
	if live != 1 {
		t.Fatalf("%d live api services after a move", live)
	}
	dev := memberToken(t, srv, tok, "dev6@acme.uz", "developer")
	if res, _ := call(t, srv, dev, "POST", "/api/services/acme/staging/api/copy?dry=1", `{"project":"acme","env":"production"}`); res.StatusCode != 403 {
		t.Fatalf("a Developer: %d", res.StatusCode)
	}
}
