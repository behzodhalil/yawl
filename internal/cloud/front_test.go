package cloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeCells struct {
	mu       sync.Mutex
	made     []Cell
	deleted  []string
	fail     error
	delErr   error
	images   []string
	failing  map[string]string
	slow     time.Duration
	at, peak int
}

func (f *fakeCells) Ensure(context.Context) error { return nil }
func (f *fakeCells) Create(_ context.Context, c Cell) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.made = append(f.made, c)
	return nil
}
func (f *fakeCells) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, name)
	return f.delErr
}
func (f *fakeCells) State(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.failing[name]; s != "" {
		return s, nil
	}
	return "live", nil
}
func (f *fakeCells) SetImage(_ context.Context, name, image string) error {
	f.mu.Lock()
	f.images = append(f.images, name+" "+image)
	f.at++
	f.peak = max(f.peak, f.at)
	f.mu.Unlock()
	time.Sleep(f.slow)
	f.mu.Lock()
	f.at--
	f.mu.Unlock()
	return nil
}

func fakeGitHub(t *testing.T) *httptest.Server {
	people := map[string]string{"c1": `{"id":7,"login":"behzod","email":"b@acme.uz"}`, "c2": `{"id":8,"login":"aziz","email":"a@acme.uz"}`,
		"c3": `{"id":9,"login":"nodira","email":"n@acme.uz"}`}
	tokens := map[string]string{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			r.ParseForm()
			if r.Form.Get("client_secret") != "gh-secret" || people[r.Form.Get("code")] == "" {
				json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
				return
			}
			tokens["tok-"+r.Form.Get("code")] = r.Form.Get("code")
			json.NewEncoder(w).Encode(map[string]string{"access_token": "tok-" + r.Form.Get("code")})
		case "/user":
			code := tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
			if code == "" {
				w.WriteHeader(401)
				return
			}
			io.WriteString(w, people[code])
		}
	}))
	t.Cleanup(s.Close)
	return s
}

type frontWorld struct {
	srv   *httptest.Server
	cells *fakeCells
	data  string
	key   []byte
}

func (f *frontWorld) sig(account int64) string { return (&front{key: f.key}).accountSig(account) }

func (f *frontWorld) st(t *testing.T) *frontDB {
	s, err := openStore(f.data + "/cloud.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	return s
}

func newFront(t *testing.T, edit func(*Config)) *frontWorld {
	t.Helper()
	gh := fakeGitHub(t)
	cells := &fakeCells{}
	cfg := Config{PublicURL: "https://cloud.test", Data: t.TempDir(), CellHost: "panel-{name}.198-51-100-20.sslip.io",
		Image: "registry.test/yawl:0.1.0", InstallURL: "https://cloud.test/install.sh", Releases: "https://cloud.test/releases",
		GitHubID: "gh-id", GitHubSecret: "gh-secret", GitHubWeb: gh.URL, GitHubAPI: gh.URL, AdminToken: "admin-tok", MaxCells: 50, Cells: cells}
	if edit != nil {
		edit(&cfg)
	}
	h, err := NewFront(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	key, _ := os.ReadFile(filepath.Join(cfg.Data, "session.key"))
	return &frontWorld{srv: srv, cells: cells, data: cfg.Data, key: key}
}

type person struct {
	t *testing.T
	f *frontWorld
	c *http.Client
}

func (f *frontWorld) person(t *testing.T, code string) *person {
	jar, _ := cookiejar.New(nil)
	p := &person{t, f, &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	res := p.get("/auth/github")
	loc, _ := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != 302 || loc.Query().Get("client_id") != "gh-id" || loc.Query().Get("state") == "" {
		t.Fatalf("sign-in: %d %s", res.StatusCode, loc)
	}
	if res := p.get("/auth/github/callback?code=" + code + "&state=" + loc.Query().Get("state")); res.StatusCode != 303 {
		t.Fatalf("callback: %d", res.StatusCode)
	}
	return p
}

func (p *person) get(path string) *http.Response {
	res, err := p.c.Get(p.f.srv.URL + path)
	if err != nil {
		p.t.Fatal(err)
	}
	return res
}

func (p *person) post(path string, form url.Values) (*http.Response, string) {
	req, _ := http.NewRequest("POST", p.f.srv.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://cloud.test")
	res, err := p.c.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, string(b)
}

func TestFrontSignUp(t *testing.T) {
	f := newFront(t, nil)
	p := f.person(t, "c1")
	f.cells.failing = map[string]string{"acme": "building"}
	res, _ := p.post("/new", url.Values{"name": {"acme"}, "terms": {"on"}})
	if res.StatusCode != 303 || res.Header.Get("Location") != "/me" {
		t.Fatalf("%d %s", res.StatusCode, res.Header.Get("Location"))
	}
	starting, _ := io.ReadAll(p.get("/me").Body)
	if !strings.Contains(string(starting), "Your panel is starting.") || strings.Contains(string(starting), "/claim#") {
		t.Fatalf("%s", starting)
	}
	f.cells.failing = nil
	live, _ := io.ReadAll(p.get("/me").Body)
	_, after, ok := strings.Cut(string(live), `href="https://panel-acme.198-51-100-20.sslip.io/claim#`)
	tok, _, _ := strings.Cut(after, `"`)
	if !ok || len(tok) < 32 {
		t.Fatalf("%s", live)
	}
	sum := sha256.Sum256([]byte(tok))
	c := f.cells.made[0]
	if c.Name != "acme" || c.ClaimHash != hex.EncodeToString(sum[:]) || c.PublicURL != "https://panel-acme.198-51-100-20.sslip.io" || c.Domain != "" ||
		c.Image != "registry.test/yawl:0.1.0" || c.InstallURL != "https://cloud.test/install.sh" || c.Releases != "https://cloud.test/releases" {
		t.Fatalf("%+v", c)
	}
	if res, _ := p.post("/new", url.Values{"name": {"other"}, "terms": {"on"}}); res.Header.Get("Location") != "/me" || len(f.cells.made) != 1 {
		t.Fatalf("a second cell for one account: %s %d", res.Header.Get("Location"), len(f.cells.made))
	}
	me := p.get("/me")
	body, _ := io.ReadAll(me.Body)
	if !strings.Contains(string(body), "https://panel-acme.198-51-100-20.sslip.io") || !strings.Contains(string(body), "Available</span>") {
		t.Fatalf("%s", body)
	}
	q := f.person(t, "c2")
	if _, b := q.post("/new", url.Values{"name": {"acme"}, "terms": {"on"}}); !strings.Contains(b, "acme is taken.") {
		t.Fatalf("%s", b)
	}
	for name, want := range map[string]string{"ab": "A name is 3 to 30 lowercase letters, digits and dashes, starting with a letter.",
		"Acme": "A name is 3 to 30", "-acme": "A name is 3 to 30", "admin": "admin is reserved."} {
		if _, b := q.post("/new", url.Values{"name": {name}, "terms": {"on"}}); !strings.Contains(b, want) {
			t.Errorf("%s: %s", name, b)
		}
	}
	if _, b := q.post("/new", url.Values{"name": {"beta"}}); !strings.Contains(b, "Accept the terms to go on.") {
		t.Fatalf("%s", b)
	}
	if _, b := p.post("/me/delete", url.Values{"confirm": {"nope"}}); !strings.Contains(b, "Type acme to delete it.") {
		t.Fatalf("%s", b)
	}
	if res, _ := p.post("/me/delete", url.Values{"confirm": {"acme"}}); res.StatusCode != 303 || len(f.cells.deleted) != 1 {
		t.Fatalf("%d %v", res.StatusCode, f.cells.deleted)
	}
	if _, b := q.post("/new", url.Values{"name": {"acme"}, "terms": {"on"}}); !strings.Contains(b, "acme was deleted less than 48 hours ago; pick another name, or try again later.") {
		t.Fatalf("%s", b)
	}
	if _, b := q.post("/new", url.Values{"name": {"pr-12"}, "terms": {"on"}}); !strings.Contains(b, "pr-12 is reserved.") {
		t.Fatalf("%s", b)
	}
}

func TestMeStateBadge(t *testing.T) {
	f := newFront(t, nil)
	p := f.person(t, "c1")
	p.post("/new", url.Values{"name": {"acme"}, "terms": {"on"}})
	for state, want := range map[string]string{"live": `<span class="badge ok"><svg`, "building": `<span class="badge busy"><svg`,
		"failed": `<span class="badge bad"><svg`, "unknown": `<span class="badge off"><svg`} {
		f.cells.failing = map[string]string{"acme": state}
		if state == "live" {
			f.cells.failing = nil
		}
		me, _ := io.ReadAll(p.get("/me").Body)
		word := strings.ToUpper(state[:1]) + state[1:]
		if state == "live" {
			word = "Available"
		}
		if s := string(me); !strings.Contains(s, want) || !strings.Contains(s, "</svg>"+word+"</span>") || strings.Contains(s, "pill") {
			t.Fatalf("%s: %s", state, s)
		}
	}
}

func TestFrontLimitsAndInvites(t *testing.T) {
	f := newFront(t, func(c *Config) { c.MaxCells = 1; c.InviteOnly = true; c.Invites = []string{"yawl-first"} })
	p := f.person(t, "c1")
	if _, b := p.post("/new", url.Values{"name": {"acme"}, "terms": {"on"}}); !strings.Contains(b, "An invite code is needed to create a panel.") {
		t.Fatalf("%s", b)
	}
	if res, _ := p.post("/new", url.Values{"name": {"acme"}, "terms": {"on"}, "invite": {"yawl-first"}}); res.StatusCode != 303 {
		t.Fatalf("%d", res.StatusCode)
	}
	q := f.person(t, "c2")
	if _, b := q.post("/new", url.Values{"name": {"beta"}, "terms": {"on"}, "invite": {"yawl-first"}}); !strings.Contains(b, "Yawl Cloud is full for now.") {
		t.Fatalf("%s", b)
	}
}

func TestFrontFailuresAndForgery(t *testing.T) {
	f := newFront(t, nil)
	p := f.person(t, "c1")
	f.cells.fail = errors.New("the operator panel answered 503: busy")
	if _, b := p.post("/new", url.Values{"name": {"acme"}, "terms": {"on"}}); !strings.Contains(b, "Your panel could not be made just now") || strings.Contains(b, "503: busy") {
		t.Fatalf("%s", b)
	}
	if res := p.get("/me"); res.StatusCode != 303 || res.Header.Get("Location") != "/new" {
		t.Fatalf("/me of an unfinished cell: %d", res.StatusCode)
	}
	if res := p.get("/new"); res.StatusCode != 200 {
		t.Fatalf("/new of an unfinished cell: %d", res.StatusCode)
	}
	q := f.person(t, "c2")
	if _, b := q.post("/new", url.Values{"name": {"acme"}, "terms": {"on"}}); !strings.Contains(b, "acme is taken.") {
		t.Fatalf("%s", b)
	}
	f.cells.fail = nil
	res, _ := p.post("/new", url.Values{"name": {"other"}, "terms": {"on"}})
	if res.StatusCode != 303 || res.Header.Get("Location") != "/me" || len(f.cells.made) != 1 || !f.cells.made[0].Resume || f.cells.made[0].Name != "acme" {
		t.Fatalf("resume: %d %s %+v", res.StatusCode, res.Header.Get("Location"), f.cells.made)
	}
	req, _ := http.NewRequest("POST", f.srv.URL+"/me/delete", strings.NewReader("confirm=acme"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.test")
	if res, _ := p.c.Do(req); res.StatusCode != 403 {
		t.Fatalf("a forged form: %d", res.StatusCode)
	}
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if res, _ := anon.Get(f.srv.URL + "/me"); res.StatusCode != 303 || res.Header.Get("Location") != "/" {
		t.Fatalf("signed out: %d", res.StatusCode)
	}
	if res, _ := anon.Get(f.srv.URL + "/auth/github/callback?code=c1&state=forged"); res.StatusCode != 400 {
		t.Fatalf("a forged state: %d", res.StatusCode)
	}
}

func TestFrontDeleteGone(t *testing.T) {
	f := newFront(t, nil)
	p := f.person(t, "c1")
	p.post("/new", url.Values{"name": {"acme"}, "terms": {"on"}})
	f.cells.delErr = errors.New("the operator panel answered 500: database is locked")
	if _, b := p.post("/me/delete", url.Values{"confirm": {"acme"}}); strings.Contains(b, "database is locked") || !strings.Contains(b, "could not be deleted") {
		t.Fatalf("%s", b)
	}
	f.cells.delErr = &OperatorError{Status: 404, Text: "no such environment"}
	if res, _ := p.post("/me/delete", url.Values{"confirm": {"acme"}}); res.StatusCode != 303 || res.Header.Get("Location") != "/new" {
		t.Fatalf("gone already: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestImageTag(t *testing.T) {
	for ref, want := range map[string]string{"registry.test/yawl:0.2.0": "0.2.0", "registry.test:5000/yawl:0.2.0": "0.2.0", "registry.test:5000/yawl": "", "yawl": ""} {
		if got, _ := imageTag(ref); got != want {
			t.Errorf("%s: %q", ref, got)
		}
	}
}
