package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

const cookieName = "yawl_session"

type ownerKey struct{}

type limiter struct {
	mu   sync.Mutex
	fail map[string][]time.Time
}

const (
	maxFails   = 10
	failWindow = 10 * time.Minute
)

func (l *limiter) recent(ip string, now time.Time) []time.Time {
	var keep []time.Time
	for _, t := range l.fail[ip] {
		if now.Sub(t) < failWindow {
			keep = append(keep, t)
		}
	}
	if keep == nil {
		delete(l.fail, ip)
	} else {
		l.fail[ip] = keep
	}
	return keep
}

const limiterSweep = 1024

func (l *limiter) sweep(now time.Time) {
	if len(l.fail) > limiterSweep {
		for k := range l.fail {
			l.recent(k, now)
		}
	}
}

var trustedProxy atomic.Pointer[netip.Addr]

func setTrustedProxy(a netip.Addr) { trustedProxy.Store(&a) }

func loopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	if err != nil || ip == nil {
		return false
	}
	if p := trustedProxy.Load(); p != nil && p.IsValid() {
		if a, err := netip.ParseAddr(host); err == nil && a.Unmap() == *p {
			return true
		}
	}
	return ip.IsLoopback()
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" && loopback(r) {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

// addrBucket is what a per-address limit counts: an IPv6 peer is its /64, since
// one host is handed a whole /64.
func addrBucket(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || a.Unmap().Is4() {
		return ip
	}
	return netip.PrefixFrom(a, 64).Masked().String()
}

func secure(r *http.Request) bool {
	return r.TLS != nil || (loopback(r) && r.Header.Get("X-Forwarded-Proto") == "https")
}

func (h *handler) setCookie(w http.ResponseWriter, r *http.Request, session string) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: session, Path: "/", MaxAge: int(app.SessionTTL / time.Second),
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secure(r)})
}

func (h *handler) routesSession(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/session", h.session)
	mux.HandleFunc("POST /api/claim", h.claim)
	mux.HandleFunc("POST /api/invites/accept", h.acceptInvite)
	mux.HandleFunc("POST /api/password-reset", h.passwordReset)
	mux.HandleFunc("POST /api/session", h.signIn)
	mux.HandleFunc("DELETE /api/session", h.signOut)
}

func (h *handler) session(w http.ResponseWriter, r *http.Request) {
	has, err := h.a.HasOwner(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := map[string]any{"claimed": has, "adopting": h.a.Adopting(r.Context())}
	if h.d.Managed {
		out["managed"] = true
	}
	if c, err := r.Cookie(cookieName); err == nil {
		if o, err := h.a.SessionOwner(r.Context(), c.Value); err == nil {
			out["email"] = o.Email
		}
	}
	writeJSON(w, out)
}

type login struct {
	Token    string `json:"token"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func readLogin(w http.ResponseWriter, r *http.Request) (login, bool) {
	var in login
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "Send JSON with email and password.", http.StatusBadRequest)
		return in, false
	}
	return in, true
}

func (h *handler) claim(w http.ResponseWriter, r *http.Request) {
	in, ok := readLogin(w, r)
	if !ok {
		return
	}
	s, err := h.a.Claim(r.Context(), in.Token, in.Email, in.Password)
	switch {
	case errors.Is(err, app.ErrClaimed):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.setCookie(w, r, s)
	writeJSON(w, map[string]any{"claimed": true, "email": strings.ToLower(strings.TrimSpace(in.Email))})
}

func (h *handler) passwordReset(w http.ResponseWriter, r *http.Request) {
	ip, now := clientIP(r), h.a.Now()
	if !h.lim.take(ip, now) {
		http.Error(w, "Too many attempts. Try again in a few minutes.", http.StatusTooManyRequests)
		return
	}
	in, ok := readLogin(w, r)
	if !ok {
		return
	}
	email, err := h.a.ResetPassword(r.Context(), in.Token, in.Password)
	if !errors.Is(err, app.ErrResetLink) {
		h.lim.refund(ip, now)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"email": email})
}

func (h *handler) spendPasswordTime(ctx context.Context, password string) {
	h.a.CheckPassword(ctx, "", password)
}

func (h *handler) signIn(w http.ResponseWriter, r *http.Request) {
	ip, now := clientIP(r), h.a.Now()
	if !h.lim.take(ip, now) {
		http.Error(w, "Too many attempts. Try again in a few minutes.", http.StatusTooManyRequests)
		return
	}
	in, ok := readLogin(w, r)
	if !ok {
		return
	}
	why, glass := h.ssoOnly(r.Context(), in.Email)
	if why != "" && !glass {
		h.spendPasswordTime(r.Context(), in.Password)
		http.Error(w, why, http.StatusForbidden)
		return
	}
	o, err := h.a.CheckPassword(r.Context(), in.Email, in.Password)
	if errors.Is(err, app.ErrWrongLogin) {
		if why != "" {
			http.Error(w, why, http.StatusForbidden)
			return
		}
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.lim.refund(ip, now)
	if !h.secondStep(w, r, o) {
		return
	}
	s, err := h.a.NewSession(r.Context(), o.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.setCookie(w, r, s)
	writeJSON(w, map[string]any{"claimed": true, "email": strings.ToLower(strings.TrimSpace(in.Email))})
}

func (h *handler) signOut(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		h.a.SignOut(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secure(r)})
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) changePassword(w http.ResponseWriter, r *http.Request) {
	o, ok := r.Context().Value(ownerKey{}).(db.Owner)
	c, err := r.Cookie(cookieName)
	if !ok || err != nil {
		http.Error(w, "Sign in to the dashboard to change the password.", http.StatusForbidden)
		return
	}
	var in struct{ Old, New string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "Send JSON with old and new.", http.StatusBadRequest)
		return
	}
	err = h.a.ChangePassword(r.Context(), o.ID, in.Old, in.New, c.Value)
	if errors.Is(err, app.ErrWrongLogin) {
		http.Error(w, "The current password is wrong.", http.StatusBadRequest)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// auth lets a request in with a bearer API token or a session cookie. A
// cookie write must carry X-Yawl-Request, which a page on another site cannot set.
func (h *handler) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && tok != "" {
			t, err := h.a.St.Q.GetAPITokenByHash(r.Context(), app.HashToken(tok))
			if err != nil {
				http.Error(w, "unknown API token", http.StatusUnauthorized)
				return
			}
			m, err := h.a.TokenMember(r.Context(), app.HashToken(tok))
			if err != nil {
				http.Error(w, "unknown API token", http.StatusUnauthorized)
				return
			}
			who := "API token " + t.Name
			if m.Email != "" {
				who += " (" + m.Email + ")"
			}
			next.ServeHTTP(w, r.WithContext(withMember(context.WithValue(r.Context(), actorKey{}, who), m)))
			return
		}
		c, err := r.Cookie(cookieName)
		if err != nil {
			http.Error(w, "missing API token", http.StatusUnauthorized)
			return
		}
		o, err := h.a.SessionOwner(r.Context(), c.Value)
		if err != nil {
			http.Error(w, "Your session has ended. Sign in again.", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Yawl-Request") != "1" {
			http.Error(w, "Missing X-Yawl-Request header.", http.StatusForbidden)
			return
		}
		ctx := withMember(context.WithValue(context.WithValue(r.Context(), actorKey{}, o.Email), ownerKey{}, o), o)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (l *limiter) take(ip string, now time.Time) bool {
	ip = addrBucket(ip)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	recent := l.recent(ip, now)
	if len(recent) >= maxFails {
		return false
	}
	l.fail[ip] = append(recent, now)
	return true
}

func (l *limiter) refund(ip string, now time.Time) {
	ip = addrBucket(ip)
	l.mu.Lock()
	defer l.mu.Unlock()
	if recent := l.recent(ip, now); len(recent) > 0 {
		l.fail[ip] = recent[:len(recent)-1]
	}
}
