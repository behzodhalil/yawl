package gitlab

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
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

// client follows no redirect: Go keeps PRIVATE-TOKEN on one to another host,
// such as an SSO proxy's login page (review I1).
var client = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

type Host struct{ Name, Token, Secret string }

const hostsKey = "gitlab.hosts"

func names(ctx context.Context, q *db.Queries) []string {
	var hs []string
	if v, err := q.GetSetting(ctx, hostsKey); err == nil {
		json.Unmarshal([]byte(v), &hs)
	}
	return hs
}

func Load(ctx context.Context, q *db.Queries, v *vault.Vault) ([]Host, error) {
	var out []Host
	for _, n := range names(ctx, q) {
		tok, err1 := vault.GetSecret(ctx, q, v, "gitlab."+n+".token")
		sec, err2 := vault.GetSecret(ctx, q, v, "gitlab."+n+".secret")
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
		return "", fmt.Errorf("%q: give the host alone, like gitlab.com or gitlab.acme.uz", host)
	}
	if err := Check(ctx, host, token); err != nil {
		return "", err
	}
	secret := ""
	if _, err := vault.GetSecret(ctx, q, v, "gitlab."+host+".secret"); err != nil {
		b := make([]byte, 32)
		rand.Read(b)
		secret = hex.EncodeToString(b)
		if err := vault.SetSecret(ctx, q, v, "gitlab."+host+".secret", []byte(secret)); err != nil {
			return "", err
		}
	}
	if err := vault.SetSecret(ctx, q, v, "gitlab."+host+".token", []byte(token)); err != nil {
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
	return call(ctx, host, token, "GET", "/api/v4/user", nil, nil)
}

func Forget(ctx context.Context, q *db.Queries, host string) error {
	hs := slices.DeleteFunc(names(ctx, q), func(h string) bool { return h == host })
	js, _ := json.Marshal(hs)
	return errors.Join(q.SetSetting(ctx, db.SetSettingParams{Key: hostsKey, Value: string(js)}),
		q.DeleteSettingKey(ctx, "gitlab."+host+".token"), q.DeleteSettingKey(ctx, "gitlab."+host+".secret"))
}

func Match(hosts []Host, header string) (Host, bool) {
	var found Host
	ok := false
	for _, h := range hosts {
		if header != "" && subtle.ConstantTimeCompare([]byte(h.Secret), []byte(header)) == 1 {
			found, ok = h, true
		}
	}
	return found, ok
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
	req.Header.Set("PRIVATE-TOKEN", token)
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

func (e *APIError) Error() string { return fmt.Sprintf("GitLab refused the token: %d", e.Status) }

func (e *APIError) Refused() bool { return e.Status == 401 || e.Status == 403 || e.Status == 404 }

type Statuses struct {
	Q *db.Queries
	V *vault.Vault
}

func (s Statuses) Status(ctx context.Context, key, sha, state, name, description, target string) error {
	host, path, ok := split(key)
	if !ok {
		return &APIError{Status: 404, unconnected: true}
	}
	hosts, err := Load(ctx, s.Q, s.V)
	if err != nil {
		return err
	}
	if w, ok := map[string]string{"failure": "failed", "error": "canceled"}[state]; ok {
		state = w
	}
	for _, h := range hosts {
		if h.Name == host {
			return call(ctx, host, h.Token, "POST", fmt.Sprintf("/api/v4/projects/%s/statuses/%s", url.PathEscape(path), sha),
				map[string]string{"state": state, "name": name, "description": description, "target_url": target}, nil)
		}
	}
	return &APIError{Status: 404, unconnected: true}
}

func split(key string) (string, string, bool) {
	host, path, ok := strings.Cut(key, "/")
	return host, path, ok && strings.Contains(host, ".")
}

type Tokens struct {
	Q *db.Queries
	V *vault.Vault
}

func (t Tokens) Token(ctx context.Context, repo string) (string, error) {
	if host, path, ok := strings.Cut(strings.TrimPrefix(repo, "git@"), ":"); ok && strings.HasPrefix(repo, "git@") {
		hosts, err := Load(ctx, t.Q, t.V)
		if err != nil {
			return "", err
		}
		for _, h := range hosts {
			if h.Name == strings.ToLower(host) {
				return "", fmt.Errorf("%s is an SSH address; Yawl clones from %s with its token over https: set repo to https://%s/%s", repo, h.Name, h.Name, strings.TrimSuffix(path, ".git"))
			}
		}
		return "", nil
	}
	key, ok := spec.RepoKey(repo)
	if !ok {
		return "", nil
	}
	host, _, ok := split(key)
	if !ok {
		return "", nil
	}
	hosts, err := Load(ctx, t.Q, t.V)
	if err != nil {
		return "", err
	}
	for _, h := range hosts {
		if h.Name == host {
			return h.Token, nil
		}
	}
	return "", nil
}

type Comments struct {
	Q *db.Queries
	V *vault.Vault
}

func (c Comments) Comment(ctx context.Context, key string, iid, id int64, body string) (int64, error) {
	host, path, ok := split(key)
	if !ok {
		return 0, fmt.Errorf("%s is not a GitLab project", key)
	}
	hosts, err := Load(ctx, c.Q, c.V)
	if err != nil {
		return 0, err
	}
	for _, h := range hosts {
		if h.Name != host {
			continue
		}
		var out struct{ ID int64 }
		p := fmt.Sprintf("/api/v4/projects/%s/merge_requests/%d/notes", url.PathEscape(path), iid)
		if id == 0 {
			err = call(ctx, host, h.Token, "POST", p, map[string]string{"body": body}, &out)
		} else {
			err = call(ctx, host, h.Token, "PUT", fmt.Sprintf("%s/%d", p, id), map[string]string{"body": body}, &out)
		}
		return out.ID, err
	}
	return 0, fmt.Errorf("%s is not connected", host)
}
