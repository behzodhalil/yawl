package api

import (
	"strings"
	"testing"
)

func TestAPILogAlerts(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	path := "/api/services/acme/production/api/log-alerts"
	if res, b := call(t, srv, tok, "GET", path, ""); res.StatusCode != 200 || strings.TrimSpace(string(b)) != `{"rules":[]}` {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	res, b := call(t, srv, tok, "PUT", path, `{"rules":[{"lines":20,"minutes":5},{"text":"timeout","lines":0,"minutes":10}]}`)
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	got := decode[struct {
		Rules []struct {
			ID, Text       string
			Lines, Minutes int
			Open           bool
		}
	}](t, string(b))
	if len(got.Rules) != 2 || !strings.HasPrefix(got.Rules[0].ID, "la_") || got.Rules[0].Lines != 20 || got.Rules[1].Text != "timeout" || got.Rules[1].Minutes != 10 {
		t.Fatalf("%+v", got)
	}
	keep := got.Rules[0].ID
	_, b = call(t, srv, tok, "PUT", path, `{"rules":[{"id":"`+keep+`","lines":50,"minutes":5},{"id":"la_made_up","text":"x","lines":0,"minutes":1}]}`)
	again := decode[struct{ Rules []struct{ ID string } }](t, string(b))
	if len(again.Rules) != 2 || again.Rules[0].ID != keep || again.Rules[1].ID == "la_made_up" {
		t.Fatalf("%s", b)
	}
	testDB.Exec(`INSERT INTO settings (key, value) VALUES (?, '1 acme/production/api')`, "logalert.open."+keep)
	if _, b := call(t, srv, tok, "GET", path, ""); !strings.Contains(string(b), `"id":"`+keep+`","text":"","lines":50,"minutes":5,"open":true}`) {
		t.Fatalf("%s", b)
	}
	for _, bad := range []string{
		`{"rules":[{"lines":1,"minutes":0}]}`,
		`{"rules":[{"lines":1,"minutes":61}]}`,
		`{"rules":[{"lines":-1,"minutes":5}]}`,
		`{"rules":[{"lines":100001,"minutes":5}]}`,
		`{"rules":[{"text":"a\nb","lines":0,"minutes":5}]}`,
		`{"rules":[{"text":"` + strings.Repeat("a", 201) + `","lines":0,"minutes":5}]}`,
		`{"rules":[` + strings.Repeat(`{"lines":1,"minutes":5},`, 5) + `{"lines":1,"minutes":5}]}`,
	} {
		if res, b := call(t, srv, tok, "PUT", path, bad); res.StatusCode != 400 {
			t.Fatalf("%s: %d %s", bad, res.StatusCode, b)
		}
	}
	if res, _ := call(t, srv, tok, "PUT", "/api/services/acme/production/nope/log-alerts", `{"rules":[]}`); res.StatusCode != 404 {
		t.Fatalf("%d", res.StatusCode)
	}
	viewer := memberToken(t, srv, tok, "v@acme.uz", "viewer")
	if res, _ := call(t, srv, viewer, "GET", path, ""); res.StatusCode != 200 {
		t.Fatalf("a Viewer could not read: %d", res.StatusCode)
	}
	if res, _ := call(t, srv, viewer, "PUT", path, `{"rules":[]}`); res.StatusCode != 403 {
		t.Fatalf("a Viewer set them: %d", res.StatusCode)
	}
	dev := memberToken(t, srv, tok, "d@acme.uz", "developer")
	if res, b := call(t, srv, dev, "PUT", path, `{"rules":[]}`); res.StatusCode != 200 || strings.TrimSpace(string(b)) != `{"rules":[]}` {
		t.Fatalf("a Developer: %d %s", res.StatusCode, b)
	}
	if _, b := call(t, srv, tok, "GET", "/api/audit", ""); !strings.Contains(string(b), "changed the log alerts of acme/production/api") {
		t.Fatalf("%s", b)
	}
}
