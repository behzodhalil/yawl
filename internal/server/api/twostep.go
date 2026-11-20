package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

const (
	ticketTTL   = 5 * time.Minute
	ticketTries = 5
)

const errTicketGone = "This sign in has expired. Start again."

type ticketKind string

const (
	ticketCode  ticketKind = "code"
	ticketSetup ticketKind = "setup"
)

type ticket struct {
	ownerID string
	kind    ticketKind
	expires time.Time
	tries   int
}

type tickets struct {
	mu sync.Mutex
	m  map[string]*ticket
}

func (ts *tickets) add(ownerID string, kind ticketKind, now time.Time) string {
	b := make([]byte, 24)
	rand.Read(b)
	id := base64.RawURLEncoding.EncodeToString(b)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for k, t := range ts.m {
		if now.After(t.expires) {
			delete(ts.m, k)
		}
	}
	ts.m[id] = &ticket{ownerID: ownerID, kind: kind, expires: now.Add(ticketTTL)}
	return id
}

func (ts *tickets) get(id string, kind ticketKind, now time.Time) *ticket {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	t := ts.m[id]
	if t == nil || t.kind != kind || now.After(t.expires) || t.tries >= ticketTries {
		return nil
	}
	t.tries++
	return t
}

func (ts *tickets) take(id string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	_, ok := ts.m[id]
	delete(ts.m, id)
	return ok
}

func (h *handler) routesTwoStep(outer, mux *http.ServeMux) {
	outer.HandleFunc("POST /api/session/code", h.signInCode)
	outer.HandleFunc("POST /api/session/setup", h.signInSetup)
	outer.HandleFunc("POST /api/session/setup/confirm", h.signInSetupConfirm)
	mux.HandleFunc("GET /api/account/two-step", h.twoStepStatus)
	mux.HandleFunc("POST /api/account/two-step", h.twoStepStart)
	mux.HandleFunc("PUT /api/account/two-step", h.twoStepConfirm)
	mux.HandleFunc("DELETE /api/account/two-step", h.twoStepOff)
	mux.HandleFunc("POST /api/account/two-step/codes", h.twoStepCodes)
	mux.HandleFunc("DELETE /api/members/{id}/two-step", h.twoStepReset)
	mux.HandleFunc("PUT /api/settings/two-step", h.twoStepRequired)
}

func (h *handler) secondStep(w http.ResponseWriter, r *http.Request, o db.Owner) bool {
	need, err := h.a.NeedsSecondStep(r.Context(), o)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	if need == "" {
		return true
	}
	writeJSON(w, map[string]string{"second": need, "ticket": h.tix.add(o.ID, ticketKind(need), h.a.Now())})
	return false
}

type codeIn struct {
	Ticket string `json:"ticket"`
	Code   string `json:"code"`
}

func readCode(w http.ResponseWriter, r *http.Request) (codeIn, bool) {
	var in codeIn
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "Send JSON with the code.", http.StatusBadRequest)
		return in, false
	}
	return in, true
}

func (h *handler) ticketOf(w http.ResponseWriter, r *http.Request, kind ticketKind) (codeIn, *ticket, bool) {
	ip, now := clientIP(r), h.a.Now()
	if !h.lim.take(ip, now) {

		http.Error(w, "Too many attempts. Try again in a few minutes.", http.StatusTooManyRequests)
		return codeIn{}, nil, false
	}
	in, ok := readCode(w, r)
	if !ok {
		return in, nil, false
	}
	t := h.tix.get(in.Ticket, kind, now)
	if t == nil {
		http.Error(w, errTicketGone, http.StatusUnauthorized)
		return in, nil, false
	}
	return in, t, true
}

func (h *handler) wrong(w http.ResponseWriter, err error) { codeErr(w, err) }

func (h *handler) signedIn(w http.ResponseWriter, r *http.Request, in codeIn, ownerID string, out map[string]any) {
	if !h.tix.take(in.Ticket) {
		http.Error(w, errTicketGone, http.StatusUnauthorized)
		return
	}
	s, err := h.a.NewSession(r.Context(), ownerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.setCookie(w, r, s)
	writeJSON(w, out)
}

func (h *handler) signInCode(w http.ResponseWriter, r *http.Request) {
	in, t, ok := h.ticketOf(w, r, ticketCode)
	if !ok {
		return
	}
	recovery, err := h.a.CheckCode(r.Context(), h.d.Vault, t.ownerID, in.Code)
	if err != nil {
		h.wrong(w, err)
		return
	}
	o, err := h.a.St.Q.GetOwner(r.Context(), t.ownerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if recovery {
		app.Event(r.Context(), h.a.St.Q, h.a.Now().UnixMilli(), "audit", r.Pattern, o.Email,
			map[string]any{"text": "signed in with a recovery code", "path": r.URL.Path, "status": 200})
	}
	h.signedIn(w, r, in, t.ownerID, map[string]any{"claimed": true, "email": o.Email})
}

// issuer names this panel in the authenticator app: the host without a
// port, since apps split the label on the colon (25-2fa).
func (h *handler) issuer() string {
	if u, err := url.Parse(h.d.PublicURL); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "Yawl"
}

func (h *handler) startOut(w http.ResponseWriter, r *http.Request, ownerID, code string) {
	secret, uri, err := h.a.StartTwoStep(r.Context(), h.d.Vault, ownerID, h.issuer(), code)
	if err != nil {
		codeErr(w, err)
		return
	}
	qr, err := app.QRSVG(uri)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"secret": secret, "uri": uri, "qr": qr})
}

func (h *handler) signInSetup(w http.ResponseWriter, r *http.Request) {
	if _, t, ok := h.ticketOf(w, r, ticketSetup); ok {
		h.startOut(w, r, t.ownerID, "")
	}
}

func (h *handler) signInSetupConfirm(w http.ResponseWriter, r *http.Request) {
	in, t, ok := h.ticketOf(w, r, ticketSetup)
	if !ok {
		return
	}
	o, err := h.a.St.Q.GetOwner(r.Context(), t.ownerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	codes, err := h.a.ConfirmTwoStep(r.Context(), h.d.Vault, t.ownerID, in.Code)
	if err != nil {
		h.wrong(w, err)
		return
	}
	app.Event(r.Context(), h.a.St.Q, h.a.Now().UnixMilli(), "audit", r.Pattern, o.Email,
		map[string]any{"text": "turned on two-step sign in", "path": r.URL.Path, "status": 200})
	h.signedIn(w, r, in, t.ownerID, map[string]any{"claimed": true, "email": o.Email, "recovery_codes": codes})
}

func (h *handler) accountCode(w http.ResponseWriter, r *http.Request, m db.Owner) (codeIn, bool) {
	if !h.lim.take("member "+m.ID, h.a.Now()) {
		http.Error(w, "Too many attempts. Try again in a few minutes.", http.StatusTooManyRequests)
		return codeIn{}, false
	}
	return readCode(w, r)
}

func me(w http.ResponseWriter, r *http.Request) (db.Owner, bool) {
	o, ok := r.Context().Value(ownerKey{}).(db.Owner)
	if !ok || o.ID == "" {
		http.Error(w, "Sign in to the dashboard to change two-step sign in.", http.StatusForbidden)
		return o, false
	}
	return o, true
}

func (h *handler) twoStepStatus(w http.ResponseWriter, r *http.Request) {
	m, ok := me(w, r)
	if !ok {
		return
	}
	on, left, required, err := h.a.TwoStepStatus(r.Context(), m.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"on": on, "codes_left": left, "required": required})
}

func (h *handler) twoStepStart(w http.ResponseWriter, r *http.Request) {
	m, ok := me(w, r)
	if !ok {
		return
	}
	var in codeIn
	if r.ContentLength != 0 {
		if in, ok = h.accountCode(w, r, m); !ok {
			return
		}
	}
	h.startOut(w, r, m.ID, in.Code)
}

func (h *handler) twoStepConfirm(w http.ResponseWriter, r *http.Request) {
	m, ok := me(w, r)
	if !ok {
		return
	}
	in, ok := h.accountCode(w, r, m)
	if !ok {
		return
	}
	codes, err := h.a.ConfirmTwoStep(r.Context(), h.d.Vault, m.ID, in.Code)
	if err != nil {
		codeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"recovery_codes": codes})
}

func codeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, app.ErrWrongCode) {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

func (h *handler) twoStepOff(w http.ResponseWriter, r *http.Request) {
	m, ok := me(w, r)
	if !ok {
		return
	}
	in, ok := h.accountCode(w, r, m)
	if !ok {
		return
	}
	if err := h.a.TurnOffTwoStep(r.Context(), h.d.Vault, m.ID, in.Code); err != nil {
		codeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) twoStepCodes(w http.ResponseWriter, r *http.Request) {
	m, ok := me(w, r)
	if !ok {
		return
	}
	in, ok := h.accountCode(w, r, m)
	if !ok {
		return
	}
	codes, err := h.a.NewRecoveryCodes(r.Context(), h.d.Vault, m.ID, in.Code)
	if err != nil {
		codeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"recovery_codes": codes})
}

func (h *handler) twoStepReset(w http.ResponseWriter, r *http.Request) {
	m, ok := me(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id == m.ID {
		http.Error(w, "Turn off your own two-step sign in in Account.", http.StatusBadRequest)
		return
	}
	if err := h.a.ResetTwoStep(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) twoStepRequired(w http.ResponseWriter, r *http.Request) {
	m, ok := me(w, r)
	if !ok {
		return
	}
	var in struct {
		Required bool `json:"required"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil {
		http.Error(w, "Send JSON with required.", http.StatusBadRequest)
		return
	}
	keep := ""
	if c, err := r.Cookie(cookieName); err == nil {
		keep = c.Value
	}
	if err := h.a.SetRequired(r.Context(), m.ID, in.Required, keep); err != nil {
		codeErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"required": in.Required})
}
