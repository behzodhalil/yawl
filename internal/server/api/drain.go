package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type drainView struct {
	URL  string `json:"url"`
	User string `json:"user"`
}

func (h *handler) drainGet(w http.ResponseWriter, r *http.Request) {
	srv, err := h.a.St.Q.GetServerByName(r.Context(), r.PathValue("server"))
	if err != nil {
		http.Error(w, "no server "+r.PathValue("server"), http.StatusNotFound)
		return
	}
	var v drainView
	js, err := h.a.St.Q.GetSetting(r.Context(), "drain."+srv.ID)
	if err == nil {
		json.Unmarshal([]byte(js), &v)
	}
	writeJSON(w, map[string]any{"set": err == nil, "url": v.URL, "user": v.User, "agent_pubkey": srv.AgentPubkey})
}

func (h *handler) drainSet(w http.ResponseWriter, r *http.Request) {
	id, ok := h.serverID(w, r)
	if !ok {
		return
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&raw); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	var in struct{ URL, User, TokenBox string }
	json.Unmarshal(raw["url"], &in.URL)
	json.Unmarshal(raw["user"], &in.User)
	json.Unmarshal(raw["token_box"], &in.TokenBox)
	_, plain := raw["token"]
	if _, err := base64.StdEncoding.DecodeString(in.TokenBox); plain || err != nil {
		http.Error(w, "send the token sealed to the server's agent (token_box); the panel takes no plain token", http.StatusBadRequest)
		return
	}
	if _, err := h.send(r.Context(), id, proto.CmdLogDrain, proto.LogDrainArgs{URL: in.URL, User: in.User, TokenBox: in.TokenBox, Server: r.PathValue("server")}); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	js, _ := json.Marshal(drainView{in.URL, in.User})
	if err := h.a.St.Q.SetSetting(r.Context(), db.SetSettingParams{Key: "drain." + id, Value: string(js)}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) drainOff(w http.ResponseWriter, r *http.Request) {
	id, ok := h.serverID(w, r)
	if !ok {
		return
	}
	if _, err := h.send(r.Context(), id, proto.CmdLogDrain, proto.LogDrainArgs{Off: true}); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if err := h.a.St.Q.DeleteSettingKey(r.Context(), "drain."+id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
