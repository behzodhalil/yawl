package notify

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func hook(t *testing.T, status int, got *map[string]any) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, got)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/services/T000/B000/secret-part"
}

func TestSlackSend(t *testing.T) {
	var got map[string]any
	url := hook(t, 200, &got)
	if err := Slack(url).Send(context.Background(), Message{Text: "api is down"}); err != nil || got["text"] != "api is down" {
		t.Fatalf("%v %v", err, got)
	}
	bad := hook(t, 404, &got)
	err := Slack(bad).Send(context.Background(), Message{Text: "x"})
	if err == nil || strings.Contains(err.Error(), "secret-part") {
		t.Fatalf("error %v", err)
	}
}

func TestDiscordSend(t *testing.T) {
	var got map[string]any
	url := hook(t, 204, &got)
	if err := Discord(url).Send(context.Background(), Message{Text: strings.Repeat("a", 2500)}); err != nil {
		t.Fatal(err)
	}
	c := got["content"].(string)
	if utf8.RuneCountInString(c) != 2000 || !strings.HasSuffix(c, "…") {
		t.Fatalf("%d runes", utf8.RuneCountInString(c))
	}
}

func fakeSMTP(t *testing.T, starttls bool) (addr string, msg chan string) {
	return fakeSMTPOn(t, starttls, false)
}

func fakeSMTPOn(t *testing.T, starttls, implicit bool) (addr string, msg chan string) {
	cert := selfSigned(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if implicit {
		l = tls.NewListener(l, &tls.Config{Certificates: []tls.Certificate{cert}})
	}
	t.Cleanup(func() { l.Close() })
	msg = make(chan string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var conn net.Conn = c
		r, w := bufio.NewReader(conn), conn
		say := func(s string) { io.WriteString(w, s+"\r\n") }
		say("220 fake")
		tlsOn := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.Fields(line + " x")[0])
			switch cmd {
			case "EHLO", "HELO":
				if starttls && !tlsOn {
					say("250-fake")
					say("250-STARTTLS")
					say("250 AUTH PLAIN")
				} else {
					say("250-fake")
					say("250 AUTH PLAIN")
				}
			case "STARTTLS":
				say("220 go")
				tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}})
				conn, tlsOn = tc, true
				r, w = bufio.NewReader(tc), tc
			case "AUTH":
				say("235 ok")
			case "MAIL", "RCPT":
				if strings.Count(line, "<") > 1 { // as a real server refuses "MAIL FROM:<Yawl <a@b>>"
					say("501 bad address")
					continue
				}
				say("250 ok")
			case "DATA":
				say("354 go")
				var b strings.Builder
				for {
					l, _ := r.ReadString('\n')
					if l == ".\r\n" {
						break
					}
					b.WriteString(l)
				}
				msg <- b.String()
				say("250 ok")
			case "QUIT":
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	return l.Addr().String(), msg
}

func TestEmailSTARTTLS(t *testing.T) {
	addr, msg := fakeSMTP(t, true)
	host, port, _ := net.SplitHostPort(addr)
	e := Email(EmailConfig{Host: host, Port: port, Username: "yawl", Password: "pw", From: "yawl@acme.uz", To: "me@acme.uz"})
	e.(*email).tls = &tls.Config{InsecureSkipVerify: true}
	if err := e.Send(context.Background(), Message{Text: "api is down\nsince 03:00"}); err != nil {
		t.Fatal(err)
	}
	got := <-msg
	if !strings.Contains(got, "Subject: Yawl: api is down") || !strings.Contains(got, "since 03:00") || !strings.Contains(got, "To: <me@acme.uz>") {
		t.Fatalf("%s", got)
	}

	addr, _ = fakeSMTP(t, false)
	host, port, _ = net.SplitHostPort(addr)
	err := Email(EmailConfig{Host: host, Port: port, Username: "yawl", Password: "pw", From: "a@b", To: "c@d"}).Send(context.Background(), Message{Text: "x"})
	if err == nil || err.Error() != "The mail server offers no STARTTLS; Yawl will not send the password in clear." {
		t.Fatalf("%v", err)
	}
}

func TestEmailNamesSubjectAnd465(t *testing.T) {
	addr, msg := fakeSMTPOn(t, false, true)
	host, port, _ := net.SplitHostPort(addr)
	e := Email(EmailConfig{Host: host, Port: port, Username: "yawl", Password: "pw", From: "Yawl <yawl@acme.uz>", To: "Ops Team <ops@acme.uz>"})
	e.(*email).tls, e.(*email).tlsPort = &tls.Config{InsecureSkipVerify: true}, port
	if err := e.Send(context.Background(), Message{Text: "Ünïcode down\rBcc: evil@x.example " + strings.Repeat("x", 2000) + "\nbody"}); err != nil {
		t.Fatal(err)
	}
	got := <-msg
	head, _, _ := strings.Cut(got, "\r\n\r\n")
	subject := ""
	for _, l := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(l, "Subject: ") {
			subject = l
		}
		if strings.HasPrefix(l, "Bcc:") || strings.ContainsRune(l, '\r') {
			t.Fatalf("a header was injected: %q", l)
		}
	}
	if !strings.HasPrefix(subject, "Subject: =?utf-8?") || len(subject) > 998 || !strings.Contains(head, `From: "Yawl" <yawl@acme.uz>`) {
		t.Fatalf("%q", head)
	}

	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err == nil {
			defer c.Close()
			time.Sleep(20 * time.Second)
		}
	}()
	host, port, _ = net.SplitHostPort(l.Addr().String())
	e = Email(EmailConfig{Host: host, Port: port, From: "a@b", To: "c@d"})
	e.(*email).tlsPort = port
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := e.Send(ctx, Message{Text: "x"}); err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
}

func selfSigned(t *testing.T) tls.Certificate {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}
}

// Review (Minor 1, graded up): the webhook's secret never reaches an error,
// also when net/http prints the URL rewritten (a scheme in capitals).
func TestWebhookErrorHidesURL(t *testing.T) {
	err := Slack("HTTPS://127.0.0.1:1/services/T0/B0/very-secret").Send(context.Background(), Message{Text: "x"})
	if err == nil || strings.Contains(err.Error(), "very-secret") {
		t.Fatalf("%v", err)
	}
}

func TestWebhookSend(t *testing.T) {
	var got map[string]any
	url := hook(t, 202, &got)
	if err := Webhook(url).Send(context.Background(), Message{Text: "api is down"}); err != nil || got["text"] != "api is down" || got["source"] != "yawl" {
		t.Fatalf("%v %v", err, got)
	}
}

func TestNtfySend(t *testing.T) {
	var body, title, ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body, title, ctype = string(b), r.Header.Get("Title"), r.Header.Get("Content-Type")
	}))
	defer srv.Close()
	if err := Ntfy(srv.URL+"/yawl-alerts").Send(context.Background(), Message{Text: "api is down"}); err != nil || body != "api is down" || title != "Yawl" || !strings.HasPrefix(ctype, "text/plain") {
		t.Fatalf("%v %q %q %q", err, body, title, ctype)
	}
}
