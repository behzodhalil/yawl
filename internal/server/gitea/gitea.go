package gitea

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
	"github.com/behzodhalil/yawl/internal/spec"
)

var APIBase = func(host string) string { return "https://" + host }

var hostRe = regexp.MustCompile(`^[a-z0-9.-]+\.[a-z]{2,}(:\d{1,5})?$`)

var client = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

type Host struct{ Name, Token, Secret string }

const hostsKey = "gitea.hosts"

func names(ctx context.Context, q *db.Queries) []string {
	var hs []string
	if v, err := q.GetSetting(ctx, hostsKey); err == nil {
		json.Unmarshal([]byte(v), &hs)
	}
	return hs
}

func Has(ctx context.Context, q *db.Queries, host string) bool {
	return slices.Contains(names(ctx, q), host)
}

func Load(ctx context.Context, q *db.Queries, v *vault.Vault) ([]Host, error) {
	var out []Host
	for _, n := range names(ctx, q) {
		tok, err1 := vault.GetSecret(ctx, q, v, "gitea."+n+".token")
		sec, err2 := vault.GetSecret(ctx, q, v, "gitea."+n+".secret")
		if err := errors.Join(err1, err2); err != nil {
			return nil, err
		}
		out = append(out, Host{Name: n, Token: string(tok), Secret: string(sec)})
	}
	return out, nil
}

func Connect(ctx context.Context, q *db.Queries, v *vault.Vault, host, token string) (string, error) {
	host = strings.ToLower(host)
	if !hostRe.MatchString(host) {
		return "", fmt.Errorf("%q: give the host alone, like codeberg.org or git.acme.uz", host)
	}
	if err := Check(ctx, host, token); err != nil {
		return "", err
	}
	secret := ""
	if _, err := vault.GetSecret(ctx, q, v, "gitea."+host+".secret"); err != nil {
		b := make([]byte, 32)
		rand.Read(b)
		secret = hex.EncodeToString(b)
		if err := vault.SetSecret(ctx, q, v, "gitea."+host+".secret", []byte(secret)); err != nil {
			return "", err
		}
	}
	if err := vault.SetSecret(ctx, q, v, "gitea."+host+".token", []byte(token)); err != nil {
		return "", err
	}
	hs := names(ctx, q)
	if !slices.Contains(hs, host) {
		hs = append(hs, host)
	}
	js, _ := json.Marshal(hs)
	return secret, q.SetSetting(ctx, db.SetSettingParams{Key: hostsKey, Value: string(js)})
}

func Check(ctx context.Context, host, token string) error {
	return call(ctx, host, token, "GET", "/api/v1/user", nil, nil)
}

func Forget(ctx context.Context, q *db.Queries, host string) error {
	hs := slices.DeleteFunc(names(ctx, q), func(h string) bool { return h == host })
	js, _ := json.Marshal(hs)
	return errors.Join(q.SetSetting(ctx, db.SetSettingParams{Key: hostsKey, Value: string(js)}),
		q.DeleteSettingKey(ctx, "gitea."+host+".token"), q.DeleteSettingKey(ctx, "gitea."+host+".secret"))
}

func Verify(hosts []Host, host string, body []byte, sig string) (Host, bool) {
	want, err := hex.DecodeString(sig)
	if err != nil {
		return Host{}, false
	}
	for _, h := range hosts {
		if h.Name != host {
			continue
		}
		mac := hmac.New(sha256.New, []byte(h.Secret))
		mac.Write(body)
		if hmac.Equal(mac.Sum(nil), want) {
			return h, true
		}
	}
	return Host{}, false
}

func call(ctx context.Context, host, token, method, path string, body, out any) error {
	var in []byte
	if body != nil {
		in, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, APIBase(host)+path, bytes.NewReader(in))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("cannot reach %s: %v", host, err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return &APIError{Status: res.StatusCode}
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}

type APIError struct {
	Status      int
	unconnected bool
}

func (e *APIError) Unconnected() bool { return e.unconnected }
func (e *APIError) Error() string     { return fmt.Sprintf("Gitea refused the token: %d", e.Status) }
func (e *APIError) Refused() bool     { return e.Status == 401 || e.Status == 403 || e.Status == 404 }

func split(key string) (string, string, bool) {
	host, path, ok := strings.Cut(key, "/")
	return host, path, ok && strings.Contains(host, ".")
}

func tokenFor(ctx context.Context, q *db.Queries, v *vault.Vault, host string) (string, bool, error) {
	hosts, err := Load(ctx, q, v)
	if err != nil {
		return "", false, err
	}
	for _, h := range hosts {
		if h.Name == host {
			return h.Token, true, nil
		}
	}
	return "", false, nil
}

type Statuses struct {
	Q *db.Queries
	V *vault.Vault
}

func (s Statuses) Status(ctx context.Context, key, sha, state, name, description, target string) error {
	host, path, ok := split(key)
	if !ok {
		return &APIError{Status: 404, unconnected: true}
	}
	tok, found, err := tokenFor(ctx, s.Q, s.V, host)
	if err != nil {
		return err
	}
	if !found {
		return &APIError{Status: 404, unconnected: true}
	}
	return call(ctx, host, tok, "POST", fmt.Sprintf("/api/v1/repos/%s/statuses/%s", path, url.PathEscape(sha)),
		map[string]string{"state": state, "context": name, "description": description, "target_url": target}, nil)
}

type Tokens struct {
	Q *db.Queries
	V *vault.Vault
}

func (t Tokens) Token(ctx context.Context, repo string) (string, error) {
	key, ok := spec.RepoKey(repo)
	if !ok {
		return "", nil
	}
	host, _, ok := split(key)
	if !ok {
		return "", nil
	}
	tok, _, err := tokenFor(ctx, t.Q, t.V, host)
	return tok, err
}

type Comments struct {
	Q *db.Queries
	V *vault.Vault
}

func (c Comments) Comment(ctx context.Context, key string, number, id int64, body string) (int64, error) {
	host, path, ok := split(key)
	if !ok {
		return 0, fmt.Errorf("%s is not a Gitea repository", key)
	}
	tok, found, err := tokenFor(ctx, c.Q, c.V, host)
	if err != nil || !found {
		return 0, errors.Join(err, fmt.Errorf("%s is not connected", host))
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if id == 0 {
		err = call(ctx, host, tok, "POST", fmt.Sprintf("/api/v1/repos/%s/issues/%d/comments", path, number), map[string]string{"body": body}, &out)
	} else {
		err = call(ctx, host, tok, "PATCH", fmt.Sprintf("/api/v1/repos/%s/issues/comments/%d", path, id), map[string]string{"body": body}, &out)
		out.ID = id
	}
	return out.ID, err
}
