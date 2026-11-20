package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/behzodhalil/yawl/internal/proto"
)

func (h *handler) tailscaleStatus(w http.ResponseWriter, r *http.Request) {
	srv, err := h.a.St.Q.GetServerByName(r.Context(), r.PathValue("server"))
	if err != nil {
		http.Error(w, "no server "+r.PathValue("server"), http.StatusNotFound)
		return
	}
	out := map[string]any{"agent_pubkey": srv.AgentPubkey, "running": false}
	if res, err := h.send(r.Context(), srv.ID, proto.CmdTailscaleStatus, struct{}{}); err == nil {
		var st proto.TailscaleStatus
		json.Unmarshal(res.Data, &st)
		out["running"], out["address"] = st.Running, st.Address
	}
	writeJSON(w, out)
}

func (h *handler) tailscaleUp(w http.ResponseWriter, r *http.Request) {
	id, ok := h.serverID(w, r)
	if !ok {
		return
	}
	var in map[string]json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	var box string
	json.Unmarshal(in["auth_key_box"], &box)
	if _, err := base64.StdEncoding.DecodeString(box); err != nil || box == "" {
		http.Error(w, "send the auth key sealed to the server's agent (auth_key_box); the panel takes no plain key", http.StatusBadRequest)
		return
	}
	if _, err := h.send(r.Context(), id, proto.CmdTailscaleUp, proto.TailscaleUpArgs{AuthKey: box, Hostname: r.PathValue("server")}); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) tailscaleDown(w http.ResponseWriter, r *http.Request) {
	id, ok := h.serverID(w, r)
	if !ok {
		return
	}
	if _, err := h.send(r.Context(), id, proto.CmdTailscaleDown, struct{}{}); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
