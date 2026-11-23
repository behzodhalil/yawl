package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/spec"
)

type App struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	PEM           string `json:"pem"`
	WebhookSecret string `json:"webhook_secret"`
}

type Client struct {
	API, Web string
	HTTP     *http.Client
	Now      func() time.Time
}

var ErrNotInstalled = errors.New("app not installed on repository")

func Manifest(publicURL, name string) ([]byte, error) {
	u := strings.TrimRight(publicURL, "/")
	return json.Marshal(map[string]any{
		"name":                name,
		"url":                 u,
		"hook_attributes":     map[string]any{"url": u + "/github/webhook"},
		"redirect_url":        u + "/github/callback",
		"setup_url":           u + "/github/installed",
		"public":              false,
		"default_events":      []string{"push", "pull_request"},
		"default_permissions": map[string]string{"contents": "read", "metadata": "read", "pull_requests": "write", "statuses": "write"},
	})
}

func (c *Client) Convert(ctx context.Context, code string) (App, error) {
	var a App
	err := c.call(ctx, "POST", "/app-manifests/"+url.PathEscape(code)+"/conversions", "", nil, &a)
	if err == nil && (a.ID == 0 || a.PEM == "" || a.WebhookSecret == "") {
		err = errors.New("github: the App GitHub returned is incomplete")
	}
	return a, err
}

func JWT(a App, now time.Time) (string, error) {
	blk, _ := pem.Decode([]byte(a.PEM))
	if blk == nil {
		return "", errors.New("github: the App key is not PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(blk.Bytes)
	if err != nil {
		k8, err8 := x509.ParsePKCS8PrivateKey(blk.Bytes)
		var ok bool
		if key, ok = k8.(*rsa.PrivateKey); err8 != nil || !ok {
			return "", fmt.Errorf("github: the App key: %v", err)
		}
	}
	enc := base64.RawURLEncoding
	hdr := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	iat := now.Unix() - 60 // GitHub's advice: allow for clock drift
	claims, _ := json.Marshal(map[string]any{"iat": iat, "exp": iat + 600, "iss": strconv.FormatInt(a.ID, 10)})
	signing := hdr + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

func (c *Client) InstallationToken(ctx context.Context, a App, fullName string) (string, error) {
	return c.token(ctx, a, fullName, map[string]string{"contents": "read"})
}

func (c *Client) token(ctx context.Context, a App, fullName string, perms map[string]string) (string, error) {
	jwt, err := JWT(a, c.Now())
	if err != nil {
		return "", err
	}
	var inst struct{ ID int64 }
	if err := c.call(ctx, "GET", "/repos/"+fullName+"/installation", jwt, nil, &inst); err != nil {
		return "", err
	}
	_, repo, _ := strings.Cut(fullName, "/")
	body, _ := json.Marshal(map[string]any{"repositories": []string{repo}, "permissions": perms})
	var out struct{ Token string }
	if err := c.call(ctx, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", inst.ID), jwt, body, &out); err != nil {
		return "", err
	}
	return out.Token, nil
}

func (c *Client) call(ctx context.Context, method, path, jwt string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.API+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode == http.StatusNotFound && strings.HasSuffix(path, "/installation") {
		return ErrNotInstalled
	}
	if res.StatusCode/100 != 2 {
		// a rate limit (403 or 429 with no calls left, or a Retry-After) is a wait, not a no
		limited := res.StatusCode == http.StatusTooManyRequests || res.Header.Get("X-RateLimit-Remaining") == "0" || res.Header.Get("Retry-After") != ""
		return &APIError{Status: res.StatusCode, limited: limited, msg: fmt.Sprintf("github: %s %s: %d %s", method, path, res.StatusCode, strings.TrimSpace(string(b[:min(len(b), 200)])))}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

type APIError struct {
	Status      int
	msg         string
	unconnected bool
	limited     bool
}

func (e *APIError) Error() string { return e.msg }

func (e *APIError) Unconnected() bool { return e.unconnected }

// Refused is GitHub saying no rather than failing: no permission, no such
// repository, or a permission the installation was not granted (422).
func (e *APIError) Refused() bool {
	return !e.limited && (e.Status == 401 || e.Status == 403 || e.Status == 404 || e.Status == 422)
}

type Statuses struct {
	C *Client
	Q *db.Queries
	V *vault.Vault
}

func (s Statuses) Status(ctx context.Context, fullName, sha, state, context_, description, target string) error {
	a, connected, err := Load(ctx, s.Q, s.V)
	if err != nil {
		return err
	}
	if !connected {
		return &APIError{Status: 404, msg: "GitHub is not connected", unconnected: true}
	}
	tok, err := s.C.token(ctx, a, fullName, map[string]string{"statuses": "write"})
	if errors.Is(err, ErrNotInstalled) {
		return &APIError{Status: 404, msg: "The Yawl GitHub App is not installed on " + fullName}
	} else if err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]string{"state": state, "context": context_, "description": description, "target_url": target})
	return s.C.call(ctx, "POST", fmt.Sprintf("/repos/%s/statuses/%s", fullName, sha), tok, b, nil)
}

func Verify(secret string, body []byte, header string) bool {
	got, ok := strings.CutPrefix(header, "sha256=")
	sig, err := hex.DecodeString(got)
	if !ok || err != nil || secret == "" {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hmac.Equal(sig, m.Sum(nil))
}

const setting = "github.app"

func Load(ctx context.Context, q *db.Queries, v *vault.Vault) (App, bool, error) {
	b, err := vault.GetSecret(ctx, q, v, setting)
	if errors.Is(err, sql.ErrNoRows) {
		return App{}, false, nil
	} else if err != nil {
		return App{}, false, err
	}
	var a App
	return a, true, json.Unmarshal(b, &a)
}

func Save(ctx context.Context, q *db.Queries, v *vault.Vault, a App) error {
	b, _ := json.Marshal(a)
	return vault.SetSecret(ctx, q, v, setting, b)
}

type Tokens struct {
	C *Client
	Q *db.Queries
	V *vault.Vault
}

func (t Tokens) Token(ctx context.Context, repo string) (string, error) {
	name, ok := spec.GitHubRepo(repo)
	if !ok {
		return "", nil
	}
	a, connected, err := Load(ctx, t.Q, t.V)
	if err != nil || !connected {
		return "", err
	}
	tok, err := t.C.InstallationToken(ctx, a, name)
	if errors.Is(err, ErrNotInstalled) {
		return "", fmt.Errorf("The Yawl GitHub App is not installed on %s. Install it: %s/apps/%s/installations/new", name, t.C.Web, a.Slug)
	}
	return tok, err
}

type Comments struct {
	C *Client
	Q *db.Queries
	V *vault.Vault
}

func (c Comments) Comment(ctx context.Context, fullName string, number, id int64, body string) (int64, error) {
	a, connected, err := Load(ctx, c.Q, c.V)
	if err != nil {
		return 0, err
	}
	if !connected {
		return 0, errors.New("GitHub is not connected")
	}
	tok, err := c.C.token(ctx, a, fullName, map[string]string{"pull_requests": "write"})
	if err != nil {
		return 0, err
	}
	b, _ := json.Marshal(map[string]string{"body": body})
	var out struct{ ID int64 }
	if id == 0 {
		err = c.C.call(ctx, "POST", fmt.Sprintf("/repos/%s/issues/%d/comments", fullName, number), tok, b, &out)
	} else {
		err = c.C.call(ctx, "PATCH", fmt.Sprintf("/repos/%s/issues/comments/%d", fullName, id), tok, b, &out)
	}
	return out.ID, err
}
