package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/deploy"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/server/workers"
	"github.com/behzodhalil/yawl/internal/spec"
	"github.com/behzodhalil/yawl/internal/update"
)

type DeployView struct {
	ID        string     `json:"id"`
	Service   string     `json:"service"`
	State     string     `json:"state"`
	Cause     string     `json:"cause,omitempty"`
	Commit    string     `json:"commit,omitempty"`
	Rollback  bool       `json:"rollback"`
	CreatedAt int64      `json:"created_at"`
	Steps     []StepView `json:"steps"`
	Warnings  []string   `json:"warnings"`
	Digest    string     `json:"digest,omitempty"`
	DeployWords
}

type StepView struct {
	Step       string `json:"step"`
	State      string `json:"state"`
	Output     string `json:"output,omitempty"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at,omitempty"`
}

type StatusRow struct {
	Project   string   `json:"project"`
	Env       string   `json:"env"`
	Service   string   `json:"service"`
	Kind      string   `json:"kind"`
	URL       string   `json:"url,omitempty"`
	DeployID  string   `json:"deploy_id,omitempty"`
	State     string   `json:"state"`
	Commit    string   `json:"commit,omitempty"`
	Cause     string   `json:"cause,omitempty"`
	Health    string   `json:"health"`
	Tags      []string `json:"tags,omitempty"`
	Subject   string   `json:"subject,omitempty"`
	At        int64    `json:"at,omitempty"`
	NoPreview bool     `json:"no_preview,omitempty"`
}

type Sender interface {
	Send(ctx context.Context, serverID string, cmd proto.Command) (proto.Result, error)
}

type handler struct {
	a       *app.App
	gw      Sender
	lim     *limiter
	d       Deps
	cli     *cliLogins
	tix     *tickets
	relayUp *relayUp
}

type Deps struct {
	App                  *app.App
	GW                   Sender
	PublicURL            string
	Managed              bool
	InstallURL, Releases string
	TrustedProxy         netip.Addr
	Vault                *vault.Vault
	TelegramBase         string
	Tokens               TokenSource
	InspectTimeout       time.Duration
	RunSelfBackup        func(ctx context.Context) error
	Version              string
	Exe                  string
	Restart              func()
	Updater              func() (update.Updater, error)
	CloudflareBase       string
	WatchCode            func() ([]byte, error)
	Terms                Terminals
	ImportDir            string
}

func New(d Deps) http.Handler {
	if d.TrustedProxy.IsValid() {
		setTrustedProxy(d.TrustedProxy)
	}
	h := &handler{a: d.App, gw: d.GW, d: d, lim: &limiter{fail: map[string][]time.Time{}}, cli: &cliLogins{m: map[string]*cliLogin{}}, tix: &tickets{m: map[string]*ticket{}}, relayUp: &relayUp{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/plan", h.apply(true))
	mux.HandleFunc("POST /api/apply", h.apply(false))
	mux.HandleFunc("POST /api/services/{project}/{env}/{name}/deploys", h.deploy)
	mux.HandleFunc("POST /api/services/{project}/{env}/{name}/rollback", h.rollback)
	mux.HandleFunc("GET /api/services/{project}/{env}/{name}/domains", h.domains)
	mux.HandleFunc("GET /api/services/{project}/{env}/{name}/logs", h.logs)
	mux.HandleFunc("GET /api/services/{project}/{env}/{name}/terminal", h.terminal)
	mux.HandleFunc("GET /api/services/{project}/{env}/{name}/analytics", h.analytics)
	mux.HandleFunc("GET /api/servers/{server}/cleanup", h.lastCleanup)
	mux.HandleFunc("POST /api/servers/{server}/cleanup", h.cleanupNow)
	mux.HandleFunc("GET /api/servers/{server}/volumes/unused", h.unusedVolumes)
	mux.HandleFunc("DELETE /api/servers/{server}/volumes/{name}", h.removeVolume)
	mux.HandleFunc("GET /api/settings/sso", h.getSSO)
	mux.HandleFunc("PUT /api/settings/sso", h.setSSO)
	mux.HandleFunc("POST /api/services/{project}/{env}/{name}/copy", h.copyService)
	mux.HandleFunc("GET /api/environments/{project}/{env}/export", h.exportEnv)
	mux.HandleFunc("GET /api/servers/{server}/tailscale", h.tailscaleStatus)
	mux.HandleFunc("PUT /api/servers/{server}/tailscale", h.tailscaleUp)
	mux.HandleFunc("DELETE /api/servers/{server}/tailscale", h.tailscaleDown)
	mux.HandleFunc("GET /api/servers/{server}/os", h.osStatus)
	mux.HandleFunc("POST /api/servers/{server}/os/upgrade", h.osUpgrade)
	mux.HandleFunc("GET /api/servers/{server}/drain", h.drainGet)
	mux.HandleFunc("PUT /api/servers/{server}/drain", h.drainSet)
	mux.HandleFunc("DELETE /api/servers/{server}/drain", h.drainOff)
	mux.HandleFunc("GET /api/environments/{project}/{env}/sources", h.listSources)
	mux.HandleFunc("PUT /api/environments/{project}/{env}/sources/{provider}", h.setSource)
	mux.HandleFunc("DELETE /api/environments/{project}/{env}/sources/{provider}", h.deleteSource)
	h.routesGitea(mux)
	h.routesHetzner(mux)
	mux.HandleFunc("GET /api/settings/builds", h.buildsGet)
	mux.HandleFunc("PUT /api/settings/builds", h.buildsPut)
	mux.HandleFunc("GET /api/shared-env", h.listShared)
	mux.HandleFunc("GET /api/shared-env/keys", h.sharedKeys)
	mux.HandleFunc("PUT /api/shared-env/team/{key}", h.setShared)
	mux.HandleFunc("PUT /api/shared-env/servers/{server}/{key}", h.setShared)
	mux.HandleFunc("DELETE /api/shared-env/team/{key}", h.deleteShared)
	mux.HandleFunc("DELETE /api/shared-env/servers/{server}/{key}", h.deleteShared)
	mux.HandleFunc("PUT /api/services/{project}/{env}/{name}/import", h.importUpload)
	mux.HandleFunc("POST /api/services/{project}/{env}/{name}/import", h.importFrom)
	mux.HandleFunc("GET /api/deploys/{id}", h.getDeploy)
	mux.HandleFunc("POST /api/deploys/{id}/cancel", h.cancelDeploy)
	mux.HandleFunc("GET /api/deploys/{id}/stream", h.stream)
	mux.HandleFunc("GET /api/status", h.status)
	h.routesEnv(mux)
	h.routesDB(mux)
	h.routesViews(mux)
	h.routesForms(mux)
	h.routesMove(mux)
	h.routesSettings(mux)
	h.routesTemplates(mux)
	h.routesWatch(mux)
	h.routesCron(mux)
	h.routesMetrics(mux)
	h.routesAudit(mux)
	h.routesTrash(mux)
	h.routesPreviews(mux)
	h.routesMembers(mux)
	h.routesFirewall(mux)
	h.routesNotifications(mux)
	h.routesGitLab(mux)
	h.routesStartStop(mux)
	h.routesLogAlerts(mux)
	h.routesBilling(mux)
	h.routesData(mux)
	h.routesStatusPages(mux)
	mux.HandleFunc("POST /api/inspect", h.inspect)
	h.routesUpdate(mux)
	mux.HandleFunc("GET /api/adopt", h.adoptView)
	mux.HandleFunc("POST /api/adopt", h.adopt)
	mux.HandleFunc("PUT /api/account/password", h.changePassword)
	outer := http.NewServeMux()
	h.routesSession(outer)
	outer.HandleFunc("GET /api/session/sso", h.ssoInfo)
	outer.HandleFunc("GET /auth/sso/start", h.ssoStart)
	outer.HandleFunc("GET /auth/sso/callback", h.ssoCallback)
	h.routesTwoStep(outer, mux)
	outer.HandleFunc("POST /api/cli-login", h.cliRegister)
	outer.HandleFunc("GET /api/cli-login/{code}", h.cliPoll)
	mux.HandleFunc("GET /api/cli-login/{code}/info", h.cliInfo)
	mux.HandleFunc("POST /api/cli-login/{code}/approve", h.cliApprove)
	outer.Handle("/api/", h.auth(h.roles(mux, h.audit(mux))))
	return outer
}

func (h *handler) apply(dry bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f, err := spec.Parse(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		changes, err := h.a.Apply(applyCtx(r), f, actor(r), dry)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, changes)
	}
}

func applyCtx(r *http.Request) context.Context {
	if m, _ := member(r); m.Role != "admin" {
		return app.Restricted(r.Context())
	}
	return r.Context()
}

func (h *handler) deploy(w http.ResponseWriter, r *http.Request) {
	svc, err := h.a.St.Q.GetServiceByName(r.Context(), db.GetServiceByNameParams{
		Project: r.PathValue("project"), Env: r.PathValue("env"), Service: r.PathValue("name")})
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such service", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var in struct {
		Commit  string `json:"commit"`
		NoCache bool   `json:"no_cache"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in)
	queue := h.a.Deploy
	if in.NoCache {
		queue = h.a.DeployNoCache
	}
	id, err := queue(r.Context(), svc.ID, in.Commit, actor(r))
	if errors.Is(err, app.ErrUnpaid) {
		http.Error(w, err.Error(), http.StatusPaymentRequired)
		return
	}
	if err == nil && svc.UserStopped == 1 {
		err = h.a.St.Q.ClearUserStopped(r.Context(), db.ClearUserStoppedParams{UpdatedAt: time.Now().UnixMilli(), ID: svc.ID})
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"deploy_id": id})
}

func (h *handler) rollback(w http.ResponseWriter, r *http.Request) {
	svc, err := h.a.St.Q.GetServiceByName(r.Context(), db.GetServiceByNameParams{
		Project: r.PathValue("project"), Env: r.PathValue("env"), Service: r.PathValue("name")})
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such service", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var in struct {
		To string `json:"to"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in)
	id, to, err := h.a.Rollback(r.Context(), svc.ID, in.To, actor(r))
	if err == nil && svc.UserStopped == 1 {
		err = h.a.St.Q.ClearUserStopped(r.Context(), db.ClearUserStoppedParams{UpdatedAt: time.Now().UnixMilli(), ID: svc.ID})
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]string{"deploy_id": id, "to": to, "note": app.RollbackNote(r.Context(), h.a.St.Q, svc.CurrentDeployID.String)})
}

func (h *handler) domains(w http.ResponseWriter, r *http.Request) {
	svc, err := h.a.St.Q.GetServiceByName(r.Context(), db.GetServiceByNameParams{
		Project: r.PathValue("project"), Env: r.PathValue("env"), Service: r.PathValue("name")})
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such service", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ds, err := h.a.St.Q.ListDomains(r.Context(), svc.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := []map[string]string{}
	for _, d := range ds {
		out = append(out, map[string]string{"host": d.Host, "dns": d.DnsState, "dns_detail": d.DnsDetail, "cert": d.CertState})
	}
	writeJSON(w, out)
}

func (h *handler) logs(w http.ResponseWriter, r *http.Request) {
	svc, err := h.a.St.Q.GetServiceByName(r.Context(), db.GetServiceByNameParams{
		Project: r.PathValue("project"), Env: r.PathValue("env"), Service: r.PathValue("name")})
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such service", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a := proto.LogsArgs{Project: svc.Project, Env: svc.Env, Service: svc.Name, Query: r.URL.Query().Get("q"),
		Drain: r.URL.Query().Get("from") == "drain", Stream: r.URL.Query().Get("stream")}
	if a.Stream != "" && a.Stream != "stderr" {
		http.Error(w, "stream is stderr, or not given", http.StatusBadRequest)
		return
	}
	if s := r.URL.Query().Get("since"); s != "" {
		if a.Since, err = time.Parse(time.RFC3339Nano, s); err != nil {
			http.Error(w, "since must be an RFC 3339 time", http.StatusBadRequest)
			return
		}
	}
	if n := r.URL.Query().Get("n"); n != "" {
		if a.Limit, err = strconv.Atoi(n); err != nil || a.Limit < 1 {
			http.Error(w, "n must be a positive number", http.StatusBadRequest)
			return
		}
	}
	if a.Vars, err = workers.ServiceVars(r.Context(), h.a.St.Q, svc.EnvironmentID, svc.ID); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	cmd, _ := proto.NewCommand(fmt.Sprintf("logs:%s:%d", svc.ID, time.Now().UnixNano()), proto.CmdLogs, a)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := h.gw.Send(ctx, svc.ServerID, cmd)
	if err != nil {
		http.Error(w, "the agent did not answer: "+err.Error(), http.StatusBadGateway)
		return
	}
	if !res.OK {
		http.Error(w, res.Cause, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(res.Data)
}

func (h *handler) view(r *http.Request, id string) (DeployView, error) {
	d, err := h.a.St.Q.GetDeploy(r.Context(), id)
	if err != nil {
		return DeployView{}, err
	}
	svc, err := h.a.St.Q.GetService(r.Context(), d.ServiceID)
	if err != nil {
		return DeployView{}, err
	}
	steps, err := h.a.St.Q.DeploySteps(r.Context(), id)
	if err != nil {
		return DeployView{}, err
	}
	current, since, err := h.liveNow(r.Context(), svc.CurrentDeployID, svc.EnvironmentID)
	if err != nil {
		return DeployView{}, err
	}
	v := DeployView{ID: d.ID, Service: svc.Name, State: d.State, Cause: d.Cause, Commit: d.CommitSha,
		Rollback: d.Rollback == 1, CreatedAt: d.CreatedAt, Steps: []StepView{}, Warnings: []string{}, Digest: d.Digest, DeployWords: words(d, current, since)}
	ws, err := h.a.St.Q.DeployWarnings(r.Context(), id)
	if err != nil {
		return DeployView{}, err
	}
	for _, body := range ws {
		var w struct{ Text string }
		json.Unmarshal([]byte(body), &w)
		v.Warnings = append(v.Warnings, w.Text)
	}
	for _, s := range steps {
		v.Steps = append(v.Steps, StepView{Step: s.Step, State: s.State, Output: s.Output, StartedAt: s.StartedAt, FinishedAt: s.FinishedAt.Int64})
	}
	return v, nil
}

func (h *handler) getDeploy(w http.ResponseWriter, r *http.Request) {
	v, err := h.view(r, r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such deploy", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, v)
}

func (h *handler) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	var last []byte
	for {
		v, err := h.view(r, r.PathValue("id"))
		if err != nil {
			fmt.Fprintf(w, "event: error\ndata: %q\n\n", err.Error())
			fl.Flush()
			return
		}
		b, _ := json.Marshal(v)
		if string(b) != string(last) {
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
			last = b
		}
		if deploy.Terminal(deploy.State(v.State)) {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (h *handler) status(w http.ResponseWriter, r *http.Request) {
	rows, err := h.statusRows(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, rows)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

type actorKey struct{}

func actor(r *http.Request) string {
	if a, ok := r.Context().Value(actorKey{}).(string); ok {
		return a
	}
	return "unknown"
}
