package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
)

const owner = `{"email":"me@acme.uz","password":"correct horse battery"}`

func claimed(t *testing.T, srv *httptest.Server, a *app.App) *http.Cookie {
	t.Helper()
	tok, err := a.ClaimLink(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	res, b := call(t, srv, "", "POST", "/api/claim", `{"token":"`+tok+`",`+owner[1:])
	if res.StatusCode != 200 {
		t.Fatalf("claim %d %s", res.StatusCode, b)
	}
	for _, c := range res.Cookies() {
		if c.Name == "yawl_session" {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func withCookie(t *testing.T, srv *httptest.Server, c *http.Cookie, method, path, body string, header bool) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	req.AddCookie(c)
	if header {
		req.Header.Set("X-Yawl-Request", "1")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := readAll(res)
	return res, string(b)
}

func TestSessionCSRF(t *testing.T) {
	srv, tok, a := setupApp(t)
	c := claimed(t, srv, a)
	if res, b := withCookie(t, srv, c, "POST", "/api/apply", yaml, false); res.StatusCode != 403 || !strings.Contains(b, "Missing X-Yawl-Request header.") {
		t.Fatalf("no header: %d %s", res.StatusCode, b)
	}
	var n int
	testDB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'spec.applied'`).Scan(&n)
	if n != 0 {
		t.Fatal("a forged write applied the spec")
	}
	if res, b := withCookie(t, srv, c, "POST", "/api/apply", yaml, true); res.StatusCode != 200 {
		t.Fatalf("with header: %d %s", res.StatusCode, b)
	}
	testDB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'spec.applied' AND actor = 'me@acme.uz'`).Scan(&n)
	if n != 1 {
		t.Fatal("the owner's apply was not recorded as the owner")
	}
	if res, _ := withCookie(t, srv, c, "GET", "/api/status", "", false); res.StatusCode != 200 {
		t.Fatalf("cookie GET: %d", res.StatusCode)
	}
	if res, b := call(t, srv, tok, "POST", "/api/apply", yaml); res.StatusCode != 200 {
		t.Fatalf("bearer without header: %d %s", res.StatusCode, b)
	}
	a.Now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	if res, _ := withCookie(t, srv, c, "GET", "/api/status", "", false); res.StatusCode != 401 {
		t.Fatalf("expired session: %d", res.StatusCode)
	}
	a.Now = time.Now
	if res, _ := withCookie(t, srv, c, "DELETE", "/api/session", "", true); res.StatusCode != 204 {
		t.Fatalf("sign out: %d", res.StatusCode)
	}
	if res, _ := withCookie(t, srv, c, "GET", "/api/status", "", false); res.StatusCode != 401 {
		t.Fatalf("after sign out: %d", res.StatusCode)
	}
}

func TestSessionState(t *testing.T) {
	srv, _, a := setupApp(t)
	if _, b := call(t, srv, "", "GET", "/api/session", ""); !strings.Contains(string(b), `"claimed":false`) {
		t.Fatal(b)
	}
	c := claimed(t, srv, a)
	if _, b := withCookie(t, srv, c, "GET", "/api/session", "", false); !strings.Contains(b, `"claimed":true`) || !strings.Contains(b, "me@acme.uz") {
		t.Fatal(b)
	}
	tok, _ := a.ClaimLink(context.Background())
	if tok != "" {
		t.Fatal("a claim link was made after the owner")
	}
}

func TestClaimTwiceHTTP(t *testing.T) {
	srv, _, a := setupApp(t)
	tok, _ := a.ClaimLink(context.Background())
	call(t, srv, "", "POST", "/api/claim", `{"token":"`+tok+`",`+owner[1:])
	if res, b := call(t, srv, "", "POST", "/api/claim", `{"token":"`+tok+`",`+owner[1:]); res.StatusCode != 409 || !strings.Contains(string(b), "This panel already has an owner.") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestSignInLimit(t *testing.T) {
	srv, _, a := setupApp(t)
	claimed(t, srv, a)
	bad := `{"email":"me@acme.uz","password":"not the password"}`
	from := func(ip string) int {
		req := httptest.NewRequest("POST", "/api/session", strings.NewReader(bad))
		req.RemoteAddr = ip + ":5000"
		rec := httptest.NewRecorder()
		srv.Config.Handler.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := range 10 {
		if c := from("10.0.0.1"); c != 401 {
			t.Fatalf("attempt %d: %d", i+1, c)
		}
	}
	if c := from("10.0.0.1"); c != 429 {
		t.Fatalf("11th: %d", c)
	}
	if c := from("10.0.0.2"); c != 401 {
		t.Fatalf("other address: %d", c)
	}
}

func TestSignInLimitConcurrentBypass(t *testing.T) {
	srv, _, a := setupApp(t)
	claimed(t, srv, a)
	const n = 25
	bad := `{"email":"me@acme.uz","password":"not the password"}`
	codes := make([]int, n)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/session", strings.NewReader(bad))
			req.RemoteAddr = "10.0.0.9:5000"
			rec := httptest.NewRecorder()
			<-gate
			srv.Config.Handler.ServeHTTP(rec, req)
			codes[i] = rec.Code
		}(i)
	}
	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()

	var got401, got429 int
	for _, c := range codes {
		switch c {
		case 401:
			got401++
		case 429:
			got429++
		}
	}
	t.Logf("burst of %d from one address: 401=%d 429=%d (cap maxFails=%d)", n, got401, got429, maxFails)
	if got401 != maxFails || got429 != n-maxFails {
		t.Fatalf("burst passed the cap: 401=%d 429=%d, want %d and %d", got401, got429, maxFails, n-maxFails)
	}
}

func TestSignInLimitSparesSuccess(t *testing.T) {
	srv, _, a := setupApp(t)
	claimed(t, srv, a)
	for i := range maxFails + 2 {
		req := httptest.NewRequest("POST", "/api/session", strings.NewReader(owner))
		req.RemoteAddr = "10.0.0.7:5000"
		rec := httptest.NewRecorder()
		srv.Config.Handler.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("sign-in %d: %d", i+1, rec.Code)
		}
	}
}

func TestCookieFlags(t *testing.T) {
	srv, _, a := setupApp(t)
	claimed(t, srv, a)
	signIn := func(proto string) string {
		req := httptest.NewRequest("POST", "/api/session", strings.NewReader(owner))
		req.RemoteAddr = "127.0.0.1:5000"
		if proto != "" {
			req.Header.Set("X-Forwarded-Proto", proto)
		}
		rec := httptest.NewRecorder()
		srv.Config.Handler.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("sign in %d %s", rec.Code, rec.Body)
		}
		return rec.Header().Get("Set-Cookie")
	}
	plain := signIn("")
	for _, want := range []string{"yawl_session=", "HttpOnly", "SameSite=Strict", "Path=/", "Max-Age=2592000"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("%q lacks %s", plain, want)
		}
	}
	if strings.Contains(plain, "Secure") {
		t.Fatalf("Secure on plain http: %q", plain)
	}
	if !strings.Contains(signIn("https"), "Secure") {
		t.Fatal("no Secure behind https")
	}
}

func TestPasswordChangeNeedsSession(t *testing.T) {
	srv, tok, a := setupApp(t)
	c := claimed(t, srv, a)
	body := `{"old":"correct horse battery","new":"another long password"}`
	if res, _ := call(t, srv, tok, "PUT", "/api/account/password", body); res.StatusCode != 403 {
		t.Fatalf("bearer: %d", res.StatusCode)
	}
	if res, b := withCookie(t, srv, c, "PUT", "/api/account/password", body, true); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestDeployActorIsOwner(t *testing.T) {
	srv, _, a := setupApp(t)
	c := claimed(t, srv, a)
	withCookie(t, srv, c, "POST", "/api/apply", yaml, true)
	if res, b := withCookie(t, srv, c, "POST", "/api/services/acme/production/api/deploys", `{"commit":""}`, true); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var who string
	testDB.QueryRow(`SELECT actor FROM events WHERE kind = 'deploy.queued'`).Scan(&who)
	if who != "me@acme.uz" {
		t.Fatalf("actor %q", who)
	}
}

func TestPasswordChangeWrongOldIsNot401(t *testing.T) {
	srv, _, a := setupApp(t)
	c := claimed(t, srv, a)
	res, b := withCookie(t, srv, c, "PUT", "/api/account/password", `{"old":"not the password!","new":"another long password"}`, true)
	if res.StatusCode != 400 || !strings.Contains(b, "The current password is wrong.") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestLimiterSweeps(t *testing.T) {
	l := &limiter{fail: map[string][]time.Time{}}
	now := time.Unix(1_800_000_000, 0)
	for i := range limiterSweep + 1 {
		l.take(fmt.Sprintf("10.0.%d.%d", i/256, i%256), now)
	}
	l.take("1.2.3.4", now.Add(failWindow+time.Second))
	if len(l.fail) != 1 {
		t.Fatalf("%d addresses kept", len(l.fail))
	}
}
