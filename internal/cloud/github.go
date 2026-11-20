package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type ghUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Email string `json:"email"`
}

func (f *front) authorizeURL(state string) string {
	q := url.Values{"client_id": {f.cfg.GitHubID}, "redirect_uri": {f.cfg.PublicURL + "/auth/github/callback"},
		"scope": {"read:user user:email"}, "state": {state}}
	return strings.TrimRight(f.cfg.GitHubWeb, "/") + "/login/oauth/authorize?" + q.Encode()
}

func (f *front) githubUser(ctx context.Context, code string) (ghUser, error) {
	form := url.Values{"client_id": {f.cfg.GitHubID}, "client_secret": {f.cfg.GitHubSecret}, "code": {code}}
	req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(f.cfg.GitHubWeb, "/")+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := f.json(req, &tok); err != nil {
		return ghUser{}, err
	}
	if tok.AccessToken == "" {
		return ghUser{}, fmt.Errorf("GitHub did not sign you in (%s)", tok.Error)
	}
	req, _ = http.NewRequestWithContext(ctx, "GET", strings.TrimRight(f.cfg.GitHubAPI, "/")+"/user", nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	var u ghUser
	if err := f.json(req, &u); err != nil {
		return ghUser{}, err
	}
	if u.ID == 0 {
		return ghUser{}, fmt.Errorf("GitHub sent no account")
	}
	return u, nil
}

func (f *front) json(req *http.Request, out any) error {
	res, err := f.http.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub: %w", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("GitHub answered %d", res.StatusCode)
	}
	return json.Unmarshal(b, out)
}
