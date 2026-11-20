package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestCliLoginFlow(t *testing.T) {
	srv, _, a := setupApp(t)
	c := claimed(t, srv, a)
	if res, b := call(t, srv, "", "POST", "/api/cli-login", `{"code":"ABCDEFGHJK","host":"laptop"}`); res.StatusCode != 201 {
		t.Fatalf("register %d %s", res.StatusCode, b)
	}
	res, b := call(t, srv, "", "GET", "/api/cli-login/ABCDEFGHJK", "")
	if res.StatusCode != 202 || !strings.Contains(string(b), `"host":"laptop"`) {
		t.Fatalf("pending %d %s", res.StatusCode, b)
	}
	check := approve(t, srv, c, "ABCDEFGHJK")
	res, b = call(t, srv, "", "GET", "/api/cli-login/ABCDEFGHJK?check="+strings.ToLower(check), "")
	var out struct{ Token string }
	json.Unmarshal(b, &out)
	if res.StatusCode != 200 || !strings.HasPrefix(out.Token, "yawl_") {
		t.Fatalf("token %d %s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, "", "GET", "/api/cli-login/ABCDEFGHJK", ""); res.StatusCode != 404 {
		t.Fatal("the token was handed out twice")
	}
	if res, _ := call(t, srv, out.Token, "GET", "/api/status", ""); res.StatusCode != 200 {
		t.Fatal("the token does not work")
	}
	var name string
	testDB.QueryRow(`SELECT name FROM api_tokens ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&name)
	if name != "CLI on laptop" {
		t.Fatalf("name %q", name)
	}
	if res, _ := call(t, srv, "", "POST", "/api/cli-login", `{"code":"bad code","host":"x"}`); res.StatusCode != 400 {
		t.Fatal("a malformed code was accepted")
	}
}

func TestCliLoginExpires(t *testing.T) {
	srv, _, a := setupApp(t)
	c := claimed(t, srv, a)
	call(t, srv, "", "POST", "/api/cli-login", `{"code":"ABCDEFGHJK","host":"laptop"}`)
	a.Now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	if res, _ := withCookie(t, srv, c, "POST", "/api/cli-login/ABCDEFGHJK/approve", "", true); res.StatusCode != 410 {
		t.Fatalf("approve after expiry: %d", res.StatusCode)
	}
}

func TestCliLoginApproveNeedsSession(t *testing.T) {
	srv, _, _ := setupApp(t)
	call(t, srv, "", "POST", "/api/cli-login", `{"code":"ABCDEFGHJK","host":"laptop"}`)
	if res, _ := call(t, srv, "", "POST", "/api/cli-login/ABCDEFGHJK/approve", ""); res.StatusCode != 401 {
		t.Fatalf("%d", res.StatusCode)
	}
}

func TestCliLoginPendingLimit(t *testing.T) {
	srv, _, _ := setupApp(t)
	for i := range 20 {
		call(t, srv, "", "POST", "/api/cli-login", fmt.Sprintf(`{"code":"ABCDEFGH%c%c","host":"x"}`, 'A'+i/8, 'A'+i%8))
	}
	if res, _ := call(t, srv, "", "POST", "/api/cli-login", `{"code":"ZZZZZZZZZZ","host":"x"}`); res.StatusCode != 429 {
		t.Fatalf("%d", res.StatusCode)
	}
	_ = context.Background()
}

func register(t *testing.T, srv *httptest.Server, code, from string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+"/api/cli-login", strings.NewReader(`{"code":"`+code+`","host":"laptop"}`))
	req.Header.Set("X-Forwarded-For", from)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := readAll(res)
	return res.StatusCode, strings.TrimSpace(string(b))
}

func TestCliLoginPerAddress(t *testing.T) {
	srv, _, _ := setupApp(t)
	for _, code := range []string{"AAAAAAAAAA", "AAAAAAAAAB", "AAAAAAAAAC"} {
		if st, b := register(t, srv, code, "203.0.113.9"); st != 201 {
			t.Fatalf("%s: %d %s", code, st, b)
		}
	}
	if st, b := register(t, srv, "AAAAAAAAAD", "203.0.113.9"); st != 429 || b != "Too many sign-in codes are waiting from this address. Wait 10 minutes or use --token." {
		t.Fatalf("fourth: %d %s", st, b)
	}
	if st, b := register(t, srv, "AAAAAAAAAE", "198.51.100.7"); st != 201 {
		t.Fatalf("another address: %d %s", st, b)
	}
	for i, code := range []string{"AAAAAAAAAF", "AAAAAAAAAG", "AAAAAAAAAH", "AAAAAAAAAI"} {
		want := 201
		if i == 3 {
			want = 429
		}
		if st, b := register(t, srv, code, fmt.Sprintf("2001:db8:1:2::%d", i+1)); st != want {
			t.Fatalf("v6 %d: %d %s", i, st, b)
		}
	}
	if st, b := register(t, srv, "AAAAAAAAAJ", "2001:db8:1:3::1"); st != 201 {
		t.Fatalf("another /64: %d %s", st, b)
	}
	res, b := call(t, srv, "", "GET", "/api/cli-login/AAAAAAAAAA", "")
	var out struct {
		Host, From string
		At         int64
	}
	json.Unmarshal(b, &out)
	if res.StatusCode != 202 || out.Host != "laptop" || out.From != "203.0.113.9" || out.At == 0 {
		t.Fatalf("pending view %d %s", res.StatusCode, b)
	}
}

func TestCliApproveOnce(t *testing.T) {
	srv, _, a := setupApp(t)
	c := claimed(t, srv, a)
	tokens := func() (n int) {
		testDB.QueryRow(`SELECT COUNT(*) FROM api_tokens`).Scan(&n)
		return
	}
	register(t, srv, "ABCDEFGHJK", "203.0.113.9")
	approve(t, srv, c, "ABCDEFGHJK")
	before := tokens()
	res, b := withCookie(t, srv, c, "POST", "/api/cli-login/ABCDEFGHJK/approve", "", true)
	if res.StatusCode != 410 || strings.TrimSpace(string(b)) != "This code has expired or was already used." || tokens() != before {
		t.Fatalf("second approve %d %s, tokens %d -> %d", res.StatusCode, b, before, tokens())
	}
	register(t, srv, "ABCDEFGHJM", "203.0.113.9")
	a.Now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	res, b = withCookie(t, srv, c, "POST", "/api/cli-login/ABCDEFGHJM/approve", "", true)
	if res.StatusCode != 410 || strings.TrimSpace(string(b)) != "This code has expired or was already used." || tokens() != before {
		t.Fatalf("late approve %d %s", res.StatusCode, b)
	}
}

func TestCliLoginInfoNeverHandsToken(t *testing.T) {
	srv, _, a := setupApp(t)
	c := claimed(t, srv, a)
	register(t, srv, "ABCDEFGHJK", "203.0.113.9")
	if res, _ := call(t, srv, "", "GET", "/api/cli-login/ABCDEFGHJK/info", ""); res.StatusCode != 401 {
		t.Fatalf("info without a session: %d", res.StatusCode)
	}
	check := approve(t, srv, c, "ABCDEFGHJK")
	res, body := withCookie(t, srv, c, "GET", "/api/cli-login/ABCDEFGHJK/info", "", false)
	var info map[string]any
	json.Unmarshal([]byte(body), &info)
	if res.StatusCode != 200 || info["host"] != "laptop" || info["from"] != "203.0.113.9" || info["approved"] != true || info["token"] != nil || info["check"] != nil {
		t.Fatalf("info %d %s", res.StatusCode, body)
	}
	res, b := call(t, srv, "", "GET", "/api/cli-login/ABCDEFGHJK?check="+check, "")
	var out struct{ Token string }
	json.Unmarshal(b, &out)
	if res.StatusCode != 200 || !strings.HasPrefix(out.Token, "yawl_") {
		t.Fatalf("the CLI lost its token: %d %s", res.StatusCode, b)
	}
}

func TestCliApproveAuditKeepsNoCode(t *testing.T) {
	srv, _, a := setupApp(t)
	c := claimed(t, srv, a)
	call(t, srv, "", "POST", "/api/cli-login", `{"code":"ABCDEFGHJK","host":"laptop"}`)
	check := approve(t, srv, c, "ABCDEFGHJK")
	var n int
	testDB.QueryRow(`SELECT count(*) FROM events WHERE body_json LIKE '%ABCDEFGHJK%' OR subject_id LIKE '%ABCDEFGHJK%' OR body_json LIKE '%'||?||'%'`, check).Scan(&n)
	if got := audits(t); n != 0 || len(got) == 0 || !strings.HasSuffix(got[len(got)-1], ": approved a CLI sign-in") {
		t.Fatalf("%d rows carry the code; %q", n, got)
	}
}

func approve(t *testing.T, srv *httptest.Server, c *http.Cookie, code string) string {
	t.Helper()
	res, b := withCookie(t, srv, c, "POST", "/api/cli-login/"+code+"/approve", "", true)
	var out struct{ Check string }
	json.Unmarshal([]byte(b), &out)
	if res.StatusCode != 200 || len(out.Check) != 8 {
		t.Fatalf("approve %d %s", res.StatusCode, b)
	}
	return out.Check
}

func TestCliLoginLinkAloneGetsNoToken(t *testing.T) {
	srv, _, a := setupApp(t)
	c := claimed(t, srv, a)
	if res, b := call(t, srv, "", "POST", "/api/cli-login", `{"code":"QWERTYUIOP","host":"me@acme.uz MacBook"}`); res.StatusCode != 201 {
		t.Fatalf("attacker register %d %s", res.StatusCode, b)
	}
	check := approve(t, srv, c, "QWERTYUIOP")
	tokens := func() (n int) {
		testDB.QueryRow(`SELECT COUNT(*) FROM api_tokens`).Scan(&n)
		return
	}
	before := tokens()
	for _, q := range []string{"", "?check=AAAAAAAA"} {
		if res, b := call(t, srv, "", "GET", "/api/cli-login/QWERTYUIOP"+q, ""); res.StatusCode == 200 || strings.Contains(string(b), "yawl_") || tokens() != before {
			t.Fatalf("poll %q: %d %s", q, res.StatusCode, b)
		}
	}
	if res, b := call(t, srv, "", "GET", "/api/cli-login/QWERTYUIOP?check="+check, ""); res.StatusCode != 404 {
		t.Fatalf("the right code after a wrong one: %d %s", res.StatusCode, b)
	}
}

func TestCliLoginCellGatewayKeysByClient(t *testing.T) {
	_, _, a := setupApp(t)
	h := &handler{a: a, cli: &cliLogins{m: map[string]*cliLogin{}}}
	setTrustedProxy(netip.MustParseAddr("172.18.0.1"))
	defer setTrustedProxy(netip.Addr{})
	reg := func(code, peer, xff string) int {
		r := httptest.NewRequest("POST", "/api/cli-login", strings.NewReader(`{"code":"`+code+`","host":"x"}`))
		r.RemoteAddr = peer + ":5555"
		r.Header.Set("X-Forwarded-For", xff)
		w := httptest.NewRecorder()
		h.cliRegister(w, r)
		return w.Code
	}
	for i := 1; i <= 4; i++ {
		if c := reg(fmt.Sprintf("AAAAAAAAA%c", 'A'+i), "172.18.0.1", fmt.Sprintf("203.0.113.%d", i)); c != 201 {
			t.Fatalf("client %d: %d", i, c)
		}
	}
	if from := h.cli.m["AAAAAAAAAE"].from; from != "203.0.113.4" {
		t.Fatalf("from %q", from)
	}
	for i := 0; i < 3; i++ {
		reg(fmt.Sprintf("BBBBBBBBB%c", 'A'+i), "172.18.0.1", "198.51.100.7")
	}
	if c := reg("BBBBBBBBBZ", "172.18.0.1", "198.51.100.7"); c != 429 {
		t.Fatalf("a fourth code from one client: %d", c)
	}
	if reg("CCCCCCCCCC", "172.18.0.5", "203.0.113.9"); h.cli.m["CCCCCCCCCC"].from != "172.18.0.5" {
		t.Fatalf("a neighbour chose its from: %q", h.cli.m["CCCCCCCCCC"].from)
	}
}
