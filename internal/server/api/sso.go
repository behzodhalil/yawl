package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

const ssoCookie = "yawl_sso_state"

type ssoFlow struct {
	nonce, verifier string
	at              time.Time
}

var (
	ssoFlows sync.Map
	ssoCount atomic.Int64
	ssoSwept atomic.Int64
)

type ssoSettings struct {
	Name         string `json:"name"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	Only         bool   `json:"only"`
	SecretSet    bool   `json:"secret_set,omitempty"`
	Works        bool   `json:"works"`
	Callback     string `json:"callback,omitempty"`
}

func (h *handler) ssoConfig(ctx context.Context) (ssoSettings, string) {
	s := ssoSettings{Name: h.setting(ctx, "sso.name"), Issuer: h.setting(ctx, "sso.issuer"), ClientID: h.setting(ctx, "sso.client_id"),
		Only: h.setting(ctx, "sso.only") == "1"}
	secret := ""
	if h.d.Vault != nil {
		if b, err := vault.GetSecret(ctx, h.a.St.Q, h.d.Vault, "sso.client_secret"); err == nil {
			secret = string(b)
		}
	}
	s.SecretSet = secret != ""
	return s, secret
}

func (h *handler) getSSO(w http.ResponseWriter, r *http.Request) {
	s, _ := h.ssoConfig(r.Context())
	s.Callback = h.callbackURL()
	s.Works = s.Issuer != "" && h.setting(r.Context(), "sso.works") == s.Issuer
	writeJSON(w, s)
}

func loopbackHost(h string) bool {
	ip := net.ParseIP(h)
	return h == "localhost" || (ip != nil && ip.IsLoopback())
}

func (h *handler) callbackURL() string {
	return strings.TrimSuffix(h.d.PublicURL, "/") + "/auth/sso/callback"
}

func (h *handler) setSSO(w http.ResponseWriter, r *http.Request) {
	var in ssoSettings
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	in.Issuer = strings.TrimSpace(in.Issuer) // as written: providers compare it exactly, a trailing slash too
	if in.Issuer != "" {
		if u, err := url.Parse(in.Issuer); err != nil || (u.Scheme != "https" && !(u.Scheme == "http" && loopbackHost(u.Hostname()))) {
			http.Error(w, "The issuer starts with https://: the client secret is sent there.", http.StatusBadRequest)
			return
		}
		if in.Name == "" || in.ClientID == "" {
			http.Error(w, "A name to show, the issuer and the client ID are all needed.", http.StatusBadRequest)
			return
		}
		if _, secret := h.ssoConfig(ctx); in.ClientSecret == "" && secret == "" {
			http.Error(w, "The client secret is needed.", http.StatusBadRequest)
			return
		}
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := oidc.NewProvider(pctx, in.Issuer)
		cancel()
		if err != nil {
			http.Error(w, "The issuer does not answer as an OpenID Connect provider: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	only := "0"
	if in.Only && in.Issuer != "" {
		only = "1"
	}
	turnsOn := only == "1" && h.setting(ctx, "sso.only") != "1"
	if only == "1" && (turnsOn || h.setting(ctx, "sso.issuer") != in.Issuer) && h.setting(ctx, "sso.works") != in.Issuer {
		http.Error(w, "Sign in with SSO once before turning on Only SSO.", http.StatusBadRequest)
		return
	}
	me, _ := member(r)
	err := h.a.St.Tx(ctx, func(q *db.Queries) error {
		if turnsOn {
			ms, err := q.ListMembers(ctx)
			if err != nil {
				return err
			}
			for _, m := range ms {
				if m.ID != me.ID {
					if err := q.DeleteMemberSessions(ctx, m.ID); err != nil {
						return err
					}
				}
			}
		}
		for k, v := range map[string]string{"sso.name": in.Name, "sso.issuer": in.Issuer, "sso.client_id": in.ClientID, "sso.only": only} {
			if err := q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: v}); err != nil {
				return err
			}
		}
		if in.ClientSecret != "" {
			return vault.SetSecret(ctx, q, h.d.Vault, "sso.client_secret", []byte(in.ClientSecret))
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) ssoInfo(w http.ResponseWriter, r *http.Request) {
	s, _ := h.ssoConfig(r.Context())
	if s.Issuer == "" {
		writeJSON(w, map[string]any{})
		return
	}
	writeJSON(w, map[string]any{"name": s.Name, "only": s.Only})
}

func (h *handler) ssoOnly(ctx context.Context, email string) (why string, glass bool) {
	s, _ := h.ssoConfig(ctx)
	if !s.Only || s.Issuer == "" {
		return "", false
	}
	first, err := h.a.St.Q.FirstOwner(ctx)
	return "This team signs in with " + s.Name + ".", err == nil && strings.EqualFold(first.Email, strings.TrimSpace(email))
}

var ssoClient = &http.Client{Timeout: 10 * time.Second}

var ssoProvider struct {
	sync.Mutex
	issuer string
	p      *oidc.Provider
}

func providerFor(ctx context.Context, issuer string) (*oidc.Provider, error) {
	ssoProvider.Lock()
	defer ssoProvider.Unlock()
	if ssoProvider.p != nil && ssoProvider.issuer == issuer {
		return ssoProvider.p, nil
	}
	p, err := oidc.NewProvider(oidc.ClientContext(context.WithoutCancel(ctx), ssoClient), issuer)
	if err != nil {
		return nil, err
	}
	ssoProvider.issuer, ssoProvider.p = issuer, p
	return p, nil
}

func (h *handler) oauth(ctx context.Context) (*oauth2.Config, *oidc.Provider, error) {
	s, secret := h.ssoConfig(ctx)
	if s.Issuer == "" || secret == "" {
		return nil, nil, errors.New("SSO is not set up")
	}
	p, err := providerFor(ctx, s.Issuer)
	if err != nil {
		return nil, nil, err
	}
	return &oauth2.Config{ClientID: s.ClientID, ClientSecret: secret, Endpoint: p.Endpoint(),
		RedirectURL: h.callbackURL(), Scopes: []string{oidc.ScopeOpenID, "email"}}, p, nil
}

func random() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *handler) ssoStart(w http.ResponseWriter, r *http.Request) {
	cfg, _, err := h.oauth(r.Context())
	if err != nil {
		http.Redirect(w, r, "/sign-in?sso=off", http.StatusFound)
		return
	}
	now := time.Now()
	if last := ssoSwept.Load(); now.Unix()-last >= 60 && ssoSwept.CompareAndSwap(last, now.Unix()) {
		ssoFlows.Range(func(k, v any) bool {
			if now.Sub(v.(ssoFlow).at) > 10*time.Minute {
				ssoFlows.Delete(k)
				ssoCount.Add(-1)
			}
			return true
		})
	}
	if ssoCount.Load() >= 10000 {
		http.Redirect(w, r, "/sign-in?sso=busy", http.StatusFound)
		return
	}
	state, f := random(), ssoFlow{nonce: random(), verifier: oauth2.GenerateVerifier(), at: now}
	ssoFlows.Store(state, f)
	ssoCount.Add(1)
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: state, Path: "/auth/sso", MaxAge: 600, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure(r)})
	http.Redirect(w, r, cfg.AuthCodeURL(state, oidc.Nonce(f.nonce), oauth2.S256ChallengeOption(f.verifier)), http.StatusFound)
}

func (h *handler) ssoCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(why string) { http.Redirect(w, r, "/sign-in?sso="+why, http.StatusFound) }
	state := r.URL.Query().Get("state")
	c, err := r.Cookie(ssoCookie)
	v, found := ssoFlows.LoadAndDelete(state)
	if found {
		ssoCount.Add(-1)
	}
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: "", Path: "/auth/sso", MaxAge: -1})
	if err != nil || !found || c.Value != state || time.Since(v.(ssoFlow).at) > 10*time.Minute {
		fail("expired")
		return
	}
	f := v.(ssoFlow)
	ctx := r.Context()
	cfg, p, err := h.oauth(ctx)
	if err != nil {
		fail("off")
		return
	}
	ctx = oidc.ClientContext(ctx, ssoClient)
	tok, err := cfg.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(f.verifier))
	if err != nil {
		fail("refused")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	id, err := p.Verifier(&oidc.Config{ClientID: cfg.ClientID}).Verify(ctx, raw)
	if err != nil || id.Nonce != f.nonce {
		fail("refused")
		return
	}
	var claims struct {
		Email    string `json:"email"`
		Verified any    `json:"email_verified"` // true, or "true" from some providers; none is not verified
	}
	if id.Claims(&claims) != nil || claims.Email == "" || (claims.Verified != true && claims.Verified != "true") {
		fail("unverified")
		return
	}
	o, err := h.a.St.Q.GetOwnerByEmail(ctx, strings.ToLower(strings.TrimSpace(claims.Email)))
	if err != nil {
		fail("unknown")
		return
	}
	subject := id.Issuer + " " + id.Subject
	if o.SsoSubject != "" && o.SsoSubject != subject {
		fail("other")
		return
	}
	if o.SsoSubject == "" {
		if err := h.a.St.Q.SetOwnerSSOSubject(ctx, db.SetOwnerSSOSubjectParams{SsoSubject: subject, ID: o.ID}); err != nil {
			fail("error")
			return
		}
	}
	if err := h.a.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "sso.works", Value: id.Issuer}); err != nil {
		fail("error")
		return
	}
	if need, err := h.a.NeedsSecondStep(ctx, o); err == nil && need == "code" {
		http.Redirect(w, r, "/sign-in?sso=code#t="+h.tix.add(o.ID, ticketKind(need), h.a.Now()), http.StatusFound) // a fragment never reaches a server
		return
	}
	s, err := h.a.NewSession(ctx, o.ID)
	if err != nil {
		fail("error")
		return
	}
	h.setCookie(w, r, s)
	// A page of the panel's own moves on: a redirect would still be the
	// provider's navigation, and the Strict session cookie would not go with it.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!doctype html><meta http-equiv="refresh" content="0;url=/"><title>Signed in</title><p><a href="/">Continue</a></p>`))
}
