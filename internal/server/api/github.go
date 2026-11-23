package api

import (
	"cmp"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/github"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

const (
	stateTTL    = 10 * time.Minute
	maxHookBody = 25 << 20 // GitHub caps payloads at 25 MiB
	hookSlots   = 4
	hookRead    = 30 * time.Second
)

type ghHandler struct {
	a         *app.App
	v         *vault.Vault
	gh        *github.Client
	publicURL string
	hooks     chan struct{}
}

func GitHub(a *app.App, v *vault.Vault, gh *github.Client, publicURL string) http.Handler {
	return newGH(a, v, gh, publicURL).routes()
}

func newGH(a *app.App, v *vault.Vault, gh *github.Client, publicURL string) *ghHandler {
	return &ghHandler{a: a, v: v, gh: gh, publicURL: strings.TrimRight(publicURL, "/"), hooks: make(chan struct{}, hookSlots)}
}

func (h *ghHandler) routes() http.Handler {
	a := h.a
	mux := http.NewServeMux()
	base := &handler{a: a}
	inner := http.NewServeMux()
	inner.HandleFunc("POST /api/github/connect", h.connect)
	inner.HandleFunc("GET /api/github", h.status)
	mux.Handle("POST /api/github/connect", base.auth(base.roles(inner, base.audit(inner))))
	mux.Handle("GET /api/github", base.auth(base.roles(inner, inner)))
	mux.HandleFunc("GET /github/setup", h.setup)
	mux.HandleFunc("GET /github/callback", h.callback)
	mux.HandleFunc("POST /github/webhook", h.webhook)
	mux.HandleFunc("GET /github/installed", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "GitHub is connected to Yawl. You can close this tab.")
	})
	return mux
}

func (h *ghHandler) installURL(slug string) string {
	return h.gh.Web + "/apps/" + slug + "/installations/new"
}

func reachable(public string) bool {
	u, err := url.Parse(public)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Hostname() == "localhost" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip == nil || !ip.IsLoopback()
}

func (h *ghHandler) status(w http.ResponseWriter, r *http.Request) {
	a, ok, err := github.Load(r.Context(), h.a.St.Q, h.v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"connected": false})
		return
	}
	writeJSON(w, map[string]any{"connected": true, "slug": a.Slug, "install_url": h.installURL(a.Slug)})
}

func (h *ghHandler) connect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if a, ok, err := github.Load(ctx, h.a.St.Q, h.v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if ok {
		writeJSON(w, map[string]any{"connected": true, "install_url": h.installURL(a.Slug)})
		return
	}
	if !reachable(h.publicURL) {
		http.Error(w, "Set --public-url to an https address GitHub can reach first.", http.StatusConflict)
		return
	}
	b := make([]byte, 16)
	rand.Read(b)
	state := hex.EncodeToString(b)
	exp := h.a.Now().Add(stateTTL).UnixMilli()
	if err := h.a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "github.state", Value: state + ":" + strconv.FormatInt(exp, 10)}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"url": h.publicURL + "/github/setup?state=" + state})
}

func (h *ghHandler) validState(r *http.Request) (string, bool) {
	row, err := h.a.St.Q.GetSetting(r.Context(), "github.state")
	if err != nil {
		return "", false
	}
	want, expS, _ := strings.Cut(row, ":")
	exp, _ := strconv.ParseInt(expS, 10, 64)
	got := r.URL.Query().Get("state")
	return row, got != "" && got == want && h.a.Now().UnixMilli() < exp
}

func expired(w http.ResponseWriter) {
	http.Error(w, "This link has expired. Run yawl github connect again.", http.StatusForbidden)
}

const submitJS = "document.forms[0].submit()"

var submitHash = func() string { s := sha256.Sum256([]byte(submitJS)); return base64.StdEncoding.EncodeToString(s[:]) }()

var setupPage = template.Must(template.New("setup").Parse(`<!doctype html>
<meta charset="utf-8"><title>Connect GitHub to Yawl</title>
<form method="post" action="{{.Action}}">
<input type="hidden" name="manifest" value="{{.Manifest}}">
<p>GitHub will ask you to confirm a private App named {{.Name}}.</p>
<button type="submit">Create the GitHub App</button>
</form>
<script>` + submitJS + `</script>
`))

func (h *ghHandler) setup(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.validState(r); !ok {
		expired(w)
		return
	}
	b := make([]byte, 2)
	rand.Read(b)
	name := "yawl-" + hex.EncodeToString(b)
	m, err := github.Manifest(h.publicURL, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'sha256-"+submitHash+"'; form-action "+h.gh.Web+"; base-uri 'none'; frame-ancestors 'none'")
	setupPage.Execute(w, map[string]string{
		"Action": h.gh.Web + "/settings/apps/new?state=" + r.URL.Query().Get("state"), "Manifest": string(m), "Name": name})
}

func (h *ghHandler) callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	row, ok := h.validState(r)
	if !ok {
		expired(w)
		return
	}
	if n, err := h.a.St.Q.DeleteSetting(ctx, db.DeleteSettingParams{Key: "github.state", Value: row}); err != nil || n != 1 {
		expired(w)
		return
	}
	a, err := h.gh.Convert(ctx, r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "GitHub did not hand over the App: "+err.Error(), http.StatusBadGateway)
		return
	}
	if err := github.Save(ctx, h.a.St.Q, h.v, a); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, h.installURL(a.Slug), http.StatusFound)
}

func (h *ghHandler) webhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, ok, err := github.Load(ctx, h.a.St.Q, h.v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if !ok {
		http.NotFound(w, r)
		return
	}
	select {
	case h.hooks <- struct{}{}:
		defer func() { <-h.hooks }()
	default:
		http.Error(w, "busy; GitHub retries", http.StatusServiceUnavailable)
		return
	}
	http.NewResponseController(w).SetReadDeadline(time.Now().Add(hookRead))
	body, err := io.ReadAll(io.LimitReader(r.Body, maxHookBody+1))
	if err != nil {
		http.Error(w, "cannot read the body", http.StatusBadRequest)
		return
	}
	if len(body) > maxHookBody {
		http.Error(w, "payload over 25 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	if !github.Verify(a.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	switch r.Header.Get("X-GitHub-Event") {
	case "ping":
		writeJSON(w, map[string]bool{"ok": true})
		return
	case "push":
	case "pull_request":
		var p struct {
			Action      string
			Number      int64
			PullRequest struct {
				State          string
				Title          string
				Merged         bool
				MergeCommitSha string `json:"merge_commit_sha"`
				Base           struct{ Ref string }
				Head           struct {
					Ref, Sha string
					Repo     struct {
						FullName string `json:"full_name"`
					}
				}
			} `json:"pull_request"`
			Repository struct {
				FullName string `json:"full_name"`
			}
			Sender struct{ Login string }
		}
		if err := json.Unmarshal(body, &p); err != nil {
			http.Error(w, "bad JSON", http.StatusBadRequest)
			return
		}
		pr := app.PR{Action: p.Action, State: p.PullRequest.State, Number: p.Number, Title: p.PullRequest.Title, Branch: p.PullRequest.Head.Ref, Sha: p.PullRequest.Head.Sha,
			Repo: p.Repository.FullName, HeadRepo: p.PullRequest.Head.Repo.FullName, Sender: p.Sender.Login,
			Base: p.PullRequest.Base.Ref, Merged: p.PullRequest.Merged, MergeSha: p.PullRequest.MergeCommitSha}
		if err := h.a.PullRequest(ctx, pr); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		return
	default:
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var p struct {
		Ref, After string
		Deleted    bool
		Repository struct {
			FullName string `json:"full_name"`
		}
		Pusher     struct{ Name string }
		Sender     struct{ Login string }
		HeadCommit struct{ Message string } `json:"head_commit"`
		Commits    []struct{ Added, Modified, Removed []string }
	}
	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	branch, isBranch := strings.CutPrefix(p.Ref, "refs/heads/")
	if !isBranch || p.Deleted || p.After == "" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	info := app.PushInfo{Message: p.HeadCommit.Message, Partial: len(p.Commits) >= 2048} // a webhook lists at most 2048 commits; the Events API lists 20
	for _, c := range p.Commits {
		info.Files = append(append(append(info.Files, c.Added...), c.Modified...), c.Removed...)
	}
	err = h.a.RenewPreviews(ctx, p.Repository.FullName, branch, p.After, cmp.Or(p.Sender.Login, p.Pusher.Name))
	var ids []string
	if err == nil {
		ids, err = h.a.PushVia(ctx, "GitHub", r.Header.Get("X-GitHub-Delivery"), p.Repository.FullName, branch, p.After, p.Pusher.Name, info)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ids == nil {
		ids = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string][]string{"deploys": ids})
}
