package api

import (
	"cmp"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/gitea"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

func (h *handler) routesGitea(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/gitea", h.giteaHosts)
	mux.HandleFunc("PUT /api/settings/gitea/{host}", h.giteaConnect)
	mux.HandleFunc("DELETE /api/settings/gitea/{host}", h.giteaForget)
	mux.HandleFunc("POST /api/settings/gitea/{host}/test", h.giteaTest)
}

func (h *handler) giteaHookURL() string {
	return strings.TrimRight(h.d.PublicURL, "/") + "/gitea/webhook"
}

func (h *handler) giteaHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := gitea.Load(r.Context(), h.a.St.Q, h.d.Vault)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := []gitlabHost{}
	for _, g := range hosts {
		out = append(out, gitlabHost{Host: g.Name, WebhookURL: h.giteaHookURL()})
	}
	writeJSON(w, out)
}

func (h *handler) giteaConnect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Token == "" {
		http.Error(w, "Paste an access token that may read repositories (and write issues and commit statuses).", http.StatusBadRequest)
		return
	}
	host := r.PathValue("host")
	secret, err := gitea.Connect(r.Context(), h.a.St.Q, h.d.Vault, host, in.Token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, gitlabHost{Host: strings.ToLower(host), WebhookURL: h.giteaHookURL(), Secret: secret})
}

func (h *handler) giteaTest(w http.ResponseWriter, r *http.Request) {
	hosts, err := gitea.Load(r.Context(), h.a.St.Q, h.d.Vault)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	host := strings.ToLower(r.PathValue("host"))
	for _, g := range hosts {
		if g.Name == host {
			if err := gitea.Check(r.Context(), g.Name, g.Token); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	http.Error(w, host+" is not connected.", http.StatusNotFound)
}

func (h *handler) giteaForget(w http.ResponseWriter, r *http.Request) {
	if err := gitea.Forget(r.Context(), h.a.St.Q, strings.ToLower(r.PathValue("host"))); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func GiteaHook(a *app.App, v *vault.Vault) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /gitea/webhook", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		http.NewResponseController(w).SetReadDeadline(time.Now().Add(hookRead))
		body, err := io.ReadAll(io.LimitReader(r.Body, maxHookBody+1))
		if err != nil || len(body) > maxHookBody {
			http.Error(w, "cannot read the body", http.StatusBadRequest)
			return
		}
		var p struct {
			Ref        string                           `json:"ref"`
			After      string                           `json:"after"`
			Action     string                           `json:"action"`
			Number     int64                            `json:"number"`
			Pusher     struct{ Login, Username string } `json:"pusher"`
			Sender     struct{ Login string }           `json:"sender"`
			Repository struct {
				FullName string `json:"full_name"`
				HTMLURL  string `json:"html_url"`
			} `json:"repository"`
			PR struct {
				Title          string `json:"title"`
				State          string `json:"state"`
				Merged         bool   `json:"merged"`
				MergeCommitSha string `json:"merge_commit_sha"`
				Base           struct {
					Ref string `json:"ref"`
				} `json:"base"`
				Head struct {
					Ref  string `json:"ref"`
					Sha  string `json:"sha"`
					Repo struct {
						FullName string `json:"full_name"`
					} `json:"repo"`
				} `json:"head"`
			} `json:"pull_request"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			http.Error(w, "bad JSON", http.StatusBadRequest)
			return
		}
		u, err := url.Parse(p.Repository.HTMLURL)
		if err != nil {
			http.Error(w, "bad repository", http.StatusBadRequest)
			return
		}
		hosts, err := gitea.Load(ctx, a.St.Q, v)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sig := r.Header.Get("X-Gitea-Signature")
		if sig == "" {
			sig = r.Header.Get("X-Forgejo-Signature")
		}
		host, ok := gitea.Verify(hosts, strings.ToLower(u.Host), body, sig)
		if !ok {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		key := host.Name + "/" + strings.ToLower(p.Repository.FullName)
		event := r.Header.Get("X-Gitea-Event")
		if event == "" {
			event = r.Header.Get("X-Forgejo-Event")
		}
		delivery := r.Header.Get("X-Gitea-Delivery")
		if delivery == "" {
			delivery = r.Header.Get("X-Forgejo-Delivery")
		}
		switch event {
		case "push":
			branch, isBranch := strings.CutPrefix(p.Ref, "refs/heads/")
			if !isBranch || p.After == "" || strings.Trim(p.After, "0") == "" { // a deleted branch
				break
			}
			who := p.Pusher.Login
			if who == "" {
				who = p.Pusher.Username
			}
			if err := a.RenewPreviews(ctx, key, branch, p.After, cmp.Or(p.Sender.Login, who)); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if _, err := a.PushVia(ctx, "Gitea", delivery, key, branch, p.After, who, giteaPushInfo(body)); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		case "pull_request":
			action := map[string]string{"opened": "opened", "reopened": "reopened", "closed": "closed", "synchronized": "synchronize", "edited": "edited"}[p.Action]
			head := key
			if !strings.EqualFold(p.PR.Head.Repo.FullName, p.Repository.FullName) {
				head = "fork"
			}
			if err := a.PullRequest(ctx, app.PR{Action: action, State: p.PR.State, Number: p.Number, Title: p.PR.Title, Branch: p.PR.Head.Ref,
				Sha: p.PR.Head.Sha, Repo: key, HeadRepo: head, Sender: p.Sender.Login,
				Base: p.PR.Base.Ref, Merged: p.PR.Merged, MergeSha: p.PR.MergeCommitSha}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusAccepted)
	})
	return mux
}

// giteaPushInfo is the push's files from every commit listed; Gitea lists a
// few of them (total_commits says how many), and then it is partial (Plan 58 review I3).
func giteaPushInfo(body []byte) app.PushInfo {
	var p struct {
		Total      int `json:"total_commits"`
		HeadCommit struct {
			Message string
		} `json:"head_commit"`
		Commits []struct {
			Added, Modified, Removed []string
		} `json:"commits"`
	}
	json.Unmarshal(body, &p)
	info := app.PushInfo{Message: p.HeadCommit.Message, Partial: p.Total > len(p.Commits)}
	for _, c := range p.Commits {
		info.Files = append(append(append(info.Files, c.Added...), c.Modified...), c.Removed...)
	}
	return info
}
