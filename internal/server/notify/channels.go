package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/egress"
)

type webhook struct {
	name, url string
	body      func(m Message) any
	hc        *http.Client
	plain     bool
}

func Webhook(url string) Notifier {
	return &webhook{name: "webhook", url: url, body: func(m Message) any {
		out := map[string]any{"text": m.Text, "source": "yawl"}
		if m.State != "" {
			out["message"] = m
		}
		return out
	}, hc: &http.Client{Timeout: 15 * time.Second}}
}

func Ntfy(url string) Notifier {
	return &webhook{name: "ntfy", url: url, plain: true, hc: &http.Client{Timeout: 15 * time.Second}}
}

func Slack(url string) Notifier {
	return &webhook{name: "slack", url: url, body: func(m Message) any { return slackBody(m, clock()) }, hc: &http.Client{Timeout: 15 * time.Second}}
}

func Discord(url string) Notifier {
	return &webhook{name: "discord", url: url, body: func(m Message) any { return discordBody(m, clock()) }, hc: &http.Client{Timeout: 15 * time.Second}}
}

// ntfyPriority is C5's: no Tags, which ntfy draws as emoji.
var ntfyPriority = map[State]string{Fail: "4", Warn: "3", OK: "2"}

func (w *webhook) Send(ctx context.Context, m Message) error {
	b, ctype := []byte(Plain(m, clock())), "text/plain; charset=utf-8"
	if !w.plain {
		b, _ = json.Marshal(w.body(m))
		ctype = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(b))
	if err != nil {
		return errors.New(w.name + ": the webhook URL is not valid") // the parse error would quote it
	}
	req.Header.Set("Content-Type", ctype)
	if w.plain {
		req.Header.Set("Title", "Yawl")
		if m.State != "" { // a header is one line, encoded when not ASCII (RFC 2047, which ntfy reads)
			req.Header.Set("Title", mime.BEncoding.Encode("utf-8", strings.Join(strings.Fields(m.line()), " ")))
			req.Header.Set("Priority", ntfyPriority[m.State])
			if ls := m.links(); len(ls) > 0 {
				req.Header.Set("Click", ls[0].URL)
			}
		}
	}
	res, err := w.hc.Do(req)
	if err != nil {
		var ue *url.Error // it prints the URL, rewritten: no replace could catch every spelling
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("%s: %v", w.name, err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s answered %d", w.name, res.StatusCode)
	}
	return nil
}

type EmailConfig struct{ Host, Port, Username, Password, From, To string }

type email struct {
	c       EmailConfig
	tls     *tls.Config
	tlsPort string
}

func Email(c EmailConfig) Notifier { return &email{c: c, tlsPort: "465"} }

func (e *email) Send(ctx context.Context, m Message) error {
	cfg := e.tls
	if cfg == nil {
		cfg = &tls.Config{ServerName: e.c.Host}
	}
	addr := net.JoinHostPort(e.c.Host, e.c.Port)
	d := &net.Dialer{Timeout: 15 * time.Second, Control: egress.Control}
	var conn net.Conn
	var err error
	if e.c.Port == e.tlsPort {
		conn, err = (&tls.Dialer{NetDialer: d, Config: cfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("email: %w", err)
	}
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	c, err := smtp.NewClient(conn, e.c.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("email: %w", err)
	}
	defer c.Close()
	if e.c.Port != e.tlsPort {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("The mail server offers no STARTTLS; Yawl will not send the password in clear.")
		}
		if err := c.StartTLS(cfg); err != nil {
			return fmt.Errorf("email: STARTTLS: %w", err)
		}
	}
	if e.c.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", e.c.Username, e.c.Password, e.c.Host)); err != nil {
			return fmt.Errorf("email: the server refused the login: %w", err)
		}
	}
	// A display name (Yawl <a@b>) is for the header; MAIL and RCPT take the address alone.
	from, err := mail.ParseAddress(e.c.From)
	if err != nil {
		return fmt.Errorf("email: From: %w", err)
	}
	to, err := mail.ParseAddress(e.c.To)
	if err != nil {
		return fmt.Errorf("email: To: %w", err)
	}
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: %w", err)
	}
	// The subject is Text's first line, with no CR to start another header, cut
	// short and encoded when not ASCII (RFC 2047); the body's lines end in CRLF.
	text := strings.ReplaceAll(m.Text, "\r", "")
	first, _, _ := strings.Cut(text, "\n")
	subject := []rune(strings.TrimSpace(first))
	if len(subject) > 100 {
		subject = append(subject[:100], '…')
	}
	fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\n", from, to, mime.BEncoding.Encode("utf-8", "Yawl: "+string(subject)))
	if m.State == "" {
		fmt.Fprintf(w, "Content-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", strings.ReplaceAll(text, "\n", "\r\n"))
	} else if err := alternative(w, m, clock()); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	return c.Quit()
}

// alternative is the body as multipart/alternative: the plain text, then the
// HTML card, each quoted-printable, so no line runs
// past SMTP's 998.
func alternative(w io.Writer, m Message, now time.Time) error {
	mw := multipart.NewWriter(w)
	fmt.Fprintf(w, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", mw.Boundary())
	for _, p := range [][2]string{{"text/plain", Plain(m, now)}, {"text/html", emailHTML(m, now)}} {
		pw, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {p[0] + "; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}})
		if err != nil {
			return err
		}
		qw := quotedprintable.NewWriter(pw)
		if _, err := io.WriteString(qw, p[1]); err != nil {
			return err
		}
		if err := qw.Close(); err != nil {
			return err
		}
	}
	return mw.Close()
}
