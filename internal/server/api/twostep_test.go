package api

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
)

func rfc6238Code(secret string, at time.Time, plus int64) string {
	raw, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	m := hmac.New(sha1.New, raw)
	binary.Write(m, binary.BigEndian, at.Unix()/30+plus)
	s := m.Sum(nil)
	o := s[len(s)-1] & 15
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(s[o:o+4])&0x7fffffff)%1_000_000)
}

func clock(a *app.App) func(time.Duration) {
	now := time.Unix(1_800_000_000, 0)
	a.Now = func() time.Time { return now }
	return func(d time.Duration) { now = now.Add(d) }
}

func decode[T any](t *testing.T, b string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(b), &v); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	return v
}

type setupOut struct {
	Secret string   `json:"secret"`
	URI    string   `json:"uri"`
	QR     string   `json:"qr"`
	Codes  []string `json:"recovery_codes"`
}

func twoStepOn(t *testing.T, srv *httptest.Server, a *app.App, c *http.Cookie) (string, []string) {
	t.Helper()
	res, b := withCookie(t, srv, c, "POST", "/api/account/two-step", "", true)
	if res.StatusCode != 200 {
		t.Fatalf("start %d %s", res.StatusCode, b)
	}
	st := decode[setupOut](t, b)
	if !strings.HasPrefix(st.QR, "<svg") || !strings.HasPrefix(st.URI, "otpauth://totp/panel.test:") {
		t.Fatalf("%+v", st)
	}
	res, b = withCookie(t, srv, c, "PUT", "/api/account/two-step", `{"code":"`+rfc6238Code(st.Secret, a.Now(), 0)+`"}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("confirm %d %s", res.StatusCode, b)
	}
	return st.Secret, decode[setupOut](t, b).Codes
}

type signInOut struct {
	Second string `json:"second"`
	Ticket string `json:"ticket"`
}

func sessionCookie(res *http.Response) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name == cookieName && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestSignInTwoStep(t *testing.T) {
	srv, _, a := setupApp(t)
	clock(a)
	c := claimed(t, srv, a)
	secret, _ := twoStepOn(t, srv, a, c)

	res, b := call(t, srv, "", "POST", "/api/session", owner)
	in := decode[signInOut](t, string(b))
	if res.StatusCode != 200 || sessionCookie(res) != nil || in.Second != "code" || in.Ticket == "" {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	res, b = call(t, srv, "", "POST", "/api/session/code", `{"ticket":"`+in.Ticket+`","code":"000000"}`)
	if res.StatusCode != 401 || strings.TrimSpace(string(b)) != "Wrong code." {
		t.Fatalf("wrong code: %d %s", res.StatusCode, b)
	}
	res, b = call(t, srv, "", "POST", "/api/session/code", `{"ticket":"`+in.Ticket+`","code":"`+rfc6238Code(secret, a.Now(), 1)+`"}`)
	if res.StatusCode != 200 || sessionCookie(res) == nil {
		t.Fatalf("right code: %d %s", res.StatusCode, b)
	}
	res, b = call(t, srv, "", "POST", "/api/session/code", `{"ticket":"`+in.Ticket+`","code":"`+rfc6238Code(secret, a.Now(), -1)+`"}`)
	if res.StatusCode != 401 || strings.TrimSpace(string(b)) != "This sign in has expired. Start again." {
		t.Fatalf("used ticket: %d %s", res.StatusCode, b)
	}
}

func TestTicketLimits(t *testing.T) {
	srv, _, a := setupApp(t)
	move := clock(a)
	c := claimed(t, srv, a)
	secret, _ := twoStepOn(t, srv, a, c)
	ticket := func() string {
		_, b := call(t, srv, "", "POST", "/api/session", owner)
		return decode[signInOut](t, string(b)).Ticket
	}
	tk := ticket()
	for range 5 {
		call(t, srv, "", "POST", "/api/session/code", `{"ticket":"`+tk+`","code":"000000"}`)
	}
	if res, b := call(t, srv, "", "POST", "/api/session/code", `{"ticket":"`+tk+`","code":"`+rfc6238Code(secret, a.Now(), 1)+`"}`); res.StatusCode != 401 || !strings.Contains(string(b), "expired") {
		t.Fatalf("after 5 tries: %d %s", res.StatusCode, b)
	}
	move(10 * time.Minute)
	tk = ticket()
	move(5*time.Minute + time.Second)
	if res, b := call(t, srv, "", "POST", "/api/session/code", `{"ticket":"`+tk+`","code":"`+rfc6238Code(secret, a.Now(), 0)+`"}`); res.StatusCode != 401 || !strings.Contains(string(b), "expired") {
		t.Fatalf("old ticket: %d %s", res.StatusCode, b)
	}
}

func TestSetupWhenRequired(t *testing.T) {
	srv, _, a := setupApp(t)
	clock(a)
	c := claimed(t, srv, a)
	twoStepOn(t, srv, a, c)
	if res, b := withCookie(t, srv, c, "PUT", "/api/settings/two-step", `{"required":true}`, true); res.StatusCode != 200 {
		t.Fatalf("required: %d %s", res.StatusCode, b)
	}
	adm, _ := a.St.Q.FirstAdmin(t.Context())
	link, _, _ := a.Invite(t.Context(), "developer", adm.ID)
	res, b := call(t, srv, "", "POST", "/api/invites/accept", `{"token":"`+link+`","email":"dev@acme.uz","password":"correct horse battery"}`)
	in := decode[signInOut](t, string(b))
	if res.StatusCode != 200 || sessionCookie(res) != nil || in.Second != "setup" {
		t.Fatalf("invite: %d %s", res.StatusCode, b)
	}
	res, b = call(t, srv, "", "POST", "/api/session", `{"email":"dev@acme.uz","password":"correct horse battery"}`)
	in = decode[signInOut](t, string(b))
	if in.Second != "setup" || sessionCookie(res) != nil {
		t.Fatalf("sign in: %s", b)
	}
	if res, _ := call(t, srv, "", "POST", "/api/session/code", `{"ticket":"`+in.Ticket+`","code":"000000"}`); res.StatusCode != 401 {
		t.Fatal("a setup ticket took a code")
	}
	_, b = call(t, srv, "", "POST", "/api/session/setup", `{"ticket":"`+in.Ticket+`"}`)
	st := decode[setupOut](t, string(b))
	if !strings.HasPrefix(st.QR, "<svg") {
		t.Fatalf("setup: %s", b)
	}
	res, b = call(t, srv, "", "POST", "/api/session/setup/confirm", `{"ticket":"`+in.Ticket+`","code":"`+rfc6238Code(st.Secret, a.Now(), 0)+`"}`)
	if res.StatusCode != 200 || sessionCookie(res) == nil || len(decode[setupOut](t, string(b)).Codes) != 10 {
		t.Fatalf("confirm: %d %s", res.StatusCode, b)
	}
}

func TestTwoStepRoles(t *testing.T) {
	srv, _, a := setupApp(t)
	clock(a)
	c := claimed(t, srv, a)
	adm, _ := a.St.Q.FirstAdmin(t.Context())
	link, _, _ := a.Invite(t.Context(), "developer", adm.ID)
	devID, _ := a.AcceptInvite(t.Context(), link, "dev@acme.uz", "correct horse battery")
	devSession, _ := a.NewSession(t.Context(), devID)
	dev := &http.Cookie{Name: cookieName, Value: devSession}
	twoStepOn(t, srv, a, dev)
	if res, _ := withCookie(t, srv, dev, "DELETE", "/api/members/"+adm.ID+"/two-step", "", true); res.StatusCode != 403 {
		t.Fatalf("developer reset: %d", res.StatusCode)
	}
	if res, _ := withCookie(t, srv, dev, "PUT", "/api/settings/two-step", `{"required":true}`, true); res.StatusCode != 403 {
		t.Fatalf("developer required: %d", res.StatusCode)
	}
	if res, _ := withCookie(t, srv, c, "DELETE", "/api/members/"+adm.ID+"/two-step", "", true); res.StatusCode != 400 {
		t.Fatalf("own reset: %d", res.StatusCode)
	}
	if res, b := withCookie(t, srv, c, "DELETE", "/api/members/"+devID+"/two-step", "", true); res.StatusCode != 204 {
		t.Fatalf("admin reset: %d %s", res.StatusCode, b)
	}
	_, b := withCookie(t, srv, c, "GET", "/api/members", "", false)
	if !strings.Contains(b, `"two_step":false`) {
		t.Fatalf("members: %s", b)
	}
	if res, b := withCookie(t, srv, c, "PUT", "/api/settings/two-step", `{"required":true}`, true); res.StatusCode != 400 || !strings.Contains(b, "yourself first") {
		t.Fatalf("required without own: %d %s", res.StatusCode, b)
	}
}

func TestRecoveryCodeAudited(t *testing.T) {
	srv, _, a := setupApp(t)
	clock(a)
	c := claimed(t, srv, a)
	_, codes := twoStepOn(t, srv, a, c)
	_, b := call(t, srv, "", "POST", "/api/session", owner)
	tk := decode[signInOut](t, string(b)).Ticket
	if res, b := call(t, srv, "", "POST", "/api/session/code", `{"ticket":"`+tk+`","code":"`+codes[0]+`"}`); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	_, audit := withCookie(t, srv, c, "GET", "/api/audit", "", false)
	for _, want := range []string{"signed in with a recovery code", "turned on two-step sign in"} {
		if !strings.Contains(audit, want) {
			t.Fatalf("audit lacks %q: %s", want, audit)
		}
	}
}

func TestTwoStepChangeNeedsSessionAndCode(t *testing.T) {
	srv, tok, a := setupApp(t)
	clock(a)
	c := claimed(t, srv, a)
	if res, _ := call(t, srv, tok, "POST", "/api/account/two-step", ""); res.StatusCode != 403 {
		t.Fatalf("an API token started a setup: %d", res.StatusCode)
	}
	secret, _ := twoStepOn(t, srv, a, c)
	if res, b := withCookie(t, srv, c, "POST", "/api/account/two-step", `{}`, true); res.StatusCode != 401 {
		t.Fatalf("a new secret without a code: %d %s", res.StatusCode, b)
	}
	res, b := withCookie(t, srv, c, "POST", "/api/account/two-step", `{"code":"`+rfc6238Code(secret, a.Now(), 1)+`"}`, true)
	if res.StatusCode != 200 || !strings.Contains(b, `"qr"`) {
		t.Fatalf("with a code: %d %s", res.StatusCode, b)
	}
}

func TestTicketTriesInParallel(t *testing.T) {
	srv, _, a := setupApp(t)
	clock(a)
	c := claimed(t, srv, a)
	twoStepOn(t, srv, a, c)
	_, b := call(t, srv, "", "POST", "/api/session", owner)
	tk := decode[signInOut](t, string(b)).Ticket
	const n = 20
	wrong := make(chan bool, n)
	for range n {
		go func() {
			res, b := call(t, srv, "", "POST", "/api/session/code", `{"ticket":"`+tk+`","code":"000000"}`)
			wrong <- res.StatusCode == 401 && strings.TrimSpace(string(b)) == "Wrong code."
		}()
	}
	checked := 0
	for range n {
		if <-wrong {
			checked++
		}
	}
	if checked > ticketTries {
		t.Fatalf("%d codes checked on one ticket", checked)
	}
}

func TestLimiterTakeIsAtomic(t *testing.T) {
	l := &limiter{fail: map[string][]time.Time{}}
	now := time.Unix(1_800_000_000, 0)
	got := make(chan bool, 50)
	for range 50 {
		go func() { got <- l.take("1.2.3.4", now) }()
	}
	ok := 0
	for range 50 {
		if <-got {
			ok++
		}
	}
	if ok != maxFails {
		t.Fatalf("%d attempts let through, want %d", ok, maxFails)
	}
}

func TestAccountCodesLimited(t *testing.T) {
	srv, _, a := setupApp(t)
	clock(a)
	c := claimed(t, srv, a)
	twoStepOn(t, srv, a, c)
	var got []int
	for range 11 {
		res, _ := withCookie(t, srv, c, "DELETE", "/api/account/two-step", `{"code":"000000"}`, true)
		got = append(got, res.StatusCode)
	}
	if got[0] != 401 || got[10] != 429 {
		t.Fatalf("%v", got)
	}
}

func TestIssuerHasNoPort(t *testing.T) {
	if got := (&handler{d: Deps{PublicURL: "https://panel.acme.uz:8443"}}).issuer(); got != "panel.acme.uz" {
		t.Fatal(got)
	}
}

func TestRequiredAuditSaysWhichWay(t *testing.T) {
	srv, _, a := setupApp(t)
	clock(a)
	c := claimed(t, srv, a)
	twoStepOn(t, srv, a, c)
	for _, on := range []string{"true", "false"} {
		if res, b := withCookie(t, srv, c, "PUT", "/api/settings/two-step", `{"required":`+on+`}`, true); res.StatusCode != 200 {
			t.Fatalf("%d %s", res.StatusCode, b)
		}
	}
	got := audits(t)
	if len(got) < 2 || got[len(got)-2] != "me@acme.uz: turned on required two-step sign in" || got[len(got)-1] != "me@acme.uz: turned off required two-step sign in" {
		t.Fatalf("%q", got)
	}
}
