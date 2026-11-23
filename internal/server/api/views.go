package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/join"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

func (h *handler) routesViews(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects", h.projects)
	mux.HandleFunc("GET /api/services/{project}/{env}/{name}", h.serviceView)
	mux.HandleFunc("GET /api/services/{project}/{env}/{name}/deploys", h.deploys)
	mux.HandleFunc("GET /api/servers", h.servers)
	mux.HandleFunc("POST /api/join-token", h.joinToken)
}

type ServiceForm struct {
	Name              string        `json:"name"`
	Kind              string        `json:"kind"`
	Repo              string        `json:"repo"`
	Branch            string        `json:"branch"`
	Port              int           `json:"port"`
	HealthPath        string        `json:"health_path"`
	HealthTimeoutSec  int64         `json:"health_timeout"`
	DrainTimeoutSec   int64         `json:"drain_timeout"`
	AutoDeploy        bool          `json:"auto_deploy"`
	Release           []string      `json:"release"`
	ReleaseTimeoutSec int64         `json:"release_timeout"`
	Domains           []string      `json:"domains"`
	Version           int           `json:"version"`
	BackupAt          string        `json:"backup_at"`
	OutsidePort       int           `json:"outside_port"`
	Allow             []string      `json:"allow"`
	Schedule          string        `json:"schedule"`
	Timezone          string        `json:"timezone"`
	Command           []string      `json:"command"`
	TimeoutSec        int64         `json:"timeout"`
	Volumes           []spec.Volume `json:"volumes"`
	CPUCores          float64       `json:"cpu"`
	Memory            string        `json:"memory"`
	Image             string        `json:"image"`
	Backup            bool          `json:"backup"`
	PITR              bool          `json:"pitr"`
	Builder           string        `json:"builder"`
	Root              string        `json:"root"`
	NoIndex           bool          `json:"noindex"`
	NoPreview         bool          `json:"no_preview"`
	WWW               string        `json:"www"`
	Watch             []string      `json:"watch"`
	Replicas          int           `json:"replicas,omitempty"`
	Tags              []string      `json:"tags,omitempty"`
	Keep              *spec.Keep    `json:"keep,omitempty"`
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func formOf(s spec.Service) ServiceForm {
	return ServiceForm{Name: s.Name, Kind: string(s.Kind), Repo: s.Repo, Branch: s.Branch, Port: s.Port, HealthPath: s.HealthPath,
		HealthTimeoutSec: int64(s.HealthTimeout / time.Second), DrainTimeoutSec: int64(s.DrainTimeout / time.Second), AutoDeploy: s.AutoDeploy,
		Release: nonNil(s.Release), ReleaseTimeoutSec: int64(s.ReleaseTimeout / time.Second), Domains: nonNil(s.Domains),
		Version: s.Version, BackupAt: s.BackupAt, OutsidePort: s.OutsidePort, Allow: nonNil(s.Allow),
		Schedule: s.Schedule, Timezone: s.Timezone, Command: nonNil(s.Command), TimeoutSec: int64(s.Timeout / time.Second),
		Volumes: nonNilVolumes(s.Volumes), CPUCores: float64(s.CPU), Memory: memoryText(s.Memory), Image: s.Image,
		Backup: spec.IsDatabase(s.Kind) && s.BackupOn(), PITR: s.PITR, Builder: s.Builder, Root: s.Root, NoIndex: s.NoIndex, NoPreview: !s.PreviewOn(), WWW: s.WWW, Watch: nonNil(s.Watch), Replicas: s.Replicas, Tags: s.Tags, Keep: s.Keep}
}

func memoryText(s spec.Size) string {
	if s == 0 {
		return ""
	}
	return s.String()
}

func nonNilVolumes(v []spec.Volume) []spec.Volume {
	if v == nil {
		return []spec.Volume{}
	}
	return v
}

func (f ServiceForm) spec() (spec.Service, error) {
	if !spec.IsDatabase(spec.Kind(f.Kind)) && len(f.Volumes) == 0 {
		f.BackupAt = ""
	}
	mem, err := spec.ParseSize(f.Memory)
	var backup *bool
	if f.Backup && spec.BackupChosen(spec.Kind(f.Kind)) {
		backup = &f.Backup
	}
	var preview *bool
	if f.NoPreview {
		preview = new(bool)
	}
	return spec.Service{Backup: backup, PITR: f.PITR, Builder: f.Builder, Root: f.Root, NoIndex: f.NoIndex, Preview: preview, WWW: f.WWW, Watch: f.Watch, Replicas: f.Replicas, Tags: f.Tags, Keep: f.Keep, Name: f.Name, Kind: spec.Kind(f.Kind), Repo: f.Repo, Branch: f.Branch, Port: f.Port, HealthPath: f.HealthPath,
		HealthTimeout: time.Duration(f.HealthTimeoutSec) * time.Second, DrainTimeout: time.Duration(f.DrainTimeoutSec) * time.Second,
		AutoDeploy: f.AutoDeploy, Release: f.Release, ReleaseTimeout: time.Duration(f.ReleaseTimeoutSec) * time.Second,
		Domains: f.Domains, Version: f.Version, BackupAt: f.BackupAt, OutsidePort: f.OutsidePort, Allow: f.Allow,
		Schedule: f.Schedule, Timezone: f.Timezone, Command: f.Command, Timeout: time.Duration(f.TimeoutSec) * time.Second,
		Volumes: f.Volumes, CPU: spec.Cores(f.CPUCores), Memory: mem, Image: f.Image}, err
}

func (h *handler) serviceURL(ctx context.Context, id, kind, service, env, serverID string) string {
	if kind != string(spec.KindWeb) {
		return ""
	}
	ds, _ := h.a.St.Q.ListDomains(ctx, id)
	for _, d := range ds {
		if d.DnsState == "ok" && d.CertState == "issued" {
			return "https://" + d.Host
		}
	}
	srv, err := h.a.St.Q.GetServer(ctx, serverID)
	if err != nil {
		return ""
	}
	ip, err := netip.ParseAddr(srv.PublicIp)
	if err != nil {
		return ""
	}
	return "https://" + app.FreeHost(service, env, ip)
}

func (h *handler) statusRows(ctx context.Context) ([]StatusRow, error) {
	svcs, err := h.a.St.Q.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	rows := []StatusRow{}
	for _, s := range svcs {
		row := StatusRow{Project: s.Project, Env: s.Env, Service: s.Name, Kind: s.Kind, State: "never deployed", Health: s.Health,
			URL: h.serviceURL(ctx, s.ID, s.Kind, s.Name, s.Env, s.ServerID), NoPreview: s.NoPreview == 1}
		json.Unmarshal([]byte(s.TagsJson), &row.Tags)
		if ds, err := h.a.St.Q.ListDeploysForService(ctx, db.ListDeploysForServiceParams{ServiceID: s.ID, Limit: 1}); err == nil && len(ds) > 0 {
			row.DeployID, row.State, row.Commit, row.Cause, row.Subject, row.At = ds[0].ID, ds[0].State, ds[0].CommitSha, ds[0].Cause, ds[0].Subject, ds[0].CreatedAt
			if ds[0].FinishedAt.Valid {
				row.At = ds[0].FinishedAt.Int64
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

type EnvView struct {
	Name      string      `json:"name"`
	Server    string      `json:"server"`
	Services  []StatusRow `json:"services"`
	Preview   bool        `json:"preview,omitempty"`
	PreviewOf string      `json:"preview_of,omitempty"`

	ServerOffline  bool  `json:"server_offline,omitempty"`
	ServerLastSeen int64 `json:"server_last_seen,omitempty"`
}

type ProjectView struct {
	Name string    `json:"name"`
	Envs []EnvView `json:"envs"`
}

func (h *handler) projects(w http.ResponseWriter, r *http.Request) {
	envs, err := h.a.St.Q.ListEnvironmentTree(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows, err := h.statusRows(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	servers, err := h.a.St.Q.ListServers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	serverByName := map[string]db.Server{}
	for _, s := range servers {
		serverByName[s.Name] = s
	}
	out := []ProjectView{}
	for _, e := range envs {
		if len(out) == 0 || out[len(out)-1].Name != e.Project {
			out = append(out, ProjectView{Name: e.Project, Envs: []EnvView{}})
		}
		ev := EnvView{Name: e.Env, Server: e.Server, Services: []StatusRow{}, Preview: e.Preview == 1, PreviewOf: e.PreviewOf}
		if s := serverByName[e.Server]; s.Offline == 1 {
			ev.ServerOffline, ev.ServerLastSeen = true, s.LastHeartbeat.Int64
		}
		for _, row := range rows {
			if row.Project == e.Project && row.Env == e.Env {
				ev.Services = append(ev.Services, row)
			}
		}
		p := &out[len(out)-1]
		p.Envs = append(p.Envs, ev)
	}
	writeJSON(w, out)
}

type DomainView struct {
	Host      string `json:"host"`
	DNSState  string `json:"dns_state"`
	DNSDetail string `json:"dns_detail,omitempty"`
	CertState string `json:"cert_state"`
}

type ServiceView struct {
	ID              string       `json:"id"`
	Form            ServiceForm  `json:"form"`
	URL             string       `json:"url,omitempty"`
	Health          string       `json:"health"`
	Server          string       `json:"server"`
	CurrentDeployID string       `json:"current_deploy_id,omitempty"`
	Domains         []DomainView `json:"domains"`
	MemoryLimit     int64        `json:"memory_limit,omitempty"`
	Stopped         bool         `json:"stopped,omitempty"`
	Maintenance     bool         `json:"maintenance,omitempty"`
	PasswordUser    string       `json:"password_user,omitempty"`
	UsedBy          []UsedByView `json:"used_by,omitempty"`
	ServerOffline   bool         `json:"server_offline,omitempty"`
	ServerLastSeen  int64        `json:"server_last_seen,omitempty"`
}

type UsedByView struct {
	Env     string `json:"env"`
	Service string `json:"service"`
}

func (h *handler) serviceView(w http.ResponseWriter, r *http.Request) {
	row, ok := h.service(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	svc, err := h.storedService(ctx, row.EnvironmentID, row.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sp, err := app.ToSpec(h.a.St.Q, ctx, svc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var auth struct{ User string }
	json.Unmarshal([]byte(svc.BasicAuth), &auth)
	v := ServiceView{ID: svc.ID, Stopped: svc.UserStopped == 1, Maintenance: svc.Maintenance == 1, PasswordUser: auth.User, Form: formOf(sp), MemoryLimit: int64(sp.Memory), Health: svc.Health, CurrentDeployID: svc.CurrentDeployID.String, Domains: []DomainView{},
		URL: h.serviceURL(ctx, svc.ID, svc.Kind, svc.Name, row.Env, row.ServerID)}
	if srv, err := h.a.St.Q.GetServer(ctx, row.ServerID); err == nil {
		v.Server = srv.Name
		if srv.Offline == 1 {
			v.ServerOffline, v.ServerLastSeen = true, srv.LastHeartbeat.Int64
		}
	}
	ds, err := h.a.St.Q.ListDomains(ctx, svc.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, d := range ds {
		v.Domains = append(v.Domains, DomainView{Host: d.Host, DNSState: d.DnsState, DNSDetail: d.DnsDetail, CertState: d.CertState})
	}
	if spec.IsDatabase(spec.Kind(svc.Kind)) {
		us, err := h.a.St.Q.DatabaseUsers(ctx, db.DatabaseUsersParams{EnvironmentID: row.EnvironmentID, OwnRef: svc.Name + ".url", CrossRef: row.Env + "/" + svc.Name + ".url"})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, u := range us {
			v.UsedBy = append(v.UsedBy, UsedByView{Env: u.Env, Service: u.Name})
		}
	}
	writeJSON(w, v)
}

type DeployRow struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	Commit     string `json:"commit,omitempty"`
	Cause      string `json:"cause,omitempty"`
	Rollback   bool   `json:"rollback"`
	CreatedAt  int64  `json:"created_at"`
	FinishedAt int64  `json:"finished_at,omitempty"`
	Digest     string `json:"digest,omitempty"`
	DeployWords
}

type DeployWords struct {
	Subject        string `json:"subject,omitempty"`
	By             string `json:"by,omitempty"`
	TookMs         int64  `json:"took_ms,omitempty"`
	Live           bool   `json:"live"`
	CanRollBack    bool   `json:"can_roll_back"`
	AutoRolledBack bool   `json:"auto_rolled_back,omitempty"`
	Restart        bool   `json:"restart,omitempty"`
}

func words(d, current db.Deploy, since int64) DeployWords {
	w := DeployWords{Subject: d.Subject, By: d.Actor, Live: d.ID == current.ID, CanRollBack: app.RollbackOK(d, current, since), AutoRolledBack: d.AutoRolledBack == 1, Restart: d.Restart == 1}
	if d.StartedAt.Valid && d.FinishedAt.Valid {
		w.TookMs = d.FinishedAt.Int64 - d.StartedAt.Int64
	}
	return w
}

func (h *handler) liveNow(ctx context.Context, current sql.NullString, envID string) (db.Deploy, int64, error) {
	since, err := h.a.St.Q.LatestMoveStart(ctx, envID)
	if err != nil || !current.Valid {
		return db.Deploy{}, since, err
	}
	d, err := h.a.St.Q.GetDeploy(ctx, current.String)
	return d, since, err
}

func (h *handler) deploys(w http.ResponseWriter, r *http.Request) {
	row, ok := h.service(w, r)
	if !ok {
		return
	}
	limit := int64(20)
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 1 || n > 100 {
			http.Error(w, "limit is 1 to 100", http.StatusBadRequest)
			return
		}
		limit = n
	}
	ds, err := h.a.St.Q.ListDeploysForService(r.Context(), db.ListDeploysForServiceParams{ServiceID: row.ID, Limit: limit})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	current, since, err := h.liveNow(r.Context(), row.CurrentDeployID, row.EnvironmentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := []DeployRow{}
	for _, d := range ds {
		out = append(out, DeployRow{ID: d.ID, State: d.State, Commit: d.CommitSha, Cause: d.Cause, Rollback: d.Rollback == 1,
			CreatedAt: d.CreatedAt, FinishedAt: d.FinishedAt.Int64, Digest: d.Digest, DeployWords: words(d, current, since)})
	}
	writeJSON(w, out)
}

type ServerView struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	PublicIP      string        `json:"public_ip"`
	AgentVersion  string        `json:"agent_version"`
	LastHeartbeat int64         `json:"last_heartbeat,omitempty"`
	DiskUsedPct   int64         `json:"disk_used_pct"`
	Offline       bool          `json:"offline"`
	PrivateIP     string        `json:"private_ip,omitempty"`
	Network       string        `json:"network,omitempty"`
	Peers         []PeerView    `json:"peers,omitempty"`
	Forecast      *ForecastView `json:"forecast,omitempty"`
	OSName        string        `json:"os_name,omitempty"`
	OSID          string        `json:"os_id,omitempty"`
	Kernel        string        `json:"kernel,omitempty"`
	Cores         int64         `json:"cores,omitempty"`
	BootedAt      int64         `json:"booted_at,omitempty"`
	MemTotal      int64         `json:"mem_total,omitempty"`
	DiskTotal     int64         `json:"disk_total,omitempty"`
	FirewallOff   bool          `json:"firewall_off,omitempty"`
}

type PeerView struct {
	Name          string `json:"name"`
	LastHandshake int64  `json:"last_handshake"`
}

type ForecastView struct {
	Days     *int    `json:"days"`
	GBPerDay float64 `json:"gb_per_day"`
	Growing  bool    `json:"growing"`
}

func (h *handler) servers(w http.ResponseWriter, r *http.Request) {
	ss, err := h.a.St.Q.ListServers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	peers, err := h.a.St.Q.ServerPeers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	names := map[string]string{}
	for _, s := range ss {
		names[s.ID] = s.Name
	}
	out := []ServerView{}
	for _, s := range ss {
		v := ServerView{ID: s.ID, Name: s.Name, PublicIP: s.PublicIp, AgentVersion: s.AgentVersion,
			LastHeartbeat: s.LastHeartbeat.Int64, DiskUsedPct: s.DiskUsedPct, Offline: s.Offline == 1, PrivateIP: s.PrivateIp,
			OSName: s.OsName, OSID: s.OsID, Kernel: s.Kernel, Cores: s.Cores, BootedAt: s.BootedAt}
		if last, err := h.a.St.Q.LastMachineMinutes(r.Context(), db.LastMachineMinutesParams{ServerID: s.ID, Limit: 1}); err == nil && len(last) == 1 {
			v.MemTotal, v.DiskTotal = last[0].MemTotal, last[0].DiskTotal
		}
		if rules, err := h.a.FirewallRulesOf(r.Context(), s.ID); err == nil {
			v.FirewallOff = !rules.On
		}
		if s.WgPubkey == "" {
			v.Network = "rejoin to use the private network"
		} else if cause, err := h.a.St.Q.GetSetting(r.Context(), "network.cause."+s.ID); err == nil {
			v.Network = "the private network is not up: " + cause
		}
		for _, p := range peers {
			if p.ServerID == s.ID && names[p.PeerID] != "" {
				v.Peers = append(v.Peers, PeerView{Name: names[p.PeerID], LastHandshake: p.LastHandshake})
			}
		}
		rows, err := h.a.St.Q.MetricHours(r.Context(), db.MetricHoursParams{ServerID: s.ID, ServiceID: "", At: h.a.Now().Add(-7 * 24 * time.Hour).UnixMilli()})
		if err == nil && len(rows) >= 24 {
			days, rate, growing := app.Forecast(rows, uint64(rows[len(rows)-1].DiskTotal))
			f := &ForecastView{GBPerDay: math.Round(rate*10) / 10, Growing: growing}
			if growing {
				f.Days = &days
			}
			v.Forecast = f
		}
		out = append(out, v)
	}
	writeJSON(w, out)
}

func (h *handler) joinToken(w http.ResponseWriter, r *http.Request) {
	tok, err := join.NewToken(r.Context(), h.a.St.Q, h.a.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if h.d.Managed {
		writeJSON(w, map[string]string{"command": fmt.Sprintf("curl -fsSL %s | YAWL_VERSION=%s YAWL_RELEASES='%s' YAWL_JOIN='%s %s' sh", h.d.InstallURL, h.d.Version, h.d.Releases, h.d.PublicURL, tok)})
		return
	}
	writeJSON(w, map[string]string{"command": "yawl agent join " + h.d.PublicURL + " " + tok})
}

func (h *handler) storedService(ctx context.Context, envID, id string) (db.Service, error) {
	all, err := h.a.St.Q.ListServicesInEnv(ctx, envID)
	if err != nil {
		return db.Service{}, err
	}
	for _, s := range all {
		if s.ID == id {
			return s, nil
		}
	}
	return db.Service{}, sql.ErrNoRows
}
