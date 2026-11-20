package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/selfbackup"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/spec"
)

var keyRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

const maxBox = 90 << 10

type EnvEntry struct {
	Key       string   `json:"key"`
	Scope     varScope `json:"scope"`
	Service   string   `json:"service,omitempty"`
	Build     bool     `json:"build,omitempty"`
	Ref       string   `json:"ref,omitempty"`
	UpdatedAt int64    `json:"updated_at"`
}

func (h *handler) routesEnv(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/recovery-key", h.setRecovery)
	mux.HandleFunc("PUT /api/recovery-key/backup-key", h.setBackupKey)
	mux.HandleFunc("GET /api/services/{project}/{env}/{name}/keys", h.keys)
	mux.HandleFunc("GET /api/services/{project}/{env}/{name}/env", h.listEnv)
	mux.HandleFunc("PUT /api/services/{project}/{env}/{name}/env/{key}", h.setEnv)
	mux.HandleFunc("DELETE /api/services/{project}/{env}/{name}/env/{key}", h.deleteEnv)
	mux.HandleFunc("GET /api/services/{project}/{env}/{name}/env/{key}/recovery_box", h.recoveryBox)
	mux.HandleFunc("PUT /api/environments/{project}/{env}/env/{key}", h.setEnv)
	mux.HandleFunc("DELETE /api/environments/{project}/{env}/env/{key}", h.deleteEnv)
	mux.HandleFunc("GET /api/environments/{project}/{env}/env", h.listEnvironment)
	mux.HandleFunc("GET /api/environments/{project}/{env}/keys", h.keys)
	mux.HandleFunc("POST /api/services/{project}/{env}/{name}/env/{key}/reveal", h.reveal)
	mux.HandleFunc("POST /api/projects/{project}/compare", h.compare)
}

func (h *handler) setRecovery(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pubkey    string `json:"pubkey"`
		BackupKey string `json:"backup_key"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || !isKey(in.Pubkey) || in.BackupKey != "" && !isKey(in.BackupKey) {
		http.Error(w, "pubkey (and backup_key) must be base64 32-byte keys", http.StatusBadRequest)
		return
	}
	errExists := errors.New("a recovery key already exists")
	err := h.a.St.Tx(r.Context(), func(q *db.Queries) error {
		if _, err := q.GetSetting(r.Context(), "recovery.pubkey"); err == nil {
			return errExists
		}
		if in.BackupKey != "" {
			k, _ := base64.StdEncoding.DecodeString(in.BackupKey)
			if err := vault.SetSecret(r.Context(), q, h.d.Vault, selfbackup.KeySetting, k); err != nil {
				return err
			}
		}
		return q.SetSetting(r.Context(), db.SetSettingParams{Key: "recovery.pubkey", Value: in.Pubkey})
	})
	if errors.Is(err, errExists) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (h *handler) setBackupKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || !isKey(in.Key) {
		http.Error(w, "key must be a base64 32-byte key", http.StatusBadRequest)
		return
	}
	k, _ := base64.StdEncoding.DecodeString(in.Key)
	errNone, errExists := errors.New("no recovery key yet; run `yawl recovery-key init` first"), errors.New("the panel already signs its backups")
	err := h.a.St.Tx(r.Context(), func(q *db.Queries) error {
		if _, err := q.GetSetting(r.Context(), "recovery.pubkey"); err != nil {
			return errNone
		}
		if _, err := q.GetSetting(r.Context(), selfbackup.KeySetting); err == nil {
			return errExists
		}
		return vault.SetSecret(r.Context(), q, h.d.Vault, selfbackup.KeySetting, k)
	})
	if errors.Is(err, errNone) || errors.Is(err, errExists) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func isKey(s string) bool {
	b, err := base64.StdEncoding.DecodeString(s)
	return err == nil && len(b) == 32
}

type scope struct {
	envID, serviceID, serviceName, serverID string
	kind                                    spec.Kind
}

func (s scope) ownVar(w http.ResponseWriter, key string) bool {
	if spec.IsDatabase(s.kind) && (key == "PASSWORD" || key == "URL") {
		http.Error(w, "A database's PASSWORD and URL are its own; Yawl makes them.", http.StatusConflict)
		return true
	}
	return false
}

func (h *handler) scope(w http.ResponseWriter, r *http.Request) (scope, bool) {
	ctx := r.Context()
	env, err := h.a.St.Q.GetEnvironmentByName(ctx, db.GetEnvironmentByNameParams{Project: r.PathValue("project"), Env: r.PathValue("env")})
	if err != nil || env.DeletedAt.Valid {
		http.Error(w, "no such environment", http.StatusNotFound)
		return scope{}, false
	}
	s := scope{envID: env.ID, serverID: env.ServerID}
	if name := r.PathValue("name"); name != "" {
		svc, err := h.a.St.Q.GetServiceByName(ctx, db.GetServiceByNameParams{Project: r.PathValue("project"), Env: r.PathValue("env"), Service: name})
		if err != nil {
			http.Error(w, "no such service", http.StatusNotFound)
			return scope{}, false
		}
		s.serviceID, s.serviceName, s.kind = svc.ID, svc.Name, spec.Kind(svc.Kind)
	}
	return s, true
}

func (h *handler) keys(w http.ResponseWriter, r *http.Request) {
	s, ok := h.scope(w, r)
	if !ok {
		return
	}
	srv, err := h.a.St.Q.GetServer(r.Context(), s.serverID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rec, _ := h.a.St.Q.GetSetting(r.Context(), "recovery.pubkey")
	writeJSON(w, map[string]string{"agent_pubkey": srv.AgentPubkey, "recovery_pubkey": rec, "server": srv.Name})
}

func (h *handler) setEnv(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !keyRe.MatchString(key) || key == "PORT" {
		http.Error(w, "key must be UPPER_SNAKE_CASE and not PORT (Yawl sets PORT)", http.StatusBadRequest)
		return
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*maxBox+1024)).Decode(&raw); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	if _, plain := raw["value"]; plain {
		http.Error(w, "send sealed values only: the server does not accept a plaintext value", http.StatusBadRequest)
		return
	}
	if _, isRef := raw["ref"]; isRef {
		h.setRef(w, r, key, raw)
		return
	}
	var agentBox, recoveryBox string
	json.Unmarshal(raw["agent_box"], &agentBox)
	json.Unmarshal(raw["recovery_box"], &recoveryBox)
	var build bool
	json.Unmarshal(raw["build"], &build)
	for _, b := range []string{agentBox, recoveryBox} {
		if _, err := base64.StdEncoding.DecodeString(b); err != nil || b == "" || len(b) > maxBox {
			http.Error(w, "agent_box and recovery_box must be base64, at most 90 KB", http.StatusBadRequest)
			return
		}
	}
	s, ok := h.scope(w, r)
	if !ok || s.ownVar(w, key) {
		return
	}
	if h.moving(w, r, s) {
		return
	}
	ctx := r.Context()
	if srv, err := h.a.St.Q.GetServer(ctx, s.serverID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if srv.AgentPubkey == "" {
		http.Error(w, "server "+srv.Name+" has no secrets key; re-join it", http.StatusConflict)
		return
	}
	if _, err := h.a.St.Q.GetSetting(ctx, "recovery.pubkey"); err != nil {
		http.Error(w, "no recovery key yet; run `yawl recovery-key init` first", http.StatusConflict)
		return
	}
	now := time.Now().UnixMilli()
	if err := h.a.St.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: store.NewID("var_"), EnvironmentID: s.envID, ServiceID: s.serviceID,
		Key: key, AgentBox: agentBox, RecoveryBox: recoveryBox, Build: boolInt(build), CreatedAt: now, UpdatedAt: now}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (h *handler) deleteEnv(w http.ResponseWriter, r *http.Request) {
	s, ok := h.scope(w, r)
	if !ok || s.ownVar(w, r.PathValue("key")) {
		return
	}
	if h.moving(w, r, s) {
		return
	}
	n, err := h.a.St.Q.DeleteEnvVar(r.Context(), db.DeleteEnvVarParams{EnvironmentID: s.envID, ServiceID: s.serviceID, Key: r.PathValue("key")})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n == 0 {
		http.Error(w, "no such variable", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (h *handler) listEnv(w http.ResponseWriter, r *http.Request) {
	s, ok := h.scope(w, r)
	if !ok {
		return
	}
	vars, err := h.a.St.Q.EnvVarsFor(r.Context(), db.EnvVarsForParams{EnvironmentID: s.envID, ServiceID: s.serviceID})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	byKey, order := map[string]EnvEntry{}, []string{}
	shared, err := h.sharedFor(r, s.envID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, v := range shared {
		if _, seen := byKey[v.Key]; !seen {
			order = append(order, v.Key)
		}
		byKey[v.Key] = EnvEntry{Key: v.Key, Scope: varScope(v.Scope), Build: v.Build == 1, UpdatedAt: v.UpdatedAt}
	}
	for _, v := range vars {
		if _, seen := byKey[v.Key]; !seen {
			order = append(order, v.Key)
		}
		e := EnvEntry{Key: v.Key, Scope: scopeEnvironment, Build: v.Build == 1, Ref: v.Ref, UpdatedAt: v.UpdatedAt}
		if v.ServiceID != "" {
			e.Scope = scopeService
		}
		byKey[v.Key] = e
	}
	out := []EnvEntry{}
	for _, k := range order {
		out = append(out, byKey[k])
	}
	writeJSON(w, out)
}

func (h *handler) listEnvironment(w http.ResponseWriter, r *http.Request) {
	s, ok := h.scope(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	vars, err := h.a.St.Q.EnvVarsInEnv(ctx, s.envID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	shared, err := h.sharedFor(r, s.envID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out, at := []EnvEntry{}, map[string]int{}
	for _, v := range shared {
		if i, ok := at[v.Key]; ok {
			out[i].Scope = varScope(v.Scope)
			continue
		}
		at[v.Key] = len(out)
		out = append(out, EnvEntry{Key: v.Key, Scope: varScope(v.Scope), Build: v.Build == 1, UpdatedAt: v.UpdatedAt})
	}
	for _, v := range vars {
		e := EnvEntry{Key: v.Key, Scope: scopeEnvironment, Build: v.Build == 1, Ref: v.Ref, UpdatedAt: v.UpdatedAt}
		if v.ServiceID != "" {
			e.Scope = scopeService
			if svc, err := h.a.St.Q.GetService(ctx, v.ServiceID); err == nil {
				e.Service = svc.Name
			}
		} else if i, ok := at[v.Key]; ok {
			out[i] = e
			continue
		}
		out = append(out, e)
	}
	writeJSON(w, out)
}

func (h *handler) sharedFor(r *http.Request, envID string) ([]db.SharedVarsForRow, error) {
	server, err := h.a.St.Q.EnvironmentServer(r.Context(), envID)
	if err != nil {
		return nil, err
	}
	return h.a.St.Q.SharedVarsFor(r.Context(), server)
}

func (h *handler) recoveryBox(w http.ResponseWriter, r *http.Request) {
	s, ok := h.scope(w, r)
	if !ok {
		return
	}
	v, err := h.envVar(r, s, r.PathValue("key"))
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such variable", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"box": v.RecoveryBox})
}

func (h *handler) envVar(r *http.Request, s scope, key string) (db.EnvVar, error) {
	vars, err := h.a.St.Q.EnvVarsFor(r.Context(), db.EnvVarsForParams{EnvironmentID: s.envID, ServiceID: s.serviceID})
	if err != nil {
		return db.EnvVar{}, err
	}
	var found *db.EnvVar
	for i := range vars {
		if vars[i].Key == key {
			found = &vars[i]
		}
	}
	if found != nil {
		return *found, nil
	}
	shared, err := h.sharedFor(r, s.envID)
	if err != nil {
		return db.EnvVar{}, err
	}
	for _, v := range shared {
		if v.Key == key {
			found = &db.EnvVar{Key: v.Key, AgentBox: v.AgentBox, RecoveryBox: v.RecoveryBox, Build: v.Build}
		}
	}
	if found == nil {
		return db.EnvVar{}, sql.ErrNoRows
	}
	return *found, nil
}

func (h *handler) reveal(w http.ResponseWriter, r *http.Request) {
	var in struct {
		To string `json:"to"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || !isKey(in.To) {
		http.Error(w, "to must be a base64 32-byte public key", http.StatusBadRequest)
		return
	}
	s, ok := h.scope(w, r)
	if !ok {
		return
	}
	v, err := h.envVar(r, s, r.PathValue("key"))
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such variable", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cmd, _ := proto.NewCommand("reveal:"+store.NewID("rv_"), proto.CmdReveal, proto.RevealArgs{Box: v.AgentBox, To: in.To})
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res, err := h.gw.Send(ctx, s.serverID, cmd)
	if err != nil || !res.OK {
		msg := res.Cause
		if err != nil {
			msg = "the agent did not answer; try `yawl env reveal --recovery`"
		}
		http.Error(w, msg, http.StatusBadGateway)
		return
	}
	now, who := time.Now().UnixMilli(), actor(r)
	text := fmt.Sprintf("%s-%s: %s was revealed (%s).", r.PathValue("project"), s.serviceName, r.PathValue("key"), who)
	err = h.a.St.Tx(r.Context(), func(q *db.Queries) error {
		evID := store.NewID("evt_")
		body, _ := json.Marshal(map[string]string{"service": s.serviceID, "key": r.PathValue("key")})
		if err := q.InsertEvent(r.Context(), db.InsertEventParams{ID: evID, Kind: "env.revealed", SubjectID: v.ID, Actor: who, BodyJson: string(body), CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		m := app.Revealed(r.PathValue("project")+"/"+r.PathValue("env")+"/"+s.serviceName, r.PathValue("key"), who, text)
		return q.InsertOutbox(r.Context(), db.InsertOutboxParams{ID: store.NewID("obx_"), EventID: evID, Channel: "telegram", Body: text, MessageJson: m.JSON(now),
			NextAttemptAt: now, CreatedAt: now, UpdatedAt: now})
	})
	if err != nil {
		http.Error(w, "could not record the reveal, so it was not shown: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var data proto.RevealData
	json.Unmarshal(res.Data, &data)
	writeJSON(w, map[string]string{"box": data.Box})
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func (h *handler) setRef(w http.ResponseWriter, r *http.Request, key string, raw map[string]json.RawMessage) {
	if _, a := raw["agent_box"]; a || raw["recovery_box"] != nil {
		http.Error(w, "send a ref or sealed boxes, not both", http.StatusBadRequest)
		return
	}
	var ref string
	json.Unmarshal(raw["ref"], &ref)
	s, ok := h.scope(w, r)
	if !ok || s.ownVar(w, key) {
		return
	}
	ctx := r.Context()
	t, err := app.FindRef(ctx, h.a.St.Q, s.envID, key, ref)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	note := ""
	if t.Cross {
		e, err := h.a.St.Q.EnvProject(ctx, s.envID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		srv, err := h.a.St.Q.GetServer(ctx, e.ServerID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if e.ServerID != t.ServerID {
			if err := app.OnMesh(ctx, h.a.St.Q, t.ServerID, e.ServerID); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		who := r.PathValue("name")
		if who == "" {
			who = "every service of " + r.PathValue("env")
		}
		if note, err = h.a.ShareRef(ctx, t, who, e.ServerID, srv.Name, actor(r)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	now := time.Now().UnixMilli()
	if err := h.a.St.Q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: store.NewID("var_"), EnvironmentID: s.envID, ServiceID: s.serviceID,
		Key: key, Ref: ref, CreatedAt: now, UpdatedAt: now}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "note": note})
}

func (h *handler) compare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Left  string            `json:"left"`
		Right string            `json:"right"`
		Keys  map[string]string `json:"keys"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil || in.Left == "" || in.Right == "" {
		http.Error(w, "left and right must name environments", http.StatusBadRequest)
		return
	}
	for _, env := range []string{in.Left, in.Right} {
		if in.Keys[env] == "" {
			http.Error(w, "A comparison key is needed for "+env+".", http.StatusBadRequest)
			return
		}
	}
	rows, err := h.a.Compare(r.Context(), h.gw, r.PathValue("project"), in.Left, in.Right, in.Keys, actor(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, rows)
}

func (h *handler) moving(w http.ResponseWriter, r *http.Request, s scope) bool {
	m, err := h.a.St.Q.ActiveMove(r.Context(), s.envID)
	if err != nil {
		return false
	}
	to, _ := h.a.St.Q.GetServer(r.Context(), m.ToServerID)
	http.Error(w, fmt.Sprintf("%s is moving to %s; change its variables after the move.", r.PathValue("env"), to.Name), http.StatusConflict)
	return true
}
