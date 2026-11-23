package api

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/gitlab"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/spec"
)

func (h *handler) routesGitLab(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/gitlab", h.gitlabHosts)
	mux.HandleFunc("PUT /api/settings/gitlab/{host}", h.gitlabConnect)
	mux.HandleFunc("DELETE /api/settings/gitlab/{host}", h.gitlabForget)
	mux.HandleFunc("POST /api/settings/gitlab/{host}/test", h.gitlabTest)
	mux.HandleFunc("GET /api/settings/commit-status", h.commitStatusNote)
}

func (h *handler) commitStatusNote(w http.ResponseWriter, r *http.Request) {
	gh, _ := h.a.St.Q.GetSetting(r.Context(), "commitstatus.refused.github")
	gl, _ := h.a.St.Q.GetSetting(r.Context(), "commitstatus.refused.gitlab")
	writeJSON(w, map[string]string{"github": gh, "gitlab": gl})
}

func (h *handler) hookURL() string {
	u, err := url.Parse(h.d.PublicURL)
	if err != nil || u.Hostname() == "" || u.Hostname() == "localhost" {
		return ""
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return ""
	}
	return strings.TrimRight(h.d.PublicURL, "/") + "/gitlab/webhook"
}

type gitlabHost struct {
	Host       string `json:"host"`
	WebhookURL string `json:"webhook_url"`
	Secret     string `json:"secret,omitempty"`
}

func (h *handler) gitlabHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := gitlab.Load(r.Context(), h.a.St.Q, h.d.Vault)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := []gitlabHost{}
	for _, g := range hosts {
		out = append(out, gitlabHost{Host: g.Name, WebhookURL: h.hookURL()})
	}
	writeJSON(w, out)
}

func (h *handler) gitlabConnect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Token == "" {
		http.Error(w, "Paste an access token with read_repository (and api, for merge request notes).", http.StatusBadRequest)
		return
	}
	host := r.PathValue("host")
	secret, err := gitlab.Connect(r.Context(), h.a.St.Q, h.d.Vault, host, in.Token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, gitlabHost{Host: strings.ToLower(host), WebhookURL: h.hookURL(), Secret: secret})
}

func (h *handler) gitlabTest(w http.ResponseWriter, r *http.Request) {
	hosts, err := gitlab.Load(r.Context(), h.a.St.Q, h.d.Vault)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	host := strings.ToLower(r.PathValue("host"))
	for _, g := range hosts {
		if g.Name == host {
			if err := gitlab.Check(r.Context(), g.Name, g.Token); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	http.Error(w, host+" is not connected.", http.StatusNotFound)
}

func (h *handler) gitlabForget(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.PathValue("host"))
	if err := gitlab.Forget(r.Context(), h.a.St.Q, host); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	svcs, _ := h.a.St.Q.ListServices(r.Context())
	var using []string
	for _, s := range svcs {
		if key, ok := spec.RepoKey(s.Repo); ok && strings.HasPrefix(key, host+"/") {
			using = append(using, s.Project+"/"+s.Env+"/"+s.Name)
		}
	}
	if len(using) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, map[string]string{"note": "Forgotten. " + strings.Join(using, ", ") + " still build from " + host + "; a private repository there no longer clones."})
}

var glHooks = make(chan struct{}, hookSlots)

func GitLabHook(a *app.App, v *vault.Vault) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /gitlab/webhook", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		hosts, err := gitlab.Load(ctx, a.St.Q, v)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		host, ok := gitlab.Match(hosts, r.Header.Get("X-Gitlab-Token"))
		if !ok {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		select {
		case glHooks <- struct{}{}:
			defer func() { <-glHooks }()
		default:
			http.Error(w, "busy; GitLab retries", http.StatusServiceUnavailable)
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
		var p struct {
			Ref         string `json:"ref"`
			CheckoutSha string `json:"checkout_sha"`
			User        string `json:"user_username"`
			MRUser      struct {
				Username string `json:"username"`
			} `json:"user"`
			Project struct {
				ID   int64  `json:"id"`
				Path string `json:"path_with_namespace"`
			} `json:"project"`
			MR struct {
				IID          int64  `json:"iid"`
				Action       string `json:"action"`
				State        string `json:"state"`
				Title        string `json:"title"`
				SourceBranch string `json:"source_branch"`
				TargetBranch string `json:"target_branch"`
				MergeCommit  string `json:"merge_commit_sha"`
				SourceID     int64  `json:"source_project_id"`
				TargetID     int64  `json:"target_project_id"`
				OldRev       string `json:"oldrev"`
				LastCommit   struct {
					ID string `json:"id"`
				} `json:"last_commit"`
			} `json:"object_attributes"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			http.Error(w, "bad JSON", http.StatusBadRequest)
			return
		}
		key := host.Name + "/" + strings.ToLower(p.Project.Path)
		switch r.Header.Get("X-Gitlab-Event") {
		case "Push Hook":
			branch, isBranch := strings.CutPrefix(p.Ref, "refs/heads/")
			if !isBranch || p.CheckoutSha == "" { // a deleted branch has no checkout
				break
			}
			if err := a.RenewPreviews(ctx, key, branch, p.CheckoutSha, p.User); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if _, err := a.PushVia(ctx, "GitLab", r.Header.Get("X-Gitlab-Event-UUID"), key, branch, p.CheckoutSha, p.User, gitlabPushInfo(body)); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		case "Merge Request Hook":
			action := map[string]string{"open": "opened", "reopen": "reopened", "close": "closed", "merge": "closed", "update": "edited"}[p.MR.Action]
			if p.MR.Action == "update" && p.MR.OldRev != "" { // new commits, not a new title
				action = "synchronize"
			}
			state := "closed"
			if p.MR.State == "opened" {
				state = "open"
			}
			head := key
			if p.MR.SourceID != p.MR.TargetID {
				head = "fork"
			}
			if err := a.PullRequest(ctx, app.PR{Action: action, State: state, Number: p.MR.IID, Title: p.MR.Title, Branch: p.MR.SourceBranch,
				Sha: p.MR.LastCommit.ID, Repo: key, HeadRepo: head, Sender: p.MRUser.Username,
				Base: p.MR.TargetBranch, Merged: p.MR.Action == "merge", MergeSha: p.MR.MergeCommit}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusAccepted)
	})
	return mux
}

// gitlabPushInfo is a Push Hook's head message and changed files;
// GitLab sends at most 20 commits, total_commits_count says if it cut.
func gitlabPushInfo(body []byte) app.PushInfo {
	var p struct {
		CheckoutSha string `json:"checkout_sha"`
		Total       int    `json:"total_commits_count"`
		Commits     []struct {
			ID                       string `json:"id"`
			Message                  string `json:"message"`
			Added, Modified, Removed []string
		} `json:"commits"`
	}
	json.Unmarshal(body, &p)
	info := app.PushInfo{Partial: p.Total > len(p.Commits)}
	for _, c := range p.Commits {
		if c.ID == p.CheckoutSha {
			info.Message = c.Message
		}
		info.Files = append(append(append(info.Files, c.Added...), c.Modified...), c.Removed...)
	}
	return info
}
