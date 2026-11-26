package api

import (
	"context"
	"net/http"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

const (
	viewer = iota
	developer
	admin
)

var roleRank = map[string]int{"viewer": viewer, "developer": developer, "admin": admin}

var adminReads = map[string]string{
	"GET /api/settings/notifications":                                 "change settings",
	"GET /api/shared-env":                                             "manage shared variables",
	"GET /api/settings/sso":                                           "change settings",
	"GET /api/shared-env/keys":                                        "manage shared variables",
	"GET /api/settings/gitlab":                                        "change settings",
	"GET /api/settings/gitea":                                         "change settings",
	"GET /api/hetzner":                                                "manage servers",
	"GET /api/settings/builds":                                        "change settings",
	"GET /api/services/{project}/{env}/{name}/terminal":               "open a terminal",
	"GET /api/services/{project}/{env}/{name}/env/{key}/recovery_box": "read secrets",
	"GET /api/settings/storage":                                       "see settings",
	"GET /api/settings/telegram":                                      "see settings",
	"GET /api/registries":                                             "see settings",
	"GET /api/settings/server-backup":                                 "see settings",
	"GET /api/adopt":                                                  "adopt a restored panel",
	"GET /api/environments/{project}/{env}/move":                      "move environments",
	"GET /api/audit.csv":                                              "export the audit log",
	"GET /api/members":                                                "manage members",
	"GET /api/invites":                                                "manage members",
	"GET /api/billing":                                                "see billing",
}

var viewerReads = map[string]bool{
	"GET /api/settings/commit-status":                      true,
	"GET /api/github":                                      true,
	"GET /api/github/repos":                                true,
	"GET /api/servers/{server}/firewall":                   true,
	"GET /api/account/two-step":                            true,
	"GET /api/audit":                                       true,
	"GET /api/templates":                                   true,
	"GET /api/templates/{id}":                              true,
	"GET /api/checks":                                      true,
	"GET /api/cli-login/{code}/info":                       true,
	"GET /api/deploys/{id}":                                true,
	"GET /api/deploys/{id}/stream":                         true,
	"GET /api/environments/{project}/{env}/env":            true,
	"GET /api/environments/{project}/{env}/keys":           true,
	"GET /api/environments/{project}/{env}/moves/latest":   true,
	"GET /api/heartbeats":                                  true,
	"GET /api/incidents":                                   true,
	"GET /api/me":                                          true,
	"GET /api/tokens":                                      true,
	"GET /api/projects":                                    true,
	"GET /api/projects/{project}/previews":                 true,
	"GET /api/projects/{project}/preview-senders":          true,
	"GET /api/recovery-key":                                true,
	"GET /api/servers":                                     true,
	"GET /api/servers/{server}/metrics":                    true,
	"GET /api/servers/{server}/cleanup":                    true,
	"GET /api/servers/{server}/tailscale":                  true,
	"GET /api/servers/{server}/os":                         true,
	"GET /api/servers/{server}/drain":                      true,
	"GET /api/environments/{project}/{env}/sources":        true,
	"GET /api/environments/{project}/{env}/export":         true,
	"GET /api/servers/{server}/volumes/unused":             true,
	"GET /api/services/{project}/{env}/{name}":             true,
	"GET /api/services/{project}/{env}/{name}/backups":     true,
	"GET /api/services/{project}/{env}/{name}/pitr":        true,
	"GET /api/services/{project}/{env}/{name}/delete-info": true,
	"GET /api/services/{project}/{env}/{name}/deploys":     true,
	"GET /api/services/{project}/{env}/{name}/domains":     true,
	"GET /api/services/{project}/{env}/{name}/env":         true,
	"GET /api/services/{project}/{env}/{name}/keys":        true,
	"GET /api/services/{project}/{env}/{name}/logs":        true,
	"GET /api/services/{project}/{env}/{name}/log-alerts":  true,
	"GET /api/services/{project}/{env}/{name}/metrics":     true,
	"GET /api/services/{project}/{env}/{name}/analytics":   true,
	"GET /api/services/{project}/{env}/{name}/runs":        true,
	"GET /api/settings/panel":                              true,
	"GET /api/settings/watch":                              true,
	"GET /api/status":                                      true,
	"GET /api/trash":                                       true,
	"GET /api/update":                                      true,

	"GET /api/status-pages":                true,
	"GET /api/status-pages/{id}/preview":   true,
	"GET /api/status-pages/{id}/incidents": true,
}

var everyoneWrites = map[string]bool{
	"PUT /api/account/password":          true,
	"POST /api/cli-login/{code}/approve": true,
	"POST /api/tokens":                   true,
	"DELETE /api/tokens/{id}":            true,
	"POST /api/account/two-step":         true,
	"PUT /api/account/two-step":          true,
	"DELETE /api/account/two-step":       true,
	"POST /api/account/two-step/codes":   true,
}

var developerWrites = map[string]bool{
	"POST /api/apply": true,
	"POST /api/environments/{project}/{env}/templates/{id}": true,
	"POST /api/plan":    true,
	"POST /api/inspect": true,
	"POST /api/services/{project}/{env}/{name}/deploys":           true,
	"POST /api/services/{project}/{env}/{name}/rollback":          true,
	"POST /api/services/{project}/{env}/{name}/restart":           true,
	"POST /api/services/{project}/{env}/{name}/stop":              true,
	"POST /api/services/{project}/{env}/{name}/start":             true,
	"PUT /api/services/{project}/{env}/{name}/log-alerts":         true,
	"PUT /api/services/{project}/{env}/{name}/password":           true,
	"PUT /api/services/{project}/{env}/{name}/maintenance":        true,
	"POST /api/deploys/{id}/cancel":                               true,
	"POST /api/services/{project}/{env}/{name}/runs":              true,
	"POST /api/services/{project}/{env}/{name}/backups":           true,
	"PUT /api/services/{project}/{env}/{name}/env/{key}":          true,
	"DELETE /api/services/{project}/{env}/{name}/env/{key}":       true,
	"PUT /api/environments/{project}/{env}/env/{key}":             true,
	"PUT /api/environments/{project}/{env}/sources/{provider}":    true,
	"DELETE /api/environments/{project}/{env}/sources/{provider}": true,
	"DELETE /api/environments/{project}/{env}/env/{key}":          true,
	"POST /api/environments/{project}/{env}/services":             true,
	"POST /api/projects/{project}/compare":                        true,
	"POST /api/incidents/{id}/note":                               true,
	"POST /api/previews/{id}/close":                               true,
	"POST /api/heartbeats":                                        true,
	"DELETE /api/heartbeats/{id}":                                 true,
	"POST /api/services/{project}/{env}/{name}/data/tables":       true,
	"POST /api/services/{project}/{env}/{name}/data/rows":         true,
	"POST /api/services/{project}/{env}/{name}/data/query":        true,

	"POST /api/status-pages/{id}/incidents":   true,
	"POST /api/status-incidents/{id}/updates": true,
	"POST /api/status-incidents/{id}/publish": true,
	"DELETE /api/status-incidents/{id}":       true,
}

var outsideAuth = map[string]bool{
	"POST /api/claim":                 true,
	"POST /api/session":               true,
	"DELETE /api/session":             true,
	"POST /api/cli-login":             true,
	"POST /api/invites/accept":        true,
	"POST /api/password-reset":        true,
	"GET /api/session":                true,
	"GET /api/session/sso":            true,
	"GET /api/cli-login/{code}":       true,
	"POST /api/session/code":          true,
	"POST /api/session/setup":         true,
	"POST /api/session/setup/confirm": true,
}

var adminWrites = map[string]string{
	"POST /api/services/{project}/{env}/{name}/env/{key}/reveal": "reveal secrets",
	"POST /api/services/{project}/{env}/{name}/restore":          "restore backups",
	"PUT /api/shared-env/team/{key}":                             "manage shared variables",
	"PUT /api/shared-env/servers/{server}/{key}":                 "manage shared variables",
	"DELETE /api/shared-env/team/{key}":                          "manage shared variables",
	"DELETE /api/shared-env/servers/{server}/{key}":              "manage shared variables",
	"POST /api/services/{project}/{env}/{name}/copy":             "copy or move a service",
	"PUT /api/services/{project}/{env}/{name}/import":            "import a dump",
	"POST /api/services/{project}/{env}/{name}/import":           "import a dump",
	"DELETE /api/services/{project}/{env}/{name}":                "delete",
	"DELETE /api/environments/{project}/{env}":                   "delete",
	"DELETE /api/projects/{project}":                             "delete",
	"POST /api/trash/{kind}/{id}/restore":                        "restore from the trash",
	"POST /api/environments/{project}/{env}/move":                "move environments",
	"POST /api/moves/{id}/retry":                                 "move environments",
	"POST /api/join-token":                                       "manage servers",
	"POST /api/projects":                                         "create projects",
	"POST /api/environments":                                     "create environments",
	"PUT /api/environments/{project}/{env}/preview":              "change settings",
	"PUT /api/services/{project}/{env}/{name}/preview":           "change settings",
	"PUT /api/projects/{project}/preview-senders":                "change settings",
	"PUT /api/settings/panel":                                    "change settings",
	"PUT /api/settings/storage":                                  "change settings",
	"PUT /api/settings/sso":                                      "change settings",
	"PUT /api/settings/storage/copies/{name}":                    "change settings",
	"DELETE /api/settings/storage/copies/{name}":                 "change settings",
	"PUT /api/settings/watch":                                    "change settings",
	"DELETE /api/settings/watch":                                 "change settings",
	"POST /api/settings/telegram":                                "change settings",
	"PUT /api/registries/{host}":                                 "change settings",
	"DELETE /api/registries/{host}":                              "change settings",
	"POST /api/settings/telegram/test":                           "change settings",
	"DELETE /api/settings/telegram":                              "change settings",
	"POST /api/settings/telegram/connect":                        "change settings",
	"DELETE /api/settings/telegram/connect":                      "change settings",
	"DELETE /api/settings/telegram/chats/{id}":                   "change settings",
	"POST /api/settings/telegram/chats/{id}/test":                "change settings",
	"PUT /api/settings/telegram/chats/{id}/routes":               "change settings",
	"POST /api/settings/server-backup":                           "change settings",
	"PUT /api/recovery-key":                                      "change the recovery key",
	"PUT /api/recovery-key/backup-key":                           "change the recovery key",
	"POST /api/update":                                           "update Yawl",
	"POST /api/adopt":                                            "adopt a restored panel",
	"POST /api/invites":                                          "manage members",
	"DELETE /api/invites/{id}":                                   "manage members",
	"PUT /api/members/{id}":                                      "manage members",
	"DELETE /api/members/{id}":                                   "manage members",
	"DELETE /api/members/{id}/two-step":                          "manage members",
	"PUT /api/settings/two-step":                                 "change settings",
	"PUT /api/servers/{server}/firewall":                         "manage servers",
	"POST /api/servers/{server}/cleanup":                         "manage servers",
	"PUT /api/servers/{server}/tailscale":                        "manage servers",
	"DELETE /api/servers/{server}/tailscale":                     "manage servers",
	"POST /api/servers/{server}/os/upgrade":                      "manage servers",
	"PUT /api/servers/{server}/drain":                            "manage servers",
	"DELETE /api/servers/{server}/drain":                         "manage servers",
	"DELETE /api/servers/{server}/volumes/{name}":                "manage servers",
	"PUT /api/settings/firewall":                                 "change settings",
	"PUT /api/settings/notifications/{channel}":                  "change settings",
	"PUT /api/settings/gitlab/{host}":                            "change settings",
	"PUT /api/settings/gitea/{host}":                             "change settings",
	"DELETE /api/settings/gitea/{host}":                          "change settings",
	"PUT /api/settings/hetzner":                                  "manage servers",
	"DELETE /api/settings/hetzner":                               "manage servers",
	"POST /api/hetzner/servers":                                  "manage servers",
	"PUT /api/settings/builds":                                   "change settings",
	"DELETE /api/settings/gitlab/{host}":                         "change settings",
	"DELETE /api/settings/notifications/{channel}":               "change settings",
	"POST /api/settings/notifications/{channel}/test":            "change settings",
	"POST /api/settings/storage/test":                            "change settings",
	"POST /api/registries/{host}/test":                           "change settings",
	"POST /api/settings/gitea/{host}/test":                       "change settings",
	"POST /api/settings/gitlab/{host}/test":                      "change settings",
	"POST /api/servers/{server}/firewall/keep":                   "manage servers",
	"POST /api/servers/{server}/firewall/unban":                  "manage servers",
	"POST /api/github/connect":                                   "connect GitHub",
	"POST /api/services/{project}/{env}/{name}/data/write":       "write to a database",

	"POST /api/status-pages":        "set up status pages",
	"PUT /api/status-pages/{id}":    "set up status pages",
	"DELETE /api/status-pages/{id}": "set up status pages",
}

type memberKey struct{}

func member(r *http.Request) (db.Owner, bool) {
	m, ok := r.Context().Value(memberKey{}).(db.Owner)
	return m, ok
}

func withMember(ctx context.Context, m db.Owner) context.Context {
	return context.WithValue(ctx, memberKey{}, m)
}

func allowed(role, pattern string) (bool, string) {
	rank, ok := roleRank[role]
	if !ok {
		rank = viewer
	}
	name := map[int]string{viewer: "Viewers", developer: "Developers"}[rank]
	if len(pattern) > 4 && pattern[:4] == "GET " {
		switch verb, secret := adminReads[pattern]; {
		case rank == admin, viewerReads[pattern] && !secret:
			return true, ""
		case secret:
			return false, name + " cannot " + verb + "."
		}
		return false, name + " cannot do this."
	}
	switch {
	case rank == admin, everyoneWrites[pattern]:
		return true, ""
	case rank == viewer:
		return false, "Viewers cannot change anything."
	case developerWrites[pattern]:
		return true, ""
	}
	verb, ok := adminWrites[pattern]
	if !ok {
		verb = "do this"
	}
	return false, name + " cannot " + verb + "."
}

var cloudsOwn = map[string]bool{
	"GET /api/hetzner": true, "PUT /api/settings/hetzner": true, "DELETE /api/settings/hetzner": true, "POST /api/hetzner/servers": true,
	"PUT /api/settings/panel": true,
}

func (h *handler) roles(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m, ok := member(r)
		if !ok {
			http.Error(w, "No member for this request.", http.StatusForbidden)
			return
		}
		_, pattern := mux.Handler(r)
		if h.d.Managed && cloudsOwn[pattern] {
			http.Error(w, "On Yawl Cloud this is the cloud's to set.", http.StatusForbidden)
			return
		}
		if ok, why := allowed(m.Role, pattern); !ok {
			http.Error(w, why, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
