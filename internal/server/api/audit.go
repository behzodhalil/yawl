package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

var sentences = map[string]string{
	"POST /api/apply": "applied yawl.yaml",
	"PUT /api/projects/{project}/preview-senders":                 "changed who may build previews of {project}",
	"POST /api/environments/{project}/{env}/templates/{id}":       "installed the {id} template in {project}/{env}",
	"POST /api/services/{project}/{env}/{name}/deploys":           "deployed {project}/{env}/{name}",
	"POST /api/services/{project}/{env}/{name}/rollback":          "rolled back {project}/{env}/{name}",
	"POST /api/services/{project}/{env}/{name}/restart":           "restarted {project}/{env}/{name}",
	"POST /api/services/{project}/{env}/{name}/stop":              "stopped {project}/{env}/{name}",
	"POST /api/services/{project}/{env}/{name}/start":             "started {project}/{env}/{name}",
	"PUT /api/services/{project}/{env}/{name}/log-alerts":         "changed the log alerts of {project}/{env}/{name}",
	"PUT /api/services/{project}/{env}/{name}/password":           "changed the password of {project}/{env}/{name}",
	"PUT /api/services/{project}/{env}/{name}/maintenance":        "turned maintenance on or off for {project}/{env}/{name}",
	"POST /api/deploys/{id}/cancel":                               "canceled deploy {id}",
	"POST /api/services/{project}/{env}/{name}/backups":           "backed up {project}/{env}/{name}",
	"POST /api/services/{project}/{env}/{name}/restore":           "restored a backup of {project}/{env}/{name}",
	"PUT /api/services/{project}/{env}/{name}/import":             "imported a dump into {project}/{env}/{name}",
	"POST /api/services/{project}/{env}/{name}/import":            "imported a dump into {project}/{env}/{name}",
	"POST /api/services/{project}/{env}/{name}/runs":              "ran {project}/{env}/{name} now",
	"PUT /api/services/{project}/{env}/{name}/env/{key}":          "set variable {key} on {project}/{env}/{name}",
	"DELETE /api/services/{project}/{env}/{name}/env/{key}":       "removed variable {key} from {project}/{env}/{name}",
	"PUT /api/environments/{project}/{env}/env/{key}":             "set shared variable {key} on {project}/{env}",
	"PUT /api/shared-env/team/{key}":                              "set team variable {key}",
	"PUT /api/shared-env/servers/{server}/{key}":                  "set server variable {key} on {server}",
	"DELETE /api/shared-env/team/{key}":                           "deleted team variable {key}",
	"DELETE /api/shared-env/servers/{server}/{key}":               "deleted server variable {key} on {server}",
	"DELETE /api/environments/{project}/{env}/env/{key}":          "removed shared variable {key} from {project}/{env}",
	"POST /api/services/{project}/{env}/{name}/env/{key}/reveal":  "revealed {key} of {project}/{env}/{name}",
	"POST /api/projects/{project}/compare":                        "compared the environments of {project}",
	"POST /api/environments/{project}/{env}/move":                 "started moving {project}/{env}",
	"POST /api/moves/{id}/retry":                                  "retried a move",
	"POST /api/environments/{project}/{env}/services":             "saved a service in {project}/{env}",
	"POST /api/environments":                                      "created an environment",
	"POST /api/previews/{id}/close":                               "closed a preview",
	"POST /api/invites":                                           "invited a member",
	"DELETE /api/invites/{id}":                                    "revoked an invite",
	"PUT /api/members/{id}":                                       "changed a member's role",
	"DELETE /api/members/{id}":                                    "removed a member",
	"DELETE /api/members/{id}/two-step":                           "turned off two-step sign in for a member",
	"POST /api/account/two-step":                                  "started turning on two-step sign in",
	"PUT /api/account/two-step":                                   "turned on two-step sign in",
	"DELETE /api/account/two-step":                                "turned off two-step sign in",
	"POST /api/account/two-step/codes":                            "made new recovery codes",
	"PUT /api/settings/two-step":                                  "turned {body:required} required two-step sign in",
	"PUT /api/servers/{server}/firewall":                          "changed the firewall of {server}",
	"PUT /api/settings/firewall":                                  "changed whether SSH bans go to Telegram",
	"PUT /api/settings/notifications/{channel}":                   "set up {channel} for alerts",
	"DELETE /api/settings/notifications/{channel}":                "removed {channel} from alerts",
	"POST /api/servers/{server}/firewall/keep":                    "kept the firewall rules of {server}",
	"POST /api/servers/{server}/firewall/unban":                   "lifted an SSH ban on {server}",
	"PUT /api/environments/{project}/{env}/preview":               "set previews of {project}/{env}",
	"PUT /api/services/{project}/{env}/{name}/preview":            "set whether previews copy {project}/{env}/{name}",
	"POST /api/projects":                                          "created a project",
	"POST /api/tokens":                                            "created API token {body:name}",
	"DELETE /api/tokens/{id}":                                     "deleted an API token",
	"PUT /api/recovery-key":                                       "set the recovery key",
	"PUT /api/recovery-key/backup-key":                            "set the key the panel signs its backups with",
	"PUT /api/account/password":                                   "changed the owner's password",
	"POST /api/settings/telegram":                                 "started linking Telegram",
	"PUT /api/registries/{host}":                                  "saved the login for {host}",
	"DELETE /api/registries/{host}":                               "removed the login for {host}",
	"POST /api/registries/{host}/test":                            "tested the login for {host}",
	"DELETE /api/settings/telegram":                               "unlinked Telegram",
	"POST /api/settings/telegram/test":                            "sent a test message",
	"PUT /api/settings/telegram/chats/{id}/routes":                "chose which alerts Telegram chat {id} gets",
	"POST /api/servers/{server}/cleanup":                          "cleaned up {server}",
	"DELETE /api/servers/{server}/volumes/{name}":                 "removed volume {name} on {server}",
	"PUT /api/settings/sso":                                       "changed SSO sign-in",
	"POST /api/services/{project}/{env}/{name}/copy":              "copied or moved {project}/{env}/{name}",
	"PUT /api/servers/{server}/tailscale":                         "joined {server} to the tailnet",
	"DELETE /api/servers/{server}/tailscale":                      "took {server} off the tailnet",
	"POST /api/servers/{server}/os/upgrade":                       "installed OS updates on {server}",
	"PUT /api/servers/{server}/drain":                             "sent the logs of {server} to a drain",
	"DELETE /api/servers/{server}/drain":                          "stopped the log drain of {server}",
	"PUT /api/environments/{project}/{env}/sources/{provider}":    "set {provider} as a secret manager of {project}/{env}",
	"DELETE /api/environments/{project}/{env}/sources/{provider}": "removed {provider} from {project}/{env}",
	"PUT /api/settings/hetzner":                                   "connected Hetzner",
	"DELETE /api/settings/hetzner":                                "disconnected Hetzner",
	"POST /api/hetzner/servers":                                   "made a server on Hetzner",
	"PUT /api/settings/builds":                                    "changed where builds run",
	"PUT /api/settings/gitea/{host}":                              "connected Gitea {host}",
	"DELETE /api/settings/gitea/{host}":                           "disconnected Gitea {host}",
	"POST /api/settings/gitea/{host}/test":                        "tested Gitea {host}",
	"POST /api/settings/gitlab/{host}/test":                       "tested GitLab {host}",
	"POST /api/settings/storage/test":                             "tested backup storage",
	"PUT /api/settings/storage":                                   "changed backup storage",
	"PUT /api/settings/storage/copies/{name}":                     "set backup copy {name}",
	"DELETE /api/settings/storage/copies/{name}":                  "removed backup copy {name}",
	"PUT /api/settings/panel":                                     "changed the panel's domain",
	"POST /api/settings/server-backup":                            "backed up the panel",
	"PUT /api/settings/watch":                                     "set up the watcher",
	"DELETE /api/settings/watch":                                  "stopped using the watcher",
	"POST /api/heartbeats":                                        "added a heartbeat",
	"DELETE /api/heartbeats/{id}":                                 "deleted a heartbeat",
	"POST /api/incidents/{id}/note":                               "wrote a note on an incident",
	"POST /api/update":                                            "updated Yawl",
	"POST /api/adopt":                                             "accepted what the servers run",
	"DELETE /api/services/{project}/{env}/{name}":                 "deleted {project}/{env}/{name}",
	"DELETE /api/environments/{project}/{env}":                    "deleted {project}/{env}",
	"DELETE /api/projects/{project}":                              "deleted {project}",
	"POST /api/trash/{kind}/{id}/restore":                         "restored a {kind} from the trash",
	"POST /api/cli-login/{code}/approve":                          "approved a CLI sign-in",
	"POST /api/github/connect":                                    "started connecting GitHub",

	"POST /api/status-pages":                  "made a status page",
	"PUT /api/status-pages/{id}":              "changed a status page",
	"DELETE /api/status-pages/{id}":           "deleted a status page",
	"POST /api/status-pages/{id}/incidents":   "wrote a status page incident",
	"POST /api/status-incidents/{id}/updates": "posted a status page update",
	"POST /api/status-incidents/{id}/publish": "published a status page incident",
	"DELETE /api/status-incidents/{id}":       "removed a status page incident",
}

var reads = map[string]bool{"POST /api/plan": true, "POST /api/inspect": true}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(c int) {
	if s.status == 0 {
		s.status = c
	}
	s.ResponseWriter.WriteHeader(c)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = 200
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (h *handler) audit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		sw := &statusWriter{ResponseWriter: w}
		var body auditBody
		if peeked[r.Method+" "+r.URL.Path] {
			r = peekBody(r, &body)
		}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = 200
		}
		if sw.status >= 400 || reads[r.Pattern] || r.URL.Query().Get("dry") == "1" || r.Pattern == "" {
			return
		}
		text := r.Pattern
		if tpl, ok := sentences[r.Pattern]; ok {
			text = fill(tpl, r, body)
		}
		now := h.a.Now().UnixMilli()
		app.Event(r.Context(), h.a.St.Q, now, "audit", r.Pattern, actor(r),
			map[string]any{"text": text, "path": strings.TrimPrefix(r.Pattern, r.Method+" "), "status": sw.status})
	})
}

func fill(tpl string, r *http.Request, body auditBody) string {
	out := tpl
	for {
		i := strings.IndexByte(out, '{')
		j := strings.IndexByte(out, '}')
		if i < 0 || j < i {
			return out
		}
		key, val := out[i+1:j], ""
		switch key {
		case "body:name":
			val = body.Name
		case "body:required":
			val = map[bool]string{true: "on", false: "off"}[body.Required]
		default:
			val = r.PathValue(key)
		}
		out = out[:i] + val + out[j+1:]
	}
}

func (h *handler) routesAudit(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/audit", h.auditList)
	mux.HandleFunc("GET /api/audit.csv", h.auditCSV)
}

type auditView struct {
	At    int64  `json:"at"`
	Actor string `json:"actor"`
	Text  string `json:"text"`
	Kind  string `json:"kind"`
}

const auditPage = 100

func (h *handler) auditRows(r *http.Request, limit int) ([]auditView, error) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
	to, _ := strconv.ParseInt(q.Get("to"), 10, 64)
	if to == 0 {
		to = h.a.Now().Add(time.Minute).UnixMilli()
	}
	text := strings.TrimSpace(q.Get("q"))
	admin := int64(0)
	if m, _ := member(r); m.Role == "admin" {
		admin = 1
	}
	rows, err := h.a.St.Q.AuditEvents(r.Context(), db.AuditEventsParams{Actor: q.Get("actor"), Q: text,
		FromAt: from, ToAt: to, Admin: admin, Lim: int64(limit), Off: int64(max(page, 0) * auditPage)})
	if err != nil {
		return nil, err
	}
	out := make([]auditView, 0, len(rows))
	for _, e := range rows {
		var b struct{ Text string }
		json.Unmarshal([]byte(e.BodyJson), &b)
		if b.Text == "" {
			b.Text = e.Kind
		}
		out = append(out, auditView{At: e.CreatedAt, Actor: e.Actor, Text: b.Text, Kind: e.Kind})
	}
	return out, nil
}

func (h *handler) auditList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.auditRows(r, auditPage+1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	more := len(rows) > auditPage
	if more {
		rows = rows[:auditPage]
	}
	writeJSON(w, map[string]any{"rows": rows, "more": more})
}

func (h *handler) auditCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := h.auditRows(r, 1_000_000)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="yawl-audit.csv"`)
	c := csv.NewWriter(w)
	c.Write([]string{"at", "actor", "text"})
	for _, e := range rows {
		c.Write([]string{time.UnixMilli(e.At).UTC().Format(time.RFC3339), cell(e.Actor), cell(e.Text)})
	}
	c.Flush()
}

func cell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

type auditBody struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

var peeked = map[string]bool{"POST /api/tokens": true, "PUT /api/settings/two-step": true}

func peekBody(r *http.Request, v *auditBody) *http.Request {
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return r
	}
	json.Unmarshal(b, v)
	r.Body = io.NopCloser(bytes.NewReader(b))
	return r
}
