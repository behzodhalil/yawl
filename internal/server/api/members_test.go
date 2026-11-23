package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/app"
)

func TestMembersRoutes(t *testing.T) {
	srv, tok := setup(t)
	adminOwner(t)
	res, b := call(t, srv, tok, "POST", "/api/invites", `{"role":"developer"}`)
	var inv struct{ Token, ID string }
	json.Unmarshal(b, &inv)
	if res.StatusCode != 200 || inv.Token == "" {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	accept := `{"token":"` + inv.Token + `","email":"Dev@acme.uz","password":"correct horse battery"}`
	r1, err := http.Post(srv.URL+"/api/invites/accept", "application/json", strings.NewReader(accept))
	if err != nil || r1.StatusCode != 200 {
		t.Fatal(r1.StatusCode, err)
	}
	cookie := r1.Header.Get("Set-Cookie")
	if r2, _ := http.Post(srv.URL+"/api/invites/accept", "application/json", strings.NewReader(accept)); r2.StatusCode != 400 {
		t.Fatalf("second use: %d", r2.StatusCode)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/me", nil)
	req.Header.Set("Cookie", strings.Split(cookie, ";")[0])
	me, _ := http.DefaultClient.Do(req)
	var who struct{ Email, Role string }
	json.NewDecoder(me.Body).Decode(&who)
	if who.Email != "dev@acme.uz" || who.Role != "developer" {
		t.Fatalf("%+v", who)
	}
	_, b = call(t, srv, tok, "GET", "/api/members", "")
	m := regexp.MustCompile(`"id":"(own_[^"]+)","email":"dev@acme.uz","role":"developer"`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("%s", b)
	}
	devTok := memberTokenFor(t, m[1])
	if res, b := call(t, srv, devTok, "POST", "/api/invites", `{"role":"admin"}`); res.StatusCode != 403 || strings.TrimSpace(string(b)) != "Developers cannot manage members." {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, tok, "PUT", "/api/members/"+m[1], `{"role":"viewer"}`); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	if res, _ := call(t, srv, devTok, "GET", "/api/projects", ""); res.StatusCode != 401 {
		t.Fatalf("a token outlived the role change: %d", res.StatusCode)
	}
	if res, _ := call(t, srv, tok, "DELETE", "/api/members/"+m[1], ""); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
}

func memberTokenFor(t *testing.T, id string) string {
	tok := "yawl_member_" + id
	if _, err := testDB.Exec(`INSERT INTO api_tokens (id, name, hash, owner_id, created_at, updated_at) VALUES (?, 'ci', ?, ?, 1, 1)`, "tok_"+id, app.HashToken(tok), id); err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestCLILoginTokenIsTheApprovers(t *testing.T) {
	srv, _, a := setupApp(t)
	claimed(t, srv, a)
	adm, _ := a.St.Q.FirstAdmin(t.Context())
	link, _, _ := a.Invite(t.Context(), "viewer", adm.ID)
	id, err := a.AcceptInvite(t.Context(), link, "view@acme.uz", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	viewer, _ := a.NewSession(t.Context(), id)
	call(t, srv, "", "POST", "/api/cli-login", `{"code":"ABCDEFGHJK","host":"laptop"}`)
	check := approve(t, srv, &http.Cookie{Name: cookieName, Value: viewer}, "ABCDEFGHJK")
	_, b := call(t, srv, "", "GET", "/api/cli-login/ABCDEFGHJK?check="+check, "")
	var out struct{ Token string }
	json.Unmarshal(b, &out)
	if res, b := call(t, srv, out.Token, "POST", "/api/invites", `{"role":"admin"}`); res.StatusCode != 403 || strings.TrimSpace(string(b)) != "Viewers cannot change anything." {
		t.Fatalf("a Viewer's CLI token: %d %s", res.StatusCode, b)
	}
}

func TestAcceptInviteLimited(t *testing.T) {
	srv, _ := setup(t)
	last := 0
	for i := 0; i < 12; i++ {
		res, _ := http.Post(srv.URL+"/api/invites/accept", "application/json", strings.NewReader(`{"token":"nope","email":"x@acme.uz","password":"correct horse battery"}`))
		last = res.StatusCode
	}
	if last != 429 {
		t.Fatalf("after 12 bad links: %d", last)
	}
}

func TestPasswordResetRoute(t *testing.T) {
	srv, _, a := setupApp(t)
	cookie := claimed(t, srv, a)
	link, err := a.PasswordResetLink(t.Context(), "me@acme.uz")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"token":"` + link + `","password":"a new long password"}`
	if res, b := call(t, srv, "", "POST", "/api/password-reset", body); res.StatusCode != 200 || !strings.Contains(string(b), `"email":"me@acme.uz"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, "", "POST", "/api/password-reset", body); res.StatusCode != 400 || strings.TrimSpace(string(b)) != app.ErrResetLink.Error() {
		t.Fatalf("second use: %d %s", res.StatusCode, b)
	}
	if res, _ := withCookie(t, srv, cookie, "GET", "/api/me", "", false); res.StatusCode != 401 {
		t.Fatalf("the old session survived: %d", res.StatusCode)
	}
	last := 0
	for i := 0; i < 12; i++ {
		res, _ := call(t, srv, "", "POST", "/api/password-reset", `{"token":"nope","password":"a new long password"}`)
		last = res.StatusCode
	}
	if last != 429 {
		t.Fatalf("after 12 bad links: %d", last)
	}
}

func TestLastAdminRefusedWith409(t *testing.T) {
	srv, _, a := setupApp(t)
	cookie := claimed(t, srv, a)
	adm, _ := a.St.Q.FirstAdmin(t.Context())
	for _, c := range []struct{ method, body string }{{"PUT", `{"role":"developer"}`}, {"DELETE", ""}} {
		res, b := withCookie(t, srv, cookie, c.method, "/api/members/"+adm.ID, c.body, true)
		if res.StatusCode != 409 || strings.TrimSpace(string(b)) != app.ErrLastAdmin.Error() {
			t.Fatalf("%s: %d %s", c.method, res.StatusCode, b)
		}
	}
}
