package cloud

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/web"
)

type Cells interface {
	Ensure(ctx context.Context) error
	Create(ctx context.Context, c Cell) error
	Delete(ctx context.Context, name string) error
	State(ctx context.Context, name string) (string, error)
	SetImage(ctx context.Context, name, image string) error
}

type Config struct {
	PublicURL, Data, CellHost, Image, InstallURL, Releases string
	GitHubID, GitHubSecret, GitHubWeb, GitHubAPI           string
	AdminToken                                             string
	MaxCells                                               int
	InviteOnly                                             bool
	Invites                                                []string
	Cells                                                  Cells
	HTTP                                                   *http.Client
	Poll, Wait                                             time.Duration
	CellScheme                                             string
	DodoAPI, DodoKey, DodoSecret, DodoProduct, Price       string
	Servers                                                []CellServer
	DNS                                                    DNS
}

type CellServer struct {
	Name, IP string
	Cap      int
}

var (
	nameRe    = regexp.MustCompile(`^[a-z][a-z0-9-]{1,28}[a-z0-9]$`)
	previewRe = regexp.MustCompile(`^pr-\d+$`)
	reserved  = []string{"www", "api", "admin", "get", "status", "cloud", "panel", "registry", "mail", "app"}
)

//go:embed templates/*.html
var pageFS embed.FS
var pages = template.Must(template.ParseFS(pageFS, "templates/*.html"))

type front struct {
	cfg  Config
	st   *frontDB
	key  []byte
	http *http.Client
}

func NewFront(cfg Config) (http.Handler, error) {
	if _, ok := imageTag(cfg.Image); !ok {
		return nil, fmt.Errorf("the cell image %q has no version tag", cfg.Image)
	}
	if err := os.MkdirAll(cfg.Data, 0o700); err != nil {
		return nil, err
	}
	if len(cfg.Servers) == 0 {
		cfg.Servers = []CellServer{{Cap: cfg.MaxCells}}
	}
	seen := map[string]bool{}
	for i, s := range cfg.Servers {
		switch {
		case seen[s.Name]:
			return nil, fmt.Errorf("the cell server %s is listed twice", s.Name)
		case i > 0 && cfg.DNS == nil:
			return nil, errors.New("a second cell server needs YAWL_CLOUD_DNS_TOKEN and YAWL_CLOUD_DNS_ZONE, so its cells' names can point at it")
		case i > 0 && s.IP == "":
			return nil, fmt.Errorf("the cell server %s has no address for its cells' records", s.Name)
		}
		seen[s.Name] = true
	}
	st, err := openStore(filepath.Join(cfg.Data, "cloud.db"))
	if err != nil {
		return nil, err
	}
	if err := st.home(cfg.Servers[0].Name); err != nil {
		return nil, err
	}
	if err := st.addInvites(cfg.Invites); err != nil {
		return nil, err
	}
	key, err := sessionKey(filepath.Join(cfg.Data, "session.key"))
	if err != nil {
		return nil, err
	}
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	f := &front{cfg: cfg, st: st, key: key, http: hc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", f.landing)
	mux.HandleFunc("GET /terms", f.static("terms"))
	mux.HandleFunc("GET /privacy", f.static("privacy"))
	mux.HandleFunc("GET /auth/github", f.signIn)
	mux.HandleFunc("GET /auth/github/callback", f.callback)
	mux.HandleFunc("GET /new", f.signedIn(f.newPage))
	mux.HandleFunc("POST /new", f.signedIn(f.sameSite(f.create)))
	mux.HandleFunc("GET /me", f.signedIn(f.me))
	mux.HandleFunc("POST /me/delete", f.signedIn(f.sameSite(f.deleteCell)))
	mux.HandleFunc("POST /signout", f.signedIn(f.sameSite(f.signOut)))
	mux.Handle("GET /assets/", web.Handler())
	f.routesAdmin(mux)
	f.routesBilling(mux)
	return mux, nil
}

func sessionKey(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil && len(b) == 32 {
		return b, nil
	}
	b := make([]byte, 32)
	rand.Read(b)
	return b, os.WriteFile(path, b, 0o600)
}

func random() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (f *front) sign(v string) string {
	m := hmac.New(sha256.New, f.key)
	m.Write([]byte(v))
	return v + "." + hex.EncodeToString(m.Sum(nil))
}

func (f *front) cookie(w http.ResponseWriter, name, value string, age time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: int(age.Seconds()), HttpOnly: true,
		Secure: strings.HasPrefix(f.cfg.PublicURL, "https://"), SameSite: http.SameSiteLaxMode}) // Lax: GitHub's redirect back must carry it
}

func (f *front) account(r *http.Request) (int64, bool) {
	c, err := r.Cookie("yc")
	if err != nil {
		return 0, false
	}
	v, _, _ := strings.Cut(c.Value, ".")
	if !hmac.Equal([]byte(f.sign(v)), []byte(c.Value)) {
		return 0, false
	}
	id, exp, _ := strings.Cut(v, ":")
	e, _ := strconv.ParseInt(exp, 10, 64)
	n, err := strconv.ParseInt(id, 10, 64)
	return n, err == nil && time.Now().Unix() < e
}

func (f *front) signedIn(h func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := f.account(r)
		if !ok {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		h(w, r, id)
	}
}

func (f *front) sameSite(h func(http.ResponseWriter, *http.Request, int64)) func(http.ResponseWriter, *http.Request, int64) {
	own, _ := url.Parse(f.cfg.PublicURL)
	return func(w http.ResponseWriter, r *http.Request, id int64) {
		from := r.Header.Get("Origin")
		if from == "" {
			from = r.Header.Get("Referer")
		}
		u, err := url.Parse(from)
		if err != nil || u.Scheme != own.Scheme || u.Host != own.Host {
			http.Error(w, "This form was not sent from Yawl Cloud.", http.StatusForbidden)
			return
		}
		h(w, r, id)
	}
}

var titles = map[string]string{"new": "Create your panel", "me": "Your panel", "terms": "Terms", "privacy": "Privacy", "error": "Error"}

func (f *front) render(w http.ResponseWriter, r *http.Request, status int, page string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if data == nil {
		data = map[string]any{}
	}
	data["Page"], data["CSS"] = page, web.Stylesheet()
	if t, ok := titles[page]; ok {
		data["App"], data["Title"] = true, t
	}
	if id, ok := f.account(r); ok {
		data["Login"] = f.st.login(id)
	}
	if f.billingOn() {
		data["Price"] = f.cfg.Price
	}
	pages.ExecuteTemplate(w, "layout", data)
}

func (f *front) landing(w http.ResponseWriter, r *http.Request) {
	if _, in := f.account(r); in {
		http.Redirect(w, r, "/me", http.StatusSeeOther)
		return
	}
	f.render(w, r, 200, "landing", nil)
}

func (f *front) signOut(w http.ResponseWriter, r *http.Request, _ int64) {
	f.cookie(w, "yc", "", -time.Second)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (f *front) static(page string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { f.render(w, r, 200, page, nil) }
}

func (f *front) signIn(w http.ResponseWriter, r *http.Request) {
	state := random()
	f.cookie(w, "ys", state, 10*time.Minute)
	http.Redirect(w, r, f.authorizeURL(state), http.StatusFound)
}

func (f *front) callback(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("ys")
	if err != nil || r.URL.Query().Get("state") == "" || !hmac.Equal([]byte(c.Value), []byte(r.URL.Query().Get("state"))) {
		f.render(w, r, 400, "error", map[string]any{"Error": "This sign-in did not start here; try again."})
		return
	}
	f.cookie(w, "ys", "", -time.Second)
	u, err := f.githubUser(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		f.render(w, r, 502, "error", map[string]any{"Error": err.Error()})
		return
	}
	id, err := f.st.account(u.ID, u.Login, u.Email)
	if err != nil {
		f.render(w, r, 500, "error", map[string]any{"Error": err.Error()})
		return
	}
	f.cookie(w, "yc", f.sign(fmt.Sprintf("%d:%d", id, time.Now().Add(30*24*time.Hour).Unix())), 30*24*time.Hour)
	to := "/new"
	if _, has := f.st.cellOf(id); has {
		to = "/me"
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (f *front) newPage(w http.ResponseWriter, r *http.Request, id int64) {
	data := map[string]any{"InviteOnly": f.cfg.InviteOnly, "Example": f.cellURL("acme")}
	if c, has := f.st.cellOf(id); has && c.Made {
		http.Redirect(w, r, "/me", http.StatusSeeOther)
		return
	} else if has {
		data["Name"], data["Error"] = c.Name, "Your panel "+c.Name+" was not finished. Press the button to finish it."
	}
	f.render(w, r, 200, "new", data)
}

func (f *front) host(name string) string { return strings.ReplaceAll(f.cfg.CellHost, "{name}", name) }

func (f *front) cellURL(name string) string {
	scheme := f.cfg.CellScheme
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + f.host(name)
}

func (f *front) create(w http.ResponseWriter, r *http.Request, id int64) {
	name := strings.TrimSpace(r.FormValue("name"))
	again := func(msg string) {
		f.render(w, r, 400, "new", map[string]any{"InviteOnly": f.cfg.InviteOnly, "Example": f.cellURL("acme"), "Name": name, "Error": msg})
	}
	if c, has := f.st.cellOf(id); has {
		if c.Made {
			http.Redirect(w, r, "/me", http.StatusSeeOther)
			return
		}
		f.make(w, r, c.Name, c.ClaimToken, c.Server, true, again)
		return
	}
	switch {
	case !nameRe.MatchString(name):
		again("A name is 3 to 30 lowercase letters, digits and dashes, starting with a letter.")
		return
	case slices.Contains(reserved, name) || previewRe.MatchString(name):
		again(name + " is reserved.")
		return
	case r.FormValue("terms") != "on":
		again("Accept the terms to go on.")
		return
	case f.cfg.InviteOnly && r.FormValue("invite") == "":
		again("An invite code is needed to create a panel.")
		return
	}
	version, _ := imageTag(f.cfg.Image)
	token := random()
	srv, err := f.st.reserve(name, id, token, version, f.cfg.MaxCells, r.FormValue("invite"), f.cfg.InviteOnly, f.cfg.Servers)
	switch {
	case errors.Is(err, errHasCell):
		http.Redirect(w, r, "/me", http.StatusSeeOther)
		return
	case errors.Is(err, errTaken):
		again(name + " is taken.")
		return
	case errors.Is(err, errFull):
		again("Yawl Cloud is full for now.")
		return
	case errors.Is(err, errInvite):
		again("That invite code is not one of ours.")
		return
	case errors.Is(err, errUsed):
		again("That invite code was used.")
		return
	case errors.Is(err, errChurn):
		again("You deleted a panel less than 48 hours ago; you can make a new one 48 hours after that.")
		return
	case errors.Is(err, errRetired):
		again(name + " was deleted less than 48 hours ago; pick another name, or try again later.")
		return
	case err != nil:
		again(err.Error())
		return
	}
	f.make(w, r, name, token, srv, false, again)
}

func (f *front) make(w http.ResponseWriter, r *http.Request, name, token, server string, resume bool, again func(string)) {
	host := f.host(name)
	c := Cell{Name: name, Image: f.cfg.Image, PublicURL: f.cellURL(name), ClaimHash: hash(token), InstallURL: f.cfg.InstallURL, Releases: f.cfg.Releases, Resume: resume,
		BillingURL: f.cfg.PublicURL + "/cells/" + name + "/billing", BillingToken: f.cellToken(name), Server: server}
	if s, ok := f.server(server); ok {
		c.RefusedHostIP = s.IP
	}
	if !strings.HasSuffix(host, ".sslip.io") {
		c.Domain = host
	}
	err := f.pointAt(r.Context(), host, server)
	if err == nil {
		err = f.cfg.Cells.Create(r.Context(), c)
	}
	if err != nil {
		if !resume && (errors.Is(err, errNotOurs) || strings.Contains(err.Error(), name+" is taken")) {
			slog.Warn("cloud: a cell's name is taken", "name", name, "err", err)
			f.st.drop(name, true)
			again(name + " is taken.")
			return
		}
		slog.Error("cloud: make a cell", "name", name, "err", err)
		again("Your panel could not be made just now. Press the button again to finish it.")
		return
	}
	f.st.made(name)
	http.Redirect(w, r, "/me", http.StatusSeeOther)
}

func (f *front) server(name string) (CellServer, bool) {
	for _, s := range f.cfg.Servers {
		if s.Name == name {
			return s, true
		}
	}
	return CellServer{}, false
}

func (f *front) pointAt(ctx context.Context, host, server string) error {
	if s, ok := f.server(server); ok && s.Name != f.cfg.Servers[0].Name && f.cfg.DNS != nil {
		return f.cfg.DNS.SetA(ctx, host, s.IP)
	}
	return nil
}

func gone(err error) bool {
	var oe *OperatorError
	return errors.As(err, &oe) && oe.Status == http.StatusNotFound
}

func imageTag(ref string) (string, bool) {
	last := ref[strings.LastIndex(ref, "/")+1:]
	_, tag, ok := strings.Cut(last, ":")
	return tag, ok && tag != ""
}

func hash(token string) string {
	s := sha256.Sum256([]byte(token))
	return hex.EncodeToString(s[:])
}

func (f *front) me(w http.ResponseWriter, r *http.Request, id int64) {
	c, has := f.st.cellOf(id)
	if !has || !c.Made {
		http.Redirect(w, r, "/new", http.StatusSeeOther)
		return
	}
	state, err := f.cfg.Cells.State(r.Context(), c.Name)
	if err != nil {
		slog.Error("cloud: a cell's state", "name", c.Name, "err", err)
		state = "unknown"
	}
	url := f.cellURL(c.Name)
	open := url
	if state != "live" {
		open = ""
	} else if c.ClaimToken != "" && !f.claimed(r.Context(), url) {
		open = url + "/claim#" + c.ClaimToken
	}
	kind, word := badge(state)
	f.render(w, r, 200, "me", map[string]any{"Name": c.Name, "URL": url, "Open": template.URL(open), "Badge": map[string]string{"Kind": kind, "Word": word},
		"Version": c.Version, "Error": r.URL.Query().Get("e"), "Billing": f.billingView(id)})
}

func badge(state string) (kind, word string) {
	switch state {
	case "":
		return "busy", "Starting"
	case "live":
		return "ok", "Available"
	case "failed", "rolled_back":
		kind = "bad"
	case "unknown", "superseded":
		kind = "off"
	default:
		kind = "busy"
	}
	return kind, strings.ToUpper(state[:1]) + strings.ReplaceAll(state[1:], "_", " ")
}

func (f *front) claimed(ctx context.Context, cellURL string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", cellURL+"/api/session", nil)
	var s struct{ Claimed bool }
	return f.json(req, &s) == nil && s.Claimed
}

func (f *front) deleteCell(w http.ResponseWriter, r *http.Request, id int64) {
	c, has := f.st.cellOf(id)
	if !has {
		http.Redirect(w, r, "/new", http.StatusSeeOther)
		return
	}
	if r.FormValue("confirm") != c.Name {
		f.render(w, r, 400, "me", map[string]any{"Name": c.Name, "URL": f.cellURL(c.Name), "Open": template.URL(f.cellURL(c.Name)),
			"Version": c.Version, "Error": "Type " + c.Name + " to delete it."})
		return
	}
	if err := f.cfg.Cells.Delete(r.Context(), c.Name); err != nil && !gone(err) {
		slog.Error("cloud: delete a cell", "name", c.Name, "err", err)
		f.render(w, r, 502, "error", map[string]any{"Error": "Your panel could not be deleted just now; try again in a minute."})
		return
	}
	if s, ok := f.server(c.Server); ok && s.Name != f.cfg.Servers[0].Name && f.cfg.DNS != nil {
		if err := f.cfg.DNS.Remove(r.Context(), f.host(c.Name), s.IP); err != nil {
			slog.Error("cloud: remove a cell's record", "name", c.Name, "err", err)
		}
	}
	if err := f.st.retire(c.Name, c.Server, id, 48*time.Hour); err != nil {
		slog.Error("cloud: retire a cell", "name", c.Name, "err", err)
		f.render(w, r, 502, "error", map[string]any{"Error": "Your panel could not be deleted just now; try again in a minute."})
		return
	}
	f.st.drop(c.Name, false)
	http.Redirect(w, r, "/new", http.StatusSeeOther)
}
