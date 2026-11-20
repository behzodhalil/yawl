package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type varScope string

const (
	scopeTeam        varScope = "team"
	scopeServer      varScope = "server"
	scopeEnvironment varScope = "shared"
	scopeService     varScope = "service"
)

type sharedEntry struct {
	Scope     varScope `json:"scope"`
	Key       string   `json:"key"`
	Servers   []string `json:"servers"`
	Missing   []string `json:"missing,omitempty"`
	Stale     []string `json:"stale,omitempty"`
	Build     bool     `json:"build,omitempty"`
	UpdatedAt int64    `json:"updated_at"`
}

func validBox(b string) bool {
	_, err := base64.StdEncoding.DecodeString(b)
	return err == nil && b != "" && len(b) <= maxBox
}

func (h *handler) listShared(w http.ResponseWriter, r *http.Request) {
	rows, err := h.a.St.Q.ListSharedVars(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	servers, err := h.a.St.Q.ListServers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := []sharedEntry{}
	at := map[string]int{}
	for _, v := range rows {
		id := v.Scope + "/" + v.Key
		if varScope(v.Scope) == scopeServer {
			id += "/" + v.Server
		}
		i, ok := at[id]
		if !ok {
			i, at[id] = len(out), len(out)
			out = append(out, sharedEntry{Scope: varScope(v.Scope), Key: v.Key, Servers: []string{}, Build: v.Build == 1})
		}
		out[i].Servers = append(out[i].Servers, v.Server)
		if v.SealedTo != "" && v.SealedTo != v.CurrentKey {
			out[i].Stale = append(out[i].Stale, v.Server)
		}
		out[i].UpdatedAt = max(out[i].UpdatedAt, v.UpdatedAt)
	}
	for i, e := range out {
		if e.Scope == scopeTeam {
			for _, s := range servers {
				if !slices.Contains(e.Servers, s.Name) {
					out[i].Missing = append(out[i].Missing, s.Name)
				}
			}
		}
	}
	writeJSON(w, out)
}

func (h *handler) sharedKeys(w http.ResponseWriter, r *http.Request) {
	servers, err := h.a.St.Q.ListServers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type key struct {
		Name        string `json:"name"`
		AgentPubkey string `json:"agent_pubkey"`
	}
	keys := []key{}
	for _, s := range servers {
		keys = append(keys, key{s.Name, s.AgentPubkey})
	}
	rec, _ := h.a.St.Q.GetSetting(r.Context(), "recovery.pubkey")
	writeJSON(w, map[string]any{"servers": keys, "recovery_pubkey": rec})
}

func (h *handler) setShared(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !keyRe.MatchString(key) || key == "PORT" {
		http.Error(w, "key must be UPPER_SNAKE_CASE and not PORT (Yawl sets PORT)", http.StatusBadRequest)
		return
	}
	var in struct {
		Boxes       map[string]string `json:"boxes"`
		Keys        map[string]string `json:"keys"`
		AgentBox    string            `json:"agent_box"`
		RecoveryBox string            `json:"recovery_box"`
		Build       bool              `json:"build"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*maxBox)).Decode(&in); err != nil {
		if errors.As(err, new(*http.MaxBytesError)) {
			http.Error(w, "the variable is too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if _, err := h.a.St.Q.GetSetting(ctx, "recovery.pubkey"); err != nil {
		http.Error(w, "no recovery key yet; run `yawl recovery-key init` first", http.StatusConflict)
		return
	}
	scope, boxes, sealedTo := "team", map[string]string{}, map[string]string{}
	servers, err := h.a.St.Q.ListServers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if name := r.PathValue("server"); name != "" {
		scope = "server"
		i := slices.IndexFunc(servers, func(s db.Server) bool { return s.Name == name })
		if i < 0 {
			http.Error(w, "no server "+name, http.StatusNotFound)
			return
		}
		boxes[servers[i].ID] = in.AgentBox
		in.Boxes = map[string]string{name: in.AgentBox}
	} else {
		var missing []string
		for _, s := range servers {
			if b, ok := in.Boxes[s.Name]; ok {
				boxes[s.ID] = b
			} else {
				missing = append(missing, s.Name)
			}
		}
		if len(missing) > 0 {
			http.Error(w, "seal it to every server's agent; missing: "+strings.Join(missing, ", "), http.StatusBadRequest)
			return
		}
		if len(servers) == 0 {
			http.Error(w, "no server has joined yet: a team variable is sealed to each server's agent", http.StatusConflict)
			return
		}
	}
	var stale []string
	for _, srv := range servers {
		if _, ok := boxes[srv.ID]; ok {
			if in.Keys[srv.Name] != srv.AgentPubkey {
				stale = append(stale, srv.Name)
			}
			sealedTo[srv.ID] = srv.AgentPubkey
		}
	}
	if len(stale) > 0 {
		http.Error(w, strings.Join(stale, ", ")+" joined again since its key was read; reload and set it again", http.StatusConflict)
		return
	}
	for _, b := range append(slices.Collect(mapValues(boxes)), in.RecoveryBox) {
		if !validBox(b) {
			http.Error(w, "each box must be base64, at most 90 KB", http.StatusBadRequest)
			return
		}
	}
	now := time.Now().UnixMilli()
	err = h.a.St.Tx(ctx, func(q *db.Queries) error {
		for server, b := range boxes {
			if err := q.UpsertSharedVar(ctx, db.UpsertSharedVarParams{ID: store.NewID("shv_"), Scope: scope, ServerID: server, Key: key,
				AgentBox: b, RecoveryBox: in.RecoveryBox, Build: boolInt(in.Build), SealedTo: sealedTo[server], CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func mapValues(m map[string]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for _, v := range m {
			if !yield(v) {
				return
			}
		}
	}
}

func (h *handler) deleteShared(w http.ResponseWriter, r *http.Request) {
	scope, server := "team", ""
	if name := r.PathValue("server"); name != "" {
		s, err := h.a.St.Q.GetServerByName(r.Context(), name)
		if err != nil {
			http.Error(w, "no server "+name, http.StatusNotFound)
			return
		}
		scope, server = "server", s.ID
	}
	if err := h.a.St.Q.DeleteSharedVar(r.Context(), db.DeleteSharedVarParams{Scope: scope, Key: r.PathValue("key"), ServerID: server}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
