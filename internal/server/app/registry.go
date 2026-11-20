package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

const RegistryKey = "registry."

func RegistryAuth(ctx context.Context, q *db.Queries, v *vault.Vault, host string) (*proto.RegistryAuth, error) {
	b, err := vault.GetSecret(ctx, q, v, RegistryKey+host)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var a proto.RegistryAuth
	return &a, json.Unmarshal(b, &a)
}

func SaveRegistry(ctx context.Context, q *db.Queries, v *vault.Vault, host, username, token string) error {
	b, _ := json.Marshal(proto.RegistryAuth{Username: username, Token: token})
	return vault.SetSecret(ctx, q, v, RegistryKey+host, b)
}

var authParam = regexp.MustCompile(`(\w+)="([^"]*)"`)

func CheckRegistry(ctx context.Context, c *http.Client, host, user, token string) error {
	api := host
	if host == "docker.io" {
		api = "registry-1.docker.io"
	}
	// "ghcr.io@evil.com" would parse as userinfo at evil.com and get the login there.
	if u, err := url.Parse("https://" + api + "/v2/"); err != nil || u.Host != api {
		return fmt.Errorf("%q is not a registry host; save the login again under a name like ghcr.io.", host)
	}
	cc := *c
	cc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	get := func(u string, login bool) (int, string, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return 0, "", err
		}
		if login {
			req.SetBasicAuth(user, token)
		}
		res, err := cc.Do(req)
		if err != nil {
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
			return 0, "", fmt.Errorf("cannot reach %s: %v", req.URL.Host, err)
		}
		res.Body.Close()
		return res.StatusCode, res.Header.Get("WWW-Authenticate"), nil
	}
	answer := func(status int, from string) error {
		switch status {
		case http.StatusOK:
			return nil
		case http.StatusUnauthorized, http.StatusForbidden:
			return errors.New("The registry refused this login.")
		}
		return fmt.Errorf("%s answered %d.", from, status)
	}
	status, challenge, err := get("https://"+api+"/v2/", false)
	if err != nil {
		return err
	} else if status != http.StatusUnauthorized {
		return answer(status, api)
	}
	scheme, params, _ := strings.Cut(challenge, " ")
	switch strings.ToLower(scheme) {
	case "basic":
		status, _, err := get("https://"+api+"/v2/", true)
		if err != nil {
			return err
		}
		return answer(status, api)
	case "bearer":
		p := map[string]string{}
		for _, m := range authParam.FindAllStringSubmatch(params, -1) {
			p[strings.ToLower(m[1])] = m[2]
		}
		realm, err := url.Parse(p["realm"])
		if err != nil || realm.Scheme != "https" || !realmAllowed(api, realm.Host) {
			return fmt.Errorf("The registry asks for the login at %q; Yawl sends it only over https to %s or a host of its own domain.", p["realm"], api)
		}
		if p["service"] != "" {
			q := realm.Query()
			q.Set("service", p["service"])
			realm.RawQuery = q.Encode()
		}
		status, _, err := get(realm.String(), true)
		if err != nil {
			return err
		}
		return answer(status, realm.Host)
	}
	return fmt.Errorf("%s asks for a sign-in Yawl does not know: %q.", api, scheme)
}

// realmAllowed says whether a token realm on realmHost may get apiHost's
// login: the same host, its parent domain, or a sibling under that parent
// (auth.docker.io for registry-1.docker.io). IPs and ports must match.
func realmAllowed(apiHost, realmHost string) bool {
	apiHost, realmHost = strings.ToLower(apiHost), strings.ToLower(realmHost)
	if apiHost == realmHost {
		return true
	}
	aName, aPort := splitPort(apiHost)
	rName, rPort := splitPort(realmHost)
	if aPort != rPort || net.ParseIP(aName) != nil || net.ParseIP(rName) != nil {
		return false
	}
	_, parent, _ := strings.Cut(aName, ".")
	if !strings.Contains(parent, ".") {
		return false
	}
	_, rParent, _ := strings.Cut(rName, ".")
	return rName == parent || rParent == parent
}

func splitPort(h string) (string, string) {
	if name, port, err := net.SplitHostPort(h); err == nil {
		return name, port
	}
	return h, ""
}
