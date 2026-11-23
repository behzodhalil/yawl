package notify

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

type State string

const (
	Fail State = "fail"
	OK   State = "ok"
	Warn State = "warn"
)

type Message struct {
	State   State     `json:"state"`
	Head    string    `json:"head"`
	Badge   string    `json:"badge"`
	Line    string    `json:"line"`
	Fields  []Field   `json:"fields,omitempty"`
	At      time.Time `json:"at"`
	Next    string    `json:"next,omitempty"`
	Links   []Link    `json:"links,omitempty"`
	Text    string    `json:"text"`
	Server  string    `json:"server,omitempty"`
	Project string    `json:"project,omitempty"`
	Env     string    `json:"env,omitempty"`
	panel   string
}

type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Code  bool   `json:"code,omitempty"`
}

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

func New(state State, head, badge, line, text string) Message {
	return Message{State: state, Head: head, Badge: badge, Line: line, Text: text}
}

func (m Message) Add(label, value string) Message { return m.field(label, value, false) }

func (m Message) AddCode(label, value string) Message { return m.field(label, value, true) }

func (m Message) field(label, value string, code bool) Message {
	if value = strings.TrimSpace(value); value != "" {
		m.Fields = append(slices.Clip(m.Fields), Field{label, value, code})
	}
	return m
}

func (m Message) Then(next string) Message { m.Next = next; return m }

func (m Message) Open(label, url string) Message {
	m.Links = append(slices.Clip(m.Links), Link{label, url})
	return m
}

func (m Message) JSON(now int64) string {
	if m.State == "" {
		return ""
	}
	if m.At.IsZero() {
		m.At = time.UnixMilli(now)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// Linked makes panel paths absolute under base. Only an https base gives
// links: Telegram refuses a button to a bare address, and the whole message
// with it.
func (m Message) Linked(base string) Message {
	base = strings.TrimSuffix(base, "/")
	ok := strings.HasPrefix(base, "https://")
	var out []Link
	for _, l := range m.Links {
		if strings.HasPrefix(l.URL, "/") {
			if !ok {
				continue
			}
			l.URL = base + l.URL
		}
		out = append(out, l)
	}
	m.Links = out
	if u, err := url.Parse(base); ok && err == nil {
		m.panel = u.Host
	}
	return m
}

var clock = time.Now

const maxFields, maxValue = 8, 200

var colors = map[State]string{Fail: "#D23F5D", OK: "#1F8A5B", Warn: "#C98A12"}

// marks carry the state on Telegram and in plain text: none has emoji
// presentation; U+2714 would, and is never used.
var marks = map[State]string{Fail: "ⓧ", OK: "✓", Warn: "ⓘ"}

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// title keeps a Discord title under its 256 runes after escaping, which can
// double the line; a cut never leaves a lone escaping backslash.
func title(s string) string {
	if r := []rune(s); len(r) > 256 {
		return strings.TrimRight(string(r[:255]), "\\") + "…"
	}
	return s
}

func (m Message) rows(now time.Time) []Field {
	fs := m.Fields
	if len(fs) > maxFields-1 {
		fs = fs[:maxFields-1]
	}
	out := make([]Field, 0, len(fs)+1)
	for _, f := range fs {
		f.Value = cut(f.Value, maxValue)
		out = append(out, f)
	}
	if !m.At.IsZero() {
		out = append(out, Field{Label: "Time", Value: When(m.At, now)})
	}
	return out
}

func (m Message) links() []Link {
	if len(m.Links) > 2 {
		return m.Links[:2]
	}
	return m.Links
}

func (m Message) line() string { return cut(m.Line, maxValue) }
func (m Message) next() string { return cut(m.Next, maxValue) }

func When(t, now time.Time) string {
	if now.Sub(t) > 12*time.Hour {
		return Date(t)
	}
	return t.Format("15:04") + " " + zone(t)
}

func Date(t time.Time) string { return t.Format("Jan 2, 15:04") + " " + zone(t) }

func zone(t time.Time) string {
	_, off := t.Zone()
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	switch {
	case off == 0:
		return "UTC"
	case off%3600 == 0:
		return fmt.Sprintf("UTC%s%d", sign, off/3600)
	}
	return fmt.Sprintf("UTC%s%d:%02d", sign, off/3600, off%3600/60)
}

var tgEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace

// TelegramPayload is sendMessage's body but chat_id; the watcher gets
// it as is. Telegram counts 4,096 characters after the HTML is read,
// and 8 fields of 200 stay well inside.
func TelegramPayload(m Message, now time.Time) map[string]any {
	if m.State == "" {
		return map[string]any{"text": m.Text}
	}
	var b strings.Builder
	b.WriteString(marks[m.State] + " <b>" + tgEscape(m.line()) + "</b>\n")
	if rows := m.rows(now); len(rows) > 0 {
		b.WriteString("\n")
		for _, f := range rows {
			v := tgEscape(f.Value)
			if f.Code {
				v = "<code>" + v + "</code>"
			}
			b.WriteString(f.Label + ": " + v + "\n")
		}
	}
	if m.Next != "" {
		b.WriteString("\n" + marks[Warn] + " <i>Next: " + tgEscape(m.next()) + "</i>\n")
	}
	p := map[string]any{"text": strings.TrimSuffix(b.String(), "\n"), "parse_mode": "HTML", "disable_web_page_preview": true}
	if ls := m.links(); len(ls) > 0 {
		row := []map[string]string{}
		for _, l := range ls {
			row = append(row, map[string]string{"text": l.Label, "url": l.URL})
		}
		p["reply_markup"] = map[string]any{"inline_keyboard": [][]map[string]string{row}}
	}
	return p
}

func Plain(m Message, now time.Time) string {
	if m.State == "" {
		return m.Text
	}
	var b strings.Builder
	b.WriteString(marks[m.State] + " " + m.line() + "\n")
	if rows := m.rows(now); len(rows) > 0 {
		b.WriteString("\n")
		for _, f := range rows {
			b.WriteString(f.Label + ": " + f.Value + "\n")
		}
	}
	if m.Next != "" {
		b.WriteString("\n" + marks[Warn] + " Next: " + m.next() + "\n")
	}
	if ls := m.links(); len(ls) > 0 {
		b.WriteString("\n")
		for _, l := range ls {
			b.WriteString(l.Label + ": " + l.URL + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func slackBody(m Message, now time.Time) any {
	if m.State == "" {
		return map[string]string{"text": m.Text}
	}
	mrkdwn := func(s string) map[string]string { return map[string]string{"type": "mrkdwn", "text": s} }
	blocks := []any{map[string]any{"type": "section", "text": mrkdwn("*" + tgEscape(m.line()) + "*")}}
	var fields []any
	for _, f := range m.rows(now) {
		v := tgEscape(f.Value)
		if f.Code {
			v = "`" + strings.ReplaceAll(v, "`", "'") + "`"
		}
		fields = append(fields, mrkdwn("*"+f.Label+"*\n"+v))
	}
	if len(fields) > 0 {
		blocks = append(blocks, map[string]any{"type": "section", "fields": fields})
	}
	if ls := m.links(); len(ls) > 0 {
		var btns []any
		for _, l := range ls {
			btns = append(btns, map[string]any{"type": "button", "text": map[string]string{"type": "plain_text", "text": l.Label}, "url": l.URL})
		}
		blocks = append(blocks, map[string]any{"type": "actions", "elements": btns})
	}
	ctx := "Yawl"
	if m.panel != "" {
		ctx += " · " + m.panel
	}
	if m.Next != "" {
		ctx = "Next: " + tgEscape(m.next()) + " · " + ctx
	}
	blocks = append(blocks, map[string]string{"type": "divider"}, map[string]any{"type": "context", "elements": []any{mrkdwn(ctx)}})
	return map[string]any{"text": tgEscape(m.Text), "attachments": []any{map[string]any{"color": colors[m.State], "blocks": blocks}}}
}

var mdEscape = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "`", "\\`", "|", `\|`, ">", `\>`, "<", `\<`,
	"#", `\#`, "-", `\-`, "[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`).Replace

// discordBody is one embed coloured by state; a webhook Yawl does not own as
// an application cannot send buttons, so links go in the description.
func discordBody(m Message, now time.Time) any {
	none := map[string]any{"parse": []string{}} // an error line never pings anyone
	if m.State == "" {
		t := m.Text
		if r := []rune(t); len(r) > 2000 { // Discord refuses more than 2,000 characters
			t = string(r[:1999]) + "…"
		}
		return map[string]any{"content": t, "allowed_mentions": none}
	}
	var fields []any
	for _, f := range m.rows(now) {
		v := mdEscape(f.Value)
		if f.Code {
			v = "`" + strings.ReplaceAll(f.Value, "`", "'") + "`"
		}
		fields = append(fields, map[string]any{"name": f.Label, "value": v, "inline": true})
	}
	e := map[string]any{"color": hexInt(colors[m.State]), "author": map[string]string{"name": m.Head + " · " + m.Badge},
		"title": title(mdEscape(m.line())), "fields": fields, "timestamp": m.At.UTC().Format(time.RFC3339)}
	var ls []string
	for _, l := range m.links() {
		ls = append(ls, "["+mdEscape(l.Label)+"]("+l.URL+")")
	}
	if len(ls) > 0 {
		e["description"] = strings.Join(ls, " · ")
	}
	foot := "Yawl"
	if m.Next != "" {
		foot = "Next: " + m.next() + " · Yawl"
	}
	e["footer"] = map[string]string{"text": foot}
	return map[string]any{"embeds": []any{e}, "allowed_mentions": none}
}

func hexInt(c string) int {
	var n int
	fmt.Sscanf(strings.TrimPrefix(c, "#"), "%x", &n)
	return n
}

// emailHTML is a table card with inline styles: clients strip <style> and
// block SVG and remote images.
func emailHTML(m Message, now time.Time) string {
	e, c := html.EscapeString, colors[m.State]
	font := "font-family:-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;"
	rule := `<tr><td style="padding:0 20px"><div style="border-top:1px solid #E4E4E7;height:0;line-height:0">&nbsp;</div></td></tr>`
	var b strings.Builder
	b.WriteString(`<!doctype html><html><body style="margin:0;padding:16px;background:#F4F4F5">`)
	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" width="100%" style="max-width:560px;background:#FFFFFF;border:1px solid #E4E4E7;border-radius:8px;` + font + `font-size:14px;line-height:20px;color:#18181B">`)
	b.WriteString(`<tr><td style="padding:16px 20px"><span style="font-size:16px;font-weight:600">` + e(m.Head) + `</span>` +
		`<span style="display:inline-block;margin-left:8px;padding:1px 8px;border-radius:10px;border:1px solid ` + c + `;color:` + c + `;font-size:12px;font-weight:600">` + e(m.Badge) + `</span></td></tr>`)
	b.WriteString(rule)
	b.WriteString(`<tr><td style="padding:16px 20px 8px;font-weight:600">` + e(m.line()) + `</td></tr>`)
	for _, f := range m.rows(now) {
		v := e(f.Value)
		if f.Code {
			v = `<code style="font-family:Menlo,Consolas,monospace;font-size:13px">` + v + `</code>`
		}
		b.WriteString(`<tr><td style="padding:2px 20px">` + e(f.Label) + `: ` + v + `</td></tr>`)
	}
	b.WriteString(`<tr><td style="height:12px"></td></tr>`)
	var ls []string
	for _, l := range m.links() {
		ls = append(ls, `<a href="`+e(l.URL)+`" style="color:#18181B">`+e(l.Label)+`</a>`)
	}
	if m.Next != "" || len(ls) > 0 {
		b.WriteString(rule)
		p := strings.Join(ls, " · ")
		if m.Next != "" {
			p = strings.TrimSuffix("Next: "+e(m.next())+"<br>"+p, "<br>")
		}
		b.WriteString(`<tr><td style="padding:12px 20px 16px;color:#52525B">` + p + `</td></tr>`)
	}
	b.WriteString(`</table></body></html>`)
	return b.String()
}

func Drawn(m Message, now time.Time) map[string]string {
	j := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	return map[string]string{"telegram": j(TelegramPayload(m, now)), "slack": j(slackBody(m, now)), "discord": j(discordBody(m, now)),
		"email.text": Plain(m, now), "email.html": emailHTML(m, now), "ntfy": m.line() + "\n" + Plain(m, now),
		"webhook": j(map[string]any{"text": m.Text, "source": "yawl", "message": m})}
}
