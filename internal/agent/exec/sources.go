package exec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
)

// sourceClient follows no redirect: a token header must not go somewhere else.
var sourceClient = &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: guarded.DialContext},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// guarded dials a link a member typed (a secret manager, a dump to import).
// The check runs on the address really dialed, so a redirect or a name that
// answers otherwise later cannot get past what the panel checked.
var guarded = &net.Dialer{Timeout: 30 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	if ip := ap.Addr().Unmap(); ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip == awsIMDS6 || (ip.IsLoopback() && !loopbackOK) {
		return fmt.Errorf("%s is this server or a cloud metadata address; Yawl does not fetch from it", ip)
	}
	return nil
}}

// awsIMDS6 is AWS's metadata service over IPv6: a private (ULA) address, so
// the private-range allowance would let it through.
var awsIMDS6 = netip.MustParseAddr("fd00:ec2::254")

var loopbackOK bool

func getJSON(req *http.Request, out any) error {
	res, err := sourceClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("%s: %v", req.URL.Host, err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return fmt.Errorf("%s answered %s: %s", req.URL.Host, res.Status, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
}

func (d *Dispatcher) fetchSecrets(s proto.SecretSource, token string) (map[string]string, error) {
	out, err := d.fetchFrom(s, token)
	if d.Keys != nil {
		m := d.Keys.Masker()
		for _, v := range out {
			m.Add(v)
		}
	}
	for k := range out { // a key with = or a newline would set another variable
		if err == nil && !varName.MatchString(k) {
			err = fmt.Errorf("%s: the key %q is not a variable name (letters, digits and _)", s.Provider, k)
		}
	}
	return out, err
}

var varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (d *Dispatcher) fetchFrom(s proto.SecretSource, token string) (map[string]string, error) {
	out := map[string]string{}
	switch s.Provider {
	case "doppler":
		base := d.DopplerURL
		if base == "" {
			base = "https://api.doppler.com"
		}
		req, _ := http.NewRequest("GET", base+"/v3/configs/config/secrets/download?format=json", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		return out, getJSON(req, &out)
	case "vault":
		req, err := http.NewRequest("GET", strings.TrimSuffix(s.URL, "/")+"/v1/"+strings.TrimPrefix(s.Path, "/"), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Vault-Token", token)
		var v struct {
			Data struct{ Data map[string]json.RawMessage } `json:"data"`
		}
		if err := getJSON(req, &v); err != nil {
			return nil, err
		}
		for k, raw := range v.Data.Data {
			var str string
			if json.Unmarshal(raw, &str) != nil {
				str = string(raw)
			}
			out[k] = str
		}
		return out, nil
	case "infisical":
		id, secret, ok := strings.Cut(token, ":")
		if !ok {
			return nil, errors.New("the token is clientId:clientSecret of a machine identity")
		}
		if d.Keys != nil {
			d.Keys.Masker().Add(secret)
		}
		base := strings.TrimSuffix(s.URL, "/")
		body, _ := json.Marshal(map[string]string{"clientId": id, "clientSecret": secret})
		login, _ := http.NewRequest("POST", base+"/api/v1/auth/universal-auth/login", bytes.NewReader(body))
		login.Header.Set("Content-Type", "application/json")
		var l struct {
			AccessToken string `json:"accessToken"`
		}
		if err := getJSON(login, &l); err != nil {
			return nil, err
		}
		if d.Keys != nil {
			d.Keys.Masker().Add(l.AccessToken)
		}
		path := s.Path
		if path == "" {
			path = "/"
		}
		q := url.Values{"workspaceId": {s.Workspace}, "environment": {s.Env}, "secretPath": {path}}
		req, _ := http.NewRequest("GET", base+"/api/v3/secrets/raw?"+q.Encode(), nil)
		req.Header.Set("Authorization", "Bearer "+l.AccessToken)
		var r struct {
			Secrets []struct{ SecretKey, SecretValue string } `json:"secrets"`
		}
		if err := getJSON(req, &r); err != nil {
			return nil, err
		}
		for _, x := range r.Secrets {
			out[x.SecretKey] = x.SecretValue
		}
		return out, nil
	}
	return nil, fmt.Errorf("no secret manager %q", s.Provider)
}
