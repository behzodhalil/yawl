package notify

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"
	"time"
)

var tashkent = time.FixedZone("", 5*3600)

func deployFailed() Message {
	return New("fail", "Deployment failed", "Failed", "Production deploy failed", "acme-api deploy failed: build exited 1. The previous version is still live.").
		AddCode("Service", "acme/production/api").AddCode("Commit", "3f0a1a3").Add("Stage", "Build").
		AddCode("Error", `npm ERR! Missing script: "build" <&>`).AddCode("Still serving", "9c41d2e").
		Then("Check the build log.").
		Open("Build log", "/p/acme/production/s/api/deploys/dep_1").Open("Open service", "/p/acme/production/s/api").
		withAt(time.Date(2026, 11, 5, 14, 14, 0, 0, tashkent)).Linked("https://panel.acme.uz")
}

func (m Message) withAt(t time.Time) Message { m.At = t; return m }

var soon = time.Date(2026, 11, 5, 14, 15, 0, 0, tashkent)

func TestTelegramPayload(t *testing.T) {
	p := TelegramPayload(deployFailed(), soon)
	want := "ⓧ <b>Production deploy failed</b>\n\n" +
		"Service: <code>acme/production/api</code>\n" +
		"Commit: <code>3f0a1a3</code>\n" +
		"Stage: Build\n" +
		"Error: <code>npm ERR! Missing script: \"build\" &lt;&amp;&gt;</code>\n" +
		"Still serving: <code>9c41d2e</code>\n" +
		"Time: 14:14 UTC+5\n\n" +
		"ⓘ <i>Next: Check the build log.</i>"
	if p["text"] != want || p["parse_mode"] != "HTML" || p["disable_web_page_preview"] != true {
		t.Fatalf("%q\n%v", p["text"], p)
	}
	b, _ := json.Marshal(p["reply_markup"])
	if string(b) != `{"inline_keyboard":[[{"text":"Build log","url":"https://panel.acme.uz/p/acme/production/s/api/deploys/dep_1"},{"text":"Open service","url":"https://panel.acme.uz/p/acme/production/s/api"}]]}` {
		t.Fatalf("%s", b)
	}
	ok := New("ok", "Service up", "Available", "Production service is back", "x").withAt(soon)
	if got := TelegramPayload(ok, soon)["text"]; got != "✓ <b>Production service is back</b>\n\nTime: 14:15 UTC+5" {
		t.Fatalf("%q", got)
	}
	if p := TelegramPayload(Message{Text: "a <b> deployed."}, soon); len(p) != 1 || p["text"] != "a <b> deployed." {
		t.Fatalf("%v", p)
	}
}

func TestWhen(t *testing.T) {
	at := time.Date(2026, 11, 5, 14, 14, 0, 0, tashkent)
	for _, c := range []struct {
		at   time.Time
		late time.Duration
		want string
	}{
		{at, time.Hour, "14:14 UTC+5"},
		{at, 13 * time.Hour, "Nov 5, 14:14 UTC+5"},
		{at.In(time.UTC), 0, "09:14 UTC"},
		{at.In(time.FixedZone("", 19800)), 0, "14:44 UTC+5:30"},
		{at.In(time.FixedZone("", -3*3600)), 0, "06:14 UTC-3"},
	} {
		if got := When(c.at, c.at.Add(c.late)); got != c.want {
			t.Errorf("%v: %q, want %q", c.at, got, c.want)
		}
	}
}

func TestLimitsAndLinks(t *testing.T) {
	m := New("warn", "h", "Alert", "l", "t").withAt(soon)
	for i := range 10 {
		m = m.Add(string(rune('A'+i)), strings.Repeat("x", 300))
	}
	rows := m.rows(soon)
	if len(rows) != 8 || rows[7].Label != "Time" || len([]rune(rows[0].Value)) != 200 || !strings.HasSuffix(rows[0].Value, "…") {
		t.Fatalf("%d rows, last %q, first %d runes", len(rows), rows[len(rows)-1].Label, len([]rune(rows[0].Value)))
	}
	if m.Add("Empty", " ").Fields[len(m.Fields)-1].Label == "Empty" {
		t.Fatal("an empty value became a field")
	}
	l := m.Open("A", "/a").Open("B", "https://app.example").Open("C", "/c")
	if got := l.Linked("http://10.0.0.1:8080").Links; len(got) != 1 || got[0].URL != "https://app.example" {
		t.Fatalf("%v", got)
	}
	if got := l.Linked("https://panel.acme.uz/").links(); len(got) != 2 || got[0].URL != "https://panel.acme.uz/a" {
		t.Fatalf("%v", got)
	}
}

func TestSlackBody(t *testing.T) {
	b, _ := json.Marshal(slackBody(deployFailed(), soon))
	var got struct {
		Text        string
		Attachments []struct {
			Color  string
			Blocks []struct {
				Type     string
				Text     struct{ Text string }
				Fields   []struct{ Text string }
				Elements []map[string]any
			}
		}
	}
	json.Unmarshal(b, &got)
	a := got.Attachments[0]
	var types []string
	for _, bl := range a.Blocks {
		types = append(types, bl.Type)
	}
	if got.Text != "acme-api deploy failed: build exited 1. The previous version is still live." || a.Color != "#D23F5D" ||
		strings.Join(types, " ") != "section section actions divider context" || a.Blocks[0].Text.Text != "*Production deploy failed*" ||
		a.Blocks[1].Fields[3].Text != "*Error*\n`npm ERR! Missing script: \"build\" &lt;&amp;&gt;`" || len(a.Blocks[2].Elements) != 2 ||
		a.Blocks[4].Elements[0]["text"] != "Next: Check the build log. · Yawl · panel.acme.uz" {
		t.Fatalf("%s", b)
	}
}

func TestDiscordBody(t *testing.T) {
	m := deployFailed().Add("Stage", "build_*x*")
	b, _ := json.Marshal(discordBody(m, soon))
	var got struct {
		Embeds []struct {
			Color       int
			Author      struct{ Name string }
			Title       string
			Description string
			Fields      []struct {
				Name, Value string
				Inline      bool
			}
			Footer    struct{ Text string }
			Timestamp string
		}
		AllowedMentions struct{ Parse []string } `json:"allowed_mentions"`
	}
	json.Unmarshal(b, &got)
	e := got.Embeds[0]
	if e.Color != 0xD23F5D || e.Author.Name != "Deployment failed · Failed" || e.Title != "Production deploy failed" ||
		e.Description != "[Build log](https://panel.acme.uz/p/acme/production/s/api/deploys/dep_1) · [Open service](https://panel.acme.uz/p/acme/production/s/api)" ||
		e.Fields[0] != (struct {
			Name, Value string
			Inline      bool
		}{"Service", "`acme/production/api`", true}) || e.Fields[5].Value != `build\_\*x\*` ||
		e.Footer.Text != "Next: Check the build log. · Yawl" || e.Timestamp != "2026-11-05T09:14:00Z" ||
		got.AllowedMentions.Parse == nil || len(got.AllowedMentions.Parse) != 0 {
		t.Fatalf("%s", b)
	}
}

func TestEmailAlternative(t *testing.T) {
	addr, msg := fakeSMTP(t, true)
	host, port, _ := net.SplitHostPort(addr)
	e := Email(EmailConfig{Host: host, Port: port, From: "yawl@acme.uz", To: "me@acme.uz"})
	e.(*email).tls = &tls.Config{InsecureSkipVerify: true}
	clock = func() time.Time { return soon }
	defer func() { clock = time.Now }()
	if err := e.Send(context.Background(), deployFailed()); err != nil {
		t.Fatal(err)
	}
	raw := <-msg
	mm, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	subject, _ := new(mime.WordDecoder).DecodeHeader(mm.Header.Get("Subject"))
	mt, params, _ := mime.ParseMediaType(mm.Header.Get("Content-Type"))
	if subject != "Yawl: acme-api deploy failed: build exited 1. The previous version is still live." || mt != "multipart/alternative" {
		t.Fatalf("%q %q", subject, mt)
	}
	r := multipart.NewReader(mm.Body, params["boundary"])
	var parts []string
	for {
		p, err := r.NextPart()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(p)
		parts = append(parts, p.Header.Get("Content-Type")+"\n"+strings.ReplaceAll(string(b), "\r\n", "\n")) // quoted-printable ends lines in CRLF
	}
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "text/plain") || !strings.Contains(parts[0], "ⓧ Production deploy failed\n\nService: acme/production/api") ||
		!strings.Contains(parts[0], "Build log: https://panel.acme.uz/p/acme/production/s/api/deploys/dep_1") {
		t.Fatalf("%q", parts)
	}
	h := parts[1]
	if !strings.HasPrefix(h, "text/html") || !strings.Contains(h, `<table role="presentation"`) || !strings.Contains(h, "color:#D23F5D") ||
		!strings.Contains(h, "&lt;&amp;&gt;") || strings.Contains(h, "<svg") || strings.Contains(h, "<img") || strings.Contains(h, "<style") {
		t.Fatalf("%s", h)
	}
}

func TestNtfyAndWebhookMessage(t *testing.T) {
	var hdr http.Header
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hdr, body = r.Header, string(b)
	}))
	defer srv.Close()
	if err := Ntfy(srv.URL).Send(context.Background(), deployFailed()); err != nil {
		t.Fatal(err)
	}
	if hdr.Get("Title") != "Production deploy failed" || hdr.Get("Priority") != "4" || hdr.Get("Click") != "https://panel.acme.uz/p/acme/production/s/api/deploys/dep_1" ||
		hdr.Get("Tags") != "" || !strings.HasPrefix(body, "ⓧ Production deploy failed\n") {
		t.Fatalf("%v %q", hdr, body)
	}
	var got map[string]any
	url := hook(t, 200, &got)
	if err := Webhook(url).Send(context.Background(), deployFailed()); err != nil {
		t.Fatal(err)
	}
	m, _ := got["message"].(map[string]any)
	if got["source"] != "yawl" || got["text"] != deployFailed().Text || m["state"] != "fail" || len(m["fields"].([]any)) != 5 {
		t.Fatalf("%v", got)
	}
}

// A line of markdown characters doubles when escaped; Discord refuses a
// title over 256 runes, and the whole message with it (review 71 M1).
func TestDiscordTitleAfterEscape(t *testing.T) {
	m := New("warn", "Log alert", "Alert", strings.Repeat("-", 200), "x")
	title := discordBody(m, soon).(map[string]any)["embeds"].([]any)[0].(map[string]any)["title"].(string)
	if n := len([]rune(title)); n > 256 || strings.HasSuffix(strings.TrimSuffix(title, "…"), `\`) {
		t.Fatalf("title is %d runes, ends %q", n, title[len(title)-6:])
	}
}
