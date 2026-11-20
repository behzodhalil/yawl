package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	cliLoginTTL     = 10 * time.Minute
	cliLoginPending = 20
	cliLoginPerAddr = 3
)

const (
	errCliCodeGone = "This code has expired or was already used."
	errCliCheck    = "That is not the code the browser showed. Run yawl login again."
)

var cliCodeRe = regexp.MustCompile(`^[A-Z2-7]{10}$`)

type cliLogin struct {
	host, from  string
	at, expires time.Time
	used        bool
	owner       string
	check       string
}

type cliLogins struct {
	mu sync.Mutex
	m  map[string]*cliLogin
}

func (c *cliLogins) prune(now time.Time) {
	for k, v := range c.m {
		if now.After(v.expires) {
			delete(c.m, k)
		}
	}
}

func (h *handler) cliRegister(w http.ResponseWriter, r *http.Request) {
	var in struct{ Code, Host string }
	if !readJSON(w, r, &in) {
		return
	}
	in.Host = strings.TrimSpace(in.Host)
	if !cliCodeRe.MatchString(in.Code) || in.Host == "" || len(in.Host) > 64 {
		http.Error(w, "code must be 10 base32 characters and host 1 to 64 characters", http.StatusBadRequest)
		return
	}
	now := h.a.Now()
	h.cli.mu.Lock()
	defer h.cli.mu.Unlock()
	h.cli.prune(now)
	if len(h.cli.m) >= cliLoginPending {
		http.Error(w, "Too many logins are waiting. Try again in a few minutes.", http.StatusTooManyRequests)
		return
	}
	from, n := clientIP(r), 0
	for _, l := range h.cli.m {
		if addrBucket(l.from) == addrBucket(from) && !l.used {
			n++
		}
	}
	if n >= cliLoginPerAddr {
		http.Error(w, "Too many sign-in codes are waiting from this address. Wait 10 minutes or use --token.", http.StatusTooManyRequests)
		return
	}
	if _, taken := h.cli.m[in.Code]; taken {
		http.Error(w, "that code is in use", http.StatusConflict)
		return
	}
	h.cli.m[in.Code] = &cliLogin{host: in.Host, from: from, at: now, expires: now.Add(cliLoginTTL)}
	w.WriteHeader(http.StatusCreated)
}

func (h *handler) cliPoll(w http.ResponseWriter, r *http.Request) {
	h.cli.mu.Lock()
	defer h.cli.mu.Unlock()
	h.cli.prune(h.a.Now())
	l, ok := h.cli.m[r.PathValue("code")]
	switch {
	case !ok:
		http.Error(w, "The login expired; run yawl login again.", http.StatusNotFound)
	case !l.used:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{"host": l.host, "from": l.from, "at": l.at.UnixMilli()})
	default:
		delete(h.cli.m, r.PathValue("code"))
		if subtle.ConstantTimeCompare([]byte(strings.ToUpper(r.URL.Query().Get("check"))), []byte(l.check)) != 1 {
			http.Error(w, errCliCheck, http.StatusForbidden)
			return
		}
		tok, err := h.a.CreateTokenFor(r.Context(), "CLI on "+l.host, l.owner)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"token": tok})
	}
}

func (h *handler) cliInfo(w http.ResponseWriter, r *http.Request) {
	h.cli.mu.Lock()
	defer h.cli.mu.Unlock()
	h.cli.prune(h.a.Now())
	l, ok := h.cli.m[r.PathValue("code")]
	if !ok {
		http.Error(w, errCliCodeGone, http.StatusGone)
		return
	}
	writeJSON(w, map[string]any{"host": l.host, "from": l.from, "at": l.at.UnixMilli(), "approved": l.used})
}

func (h *handler) cliApprove(w http.ResponseWriter, r *http.Request) {
	h.cli.mu.Lock()
	defer h.cli.mu.Unlock()
	h.cli.prune(h.a.Now())
	l, ok := h.cli.m[r.PathValue("code")]
	if !ok || l.used {
		http.Error(w, errCliCodeGone, http.StatusGone)
		return
	}
	m, _ := member(r)
	b := make([]byte, 5)
	rand.Read(b)
	l.used, l.owner, l.check = true, m.ID, base32.StdEncoding.EncodeToString(b)
	writeJSON(w, map[string]string{"check": l.check})
}
