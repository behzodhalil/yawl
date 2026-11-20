package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

const fwTrial = 2 * time.Minute

func (h *handler) routesFirewall(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{server}/firewall", h.firewallView)
	mux.HandleFunc("PUT /api/servers/{server}/firewall", h.firewallPut)
	mux.HandleFunc("POST /api/servers/{server}/firewall/keep", h.firewallKeep)
	mux.HandleFunc("POST /api/servers/{server}/firewall/unban", h.firewallUnban)
	mux.HandleFunc("PUT /api/settings/firewall", h.firewallSettings)
}

func (h *handler) firewallSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Telegram bool `json:"telegram"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	v := "off"
	if in.Telegram {
		v = "on"
	}
	if err := h.a.St.Q.SetSetting(r.Context(), db.SetSettingParams{Key: app.FirewallTelegramKey, Value: v}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"telegram": in.Telegram})
}

func (h *handler) serverOf(w http.ResponseWriter, r *http.Request) (db.Server, bool) {
	s, err := h.a.St.Q.GetServerByName(r.Context(), r.PathValue("server"))
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such server", http.StatusNotFound)
		return s, false
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return s, false
	}
	return s, true
}

func (h *handler) fwSend(w http.ResponseWriter, r *http.Request, serverID, name string, args any) (proto.Result, bool) {
	cmd, _ := proto.NewCommand(fmt.Sprintf("fw:%s:%d", serverID, time.Now().UnixNano()), name, args)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res, err := h.gw.Send(ctx, serverID, cmd)
	if err != nil {
		http.Error(w, "The server's agent did not answer: "+err.Error(), http.StatusBadGateway)
		return res, false
	}
	if !res.OK {
		http.Error(w, res.Cause, http.StatusBadGateway)
		return res, false
	}
	return res, true
}

type fwOpen struct {
	Proto string   `json:"proto"`
	Port  int      `json:"port"`
	From  []string `json:"from,omitempty"`
	Why   string   `json:"why"`
}

func (h *handler) firewallView(w http.ResponseWriter, r *http.Request) {
	s, ok := h.serverOf(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	rules, err := h.a.FirewallRulesOf(ctx, s.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	args, err := h.a.FirewallArgs(ctx, s.ID, rules)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	yawl := []fwOpen{{Proto: "tcp", Port: 22, From: rules.SSHFrom, Why: "SSH (the port sshd listens on)"},
		{Proto: "tcp", Port: 80, Why: "Web traffic"}, {Proto: "both", Port: 443, Why: "Web traffic"}}
	if len(args.Peers) > 0 {
		yawl = append(yawl, fwOpen{Proto: "udp", Port: 51820, From: args.Peers, Why: "The private network, from your other servers"})
	}
	for _, k := range args.Kept {
		yawl = append(yawl, fwOpen{Proto: k.Proto, Port: k.Port, From: k.From, Why: "A Postgres outside port"})
	}
	tg, _ := h.a.St.Q.GetSetting(ctx, app.FirewallTelegramKey)
	out := map[string]any{"rules": rules, "yawl": yawl, "your_address": clientIP(r), "status": nil, "telegram": tg == "on"}
	if t, ok := h.a.FirewallTrialOf(ctx, s.ID); ok && t.UntilMs > h.a.Now().UnixMilli() {
		out["trial_until"], out["trial"] = t.UntilMs, t.Rules
	}
	cmd, _ := proto.NewCommand(fmt.Sprintf("fw:%s:%d", s.ID, time.Now().UnixNano()), proto.CmdFirewallStatus, struct{}{})
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if res, err := h.gw.Send(sctx, s.ID, cmd); err != nil {
		out["status_error"] = "The server's agent did not answer: " + err.Error()
	} else if !res.OK {
		out["status_error"] = res.Cause
	} else {
		var st proto.FirewallStatus
		json.Unmarshal(res.Data, &st)
		out["status"] = st
	}
	writeJSON(w, out)
}

func (h *handler) firewallPut(w http.ResponseWriter, r *http.Request) {
	s, ok := h.serverOf(w, r)
	if !ok {
		return
	}
	var in struct {
		app.FirewallRules
		ConfirmAddress bool `json:"confirm_address"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	rules := in.FirewallRules
	if err := app.CheckFirewallRules(rules); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if ip := clientIP(r); rules.LocksOut(ip) && !in.ConfirmAddress {
		http.Error(w, "Your address "+ip+" is not in the list.", http.StatusConflict)
		return
	}
	ctx := r.Context()
	args, err := h.a.FirewallArgs(ctx, s.ID, rules)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !rules.On {
		if _, ok := h.fwSend(w, r, s.ID, proto.CmdFirewall, args); !ok {
			return
		}
		if err := h.a.SetFirewallRules(ctx, s.ID, rules); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"trial_until": 0})
		return
	}
	args.Trial = true
	if _, ok := h.fwSend(w, r, s.ID, proto.CmdFirewall, args); !ok {
		return
	}
	until := h.a.Now().Add(fwTrial).UnixMilli()
	if err := h.a.SetFirewallTrial(ctx, s.ID, rules, until, args.Version); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"trial_until": until, "version": args.Version})
}

func (h *handler) firewallKeep(w http.ResponseWriter, r *http.Request) {
	s, ok := h.serverOf(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	t, ok := h.a.FirewallTrialOf(ctx, s.ID)
	if !ok || t.UntilMs <= h.a.Now().UnixMilli() {
		http.Error(w, "There is no change waiting to be kept; it went back.", http.StatusConflict)
		return
	}
	if _, ok := h.fwSend(w, r, s.ID, proto.CmdFirewallKeep, proto.FirewallKeepArgs{Version: t.Version}); !ok {
		return
	}
	if err := h.a.KeepFirewallTrial(ctx, s.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"kept": true})
}

func (h *handler) firewallUnban(w http.ResponseWriter, r *http.Request) {
	s, ok := h.serverOf(w, r)
	if !ok {
		return
	}
	var in struct {
		IP string `json:"ip"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if _, ok := h.fwSend(w, r, s.ID, proto.CmdFirewallUnban, proto.FirewallUnbanArgs{IP: in.IP}); ok {
		w.WriteHeader(http.StatusNoContent)
	}
}
