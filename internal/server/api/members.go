package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
)

func (h *handler) routesMembers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/me", h.me)
	mux.HandleFunc("GET /api/members", h.listMembers)
	mux.HandleFunc("PUT /api/members/{id}", h.setRole)
	mux.HandleFunc("DELETE /api/members/{id}", h.removeMember)
	mux.HandleFunc("GET /api/invites", h.listInvites)
	mux.HandleFunc("POST /api/invites", h.invite)
	mux.HandleFunc("DELETE /api/invites/{id}", h.revokeInvite)
}

type MemberView struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	LastSignInAt int64  `json:"last_sign_in_at,omitempty"`
	TwoStep      bool   `json:"two_step"`
}

func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	m, _ := member(r)
	writeJSON(w, map[string]string{"email": m.Email, "role": m.Role})
}

func (h *handler) listMembers(w http.ResponseWriter, r *http.Request) {
	ms, err := h.a.St.Q.ListMembers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := []MemberView{}
	for _, m := range ms {
		out = append(out, MemberView{ID: m.ID, Email: m.Email, Role: m.Role, LastSignInAt: m.LastSignInAt.Int64, TwoStep: m.TotpSecret.Valid})
	}
	writeJSON(w, out)
}

func (h *handler) setRole(w http.ResponseWriter, r *http.Request) {
	var in struct{ Role string }
	if !readJSON(w, r, &in) {
		return
	}
	if err := h.a.SetRole(r.Context(), r.PathValue("id"), in.Role); err != nil {
		http.Error(w, err.Error(), memberErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) removeMember(w http.ResponseWriter, r *http.Request) {
	if err := h.a.RemoveMember(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), memberErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func memberErr(err error) int {
	if errors.Is(err, app.ErrLastAdmin) {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

type InviteView struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	ExpiresAt int64  `json:"expires_at"`
	CreatedBy string `json:"created_by,omitempty"`
}

func (h *handler) listInvites(w http.ResponseWriter, r *http.Request) {
	rows, err := h.a.St.Q.PendingInvites(r.Context(), time.Now().UnixMilli())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := []InviteView{}
	for _, i := range rows {
		v := InviteView{ID: i.ID, Role: i.Role, ExpiresAt: i.ExpiresAt}
		if m, err := h.a.St.Q.GetOwner(r.Context(), i.CreatedBy); err == nil {
			v.CreatedBy = m.Email
		}
		out = append(out, v)
	}
	writeJSON(w, out)
}

func (h *handler) invite(w http.ResponseWriter, r *http.Request) {
	var in struct{ Role string }
	if !readJSON(w, r, &in) {
		return
	}
	m, _ := member(r)
	tok, id, err := h.a.Invite(r.Context(), in.Role, m.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"token": tok, "id": id})
}

func (h *handler) revokeInvite(w http.ResponseWriter, r *http.Request) {
	n, err := h.a.St.Q.DeleteInvite(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n == 0 {
		http.Error(w, "no such pending invite", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) acceptInvite(w http.ResponseWriter, r *http.Request) {
	ip, now := clientIP(r), h.a.Now()
	if !h.lim.take(ip, now) {
		http.Error(w, "Too many attempts. Try again in a few minutes.", http.StatusTooManyRequests)
		return
	}
	in, ok := readLogin(w, r)
	if !ok {
		return
	}
	id, err := h.a.AcceptInvite(r.Context(), in.Token, in.Email, in.Password)
	if !errors.Is(err, app.ErrInvite) {
		h.lim.refund(ip, now)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	o, err := h.a.St.Q.GetOwner(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !h.secondStep(w, r, o) {
		return
	}
	s, err := h.a.NewSession(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.setCookie(w, r, s)
	writeJSON(w, map[string]bool{"joined": true})
}
