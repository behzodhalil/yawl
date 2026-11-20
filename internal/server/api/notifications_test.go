package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNotificationChannels(t *testing.T) {
	srv, a, _, c := setupFirewall(t)
	got := make(chan string, 4)
	slack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- string(b)
	}))
	defer slack.Close()
	url := slack.URL + "/services/T0/B0/very-secret"
	if res, b := withCookie(t, srv, c, "PUT", "/api/settings/notifications/slack", `{"url":"`+url+`"}`, true); res.StatusCode != 200 {
		t.Fatalf("put %d %s", res.StatusCode, b)
	}
	var raw int
	testDB.QueryRow(`SELECT COUNT(*) FROM settings WHERE value LIKE '%very-secret%'`).Scan(&raw)
	if raw != 0 {
		t.Fatal("the webhook URL is stored in clear")
	}
	_, b := withCookie(t, srv, c, "GET", "/api/settings/notifications", "", false)
	var view map[string]map[string]any
	json.Unmarshal([]byte(b), &view)
	if view["slack"]["set"] != true || strings.Contains(b, "very-secret") || view["discord"]["set"] != false {
		t.Fatalf("view %s", b)
	}
	if res, b := withCookie(t, srv, c, "POST", "/api/settings/notifications/slack/test", "", true); res.StatusCode != 204 {
		t.Fatalf("test %d %s", res.StatusCode, b)
	}
	if m := <-got; !strings.Contains(m, "test message") {
		t.Fatalf("slack got %s", m)
	}
	if res, _ := withCookie(t, srv, c, "PUT", "/api/settings/notifications/email", `{"host":"smtp.acme.uz","port":"587","username":"u","password":"mail-secret","from":"y@acme.uz","to":"me@acme.uz"}`, true); res.StatusCode != 200 {
		t.Fatal("email put")
	}
	_, b = withCookie(t, srv, c, "GET", "/api/settings/notifications", "", false)
	if strings.Contains(b, "mail-secret") || !strings.Contains(b, "me@acme.uz") {
		t.Fatalf("email view %s", b)
	}
	if res, _ := withCookie(t, srv, c, "DELETE", "/api/settings/notifications/slack", "", true); res.StatusCode != 204 {
		t.Fatal("delete")
	}
	if res, _ := withCookie(t, srv, c, "PUT", "/api/settings/notifications/pager", `{}`, true); res.StatusCode != 404 {
		t.Fatal("an unknown channel was accepted")
	}
	adm, _ := a.St.Q.FirstAdmin(t.Context())
	link, _, _ := a.Invite(t.Context(), "developer", adm.ID)
	id, _ := a.AcceptInvite(t.Context(), link, "dev@acme.uz", "correct horse battery")
	s, _ := a.NewSession(t.Context(), id)
	if res, _ := withCookie(t, srv, &http.Cookie{Name: cookieName, Value: s}, "PUT", "/api/settings/notifications/slack", `{"url":"`+url+`"}`, true); res.StatusCode != 403 {
		t.Fatalf("developer %d", res.StatusCode)
	}
}

func TestWebhookAndNtfyChannels(t *testing.T) {
	srv, tok := setup(t)
	for _, c := range []struct{ name, url string }{{"webhook", "https://n8n.acme.uz/webhook/abc-secret"}, {"ntfy", "https://ntfy.sh/acme-alerts-secret"}} {
		if res, b := call(t, srv, tok, "PUT", "/api/settings/notifications/"+c.name, `{"url":"`+c.url+`"}`); res.StatusCode != 200 {
			t.Fatalf("%s: %d %s", c.name, res.StatusCode, b)
		}
	}
	_, b := call(t, srv, tok, "GET", "/api/settings/notifications", "")
	if !strings.Contains(string(b), `"webhook":{"host":"n8n.acme.uz","set":true}`) || !strings.Contains(string(b), `"ntfy":{"host":"ntfy.sh","set":true}`) || strings.Contains(string(b), "secret") {
		t.Fatalf("%s", b)
	}
	var raw string
	testDB.QueryRow(`SELECT group_concat(value) FROM settings`).Scan(&raw)
	if strings.Contains(raw, "abc-secret") || strings.Contains(raw, "acme-alerts-secret") {
		t.Fatal("stored in the clear")
	}
}

func TestNotificationBadURL(t *testing.T) {
	srv, tok := setup(t)
	for c, want := range map[string]string{"slack": "Paste the webhook URL Slack gave you.", "discord": "Paste the webhook URL Discord gave you.",
		"webhook": "Give the webhook's whole address, like https://n8n.acme.uz/webhook/alerts.", "ntfy": "Give the topic's whole address, like https://ntfy.sh/your-topic."} {
		if res, b := call(t, srv, tok, "PUT", "/api/settings/notifications/"+c, `{"url":"ftp://x"}`); res.StatusCode != 400 || strings.TrimSpace(string(b)) != want {
			t.Errorf("%s: %d %s", c, res.StatusCode, b)
		}
	}
}
