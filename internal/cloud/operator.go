package cloud

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/seal"
)

const CellLimits = "servers=3,members=5,services=20"

type Operator struct {
	Base, Token, Project, Server string
	HTTP                         *http.Client
}

type Cell struct {
	Name, Image, Domain, PublicURL, ClaimHash, InstallURL, Releases string
	BillingURL, BillingToken                                        string
	Server                                                          string
	RefusedHostIP                                                   string
	Resume                                                          bool
}

const cellService = "panel"

func backupAt(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	m := h.Sum32() % 360
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

func (o Operator) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	switch v := in.(type) {
	case nil:
	case string:
		body = strings.NewReader(v)
	default:
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(o.Base, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+o.Token)
	req.Header.Set("Content-Type", "application/json")
	hc := o.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("the operator panel: %w", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		return &OperatorError{res.StatusCode, strings.TrimSpace(string(b))}
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

type OperatorError struct {
	Status int
	Text   string
}

func (e *OperatorError) Error() string {
	return fmt.Sprintf("the operator panel answered %d: %s", e.Status, e.Text)
}

func (o Operator) svc(name string) string {
	return "/api/services/" + o.Project + "/" + url.PathEscape(name) + "/" + cellService
}

func (o Operator) Ensure(ctx context.Context) error {
	return o.do(ctx, "GET", "/api/projects", nil, nil)
}

func (o Operator) exists(ctx context.Context, name string) (bool, error) {
	var ps []struct {
		Name string
		Envs []struct{ Name string } `json:"envs"`
	}
	if err := o.do(ctx, "GET", "/api/projects", nil, &ps); err != nil {
		return false, err
	}
	for _, p := range ps {
		for _, e := range p.Envs {
			if p.Name == o.Project && e.Name == name {
				return true, nil
			}
		}
	}
	return false, nil
}

func (o Operator) cellYAML(c Cell) string {
	domains := ""
	if c.Domain != "" {
		domains = "    domains: [" + c.Domain + "]\n"
	}
	return fmt.Sprintf("project: %s\nenvironment: %s\nserver: %s\nservices:\n  - name: %s\n    kind: web\n    image: %s\n    port: 7000\n"+
		"    health_path: /api/session\n    auto_deploy: false\n    memory: 256MB\n    backup_at: \"%s\"\n    volumes: [{name: data, path: /data}]\n%s",
		o.Project, c.Name, cmp.Or(c.Server, o.Server), cellService, c.Image, backupAt(c.Name), domains)
}

func (o Operator) Create(ctx context.Context, c Cell) error {
	if !c.Resume {
		if there, err := o.exists(ctx, c.Name); err != nil {
			return err
		} else if there {
			return fmt.Errorf("%s is taken", c.Name)
		}
	}
	if err := o.do(ctx, "POST", "/api/apply", o.cellYAML(c), nil); err != nil {
		return err
	}
	var keys struct {
		Agent    string `json:"agent_pubkey"`
		Recovery string `json:"recovery_pubkey"`
	}
	if err := o.do(ctx, "GET", "/api/environments/"+o.Project+"/"+url.PathEscape(c.Name)+"/keys", nil, &keys); err != nil {
		return err
	}
	agent, err1 := seal.ParsePublicKey(keys.Agent)
	rec, err2 := seal.ParsePublicKey(keys.Recovery)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("the operator's server has no keys to seal to: %v %v", err1, err2)
	}
	env := map[string]string{"YAWL_PUBLIC_URL": c.PublicURL, "YAWL_MANAGED": "1", "YAWL_CLAIM_HASH": c.ClaimHash,
		"YAWL_INSTALL_URL": c.InstallURL, "YAWL_RELEASES": c.Releases, "YAWL_LIMITS": CellLimits, "YAWL_BILLING_URL": c.BillingURL, "YAWL_BILLING_TOKEN": c.BillingToken}
	if c.RefusedHostIP != "" {
		env["YAWL_HOST_ADDRS"] = c.RefusedHostIP
	}
	for k, v := range env {
		a, err := seal.Seal(agent, []byte(v))
		if err != nil {
			return err
		}
		r, err := seal.Seal(rec, []byte(v))
		if err != nil {
			return err
		}
		if err := o.do(ctx, "PUT", o.svc(c.Name)+"/env/"+k, map[string]string{"agent_box": a, "recovery_box": r}, nil); err != nil {
			return err
		}
	}
	return o.do(ctx, "POST", o.svc(c.Name)+"/deploys", map[string]string{}, nil)
}

func (o Operator) Delete(ctx context.Context, name string) error {
	return o.do(ctx, "DELETE", "/api/environments/"+o.Project+"/"+url.PathEscape(name), map[string]string{"confirm": name}, nil)
}

func (o Operator) State(ctx context.Context, name string) (string, error) {
	var ds []struct{ State string }
	if err := o.do(ctx, "GET", o.svc(name)+"/deploys", nil, &ds); err != nil {
		return "", err
	}
	if len(ds) == 0 {
		return "", nil
	}
	return ds[0].State, nil
}

func (o Operator) SetImage(ctx context.Context, name, image string) error {
	var v struct {
		Form map[string]any `json:"form"`
	}
	if err := o.do(ctx, "GET", o.svc(name), nil, &v); err != nil {
		return err
	}
	if v.Form == nil {
		return fmt.Errorf("the operator panel has no form for %s", name)
	}
	v.Form["image"] = image
	if err := o.do(ctx, "POST", "/api/environments/"+o.Project+"/"+url.PathEscape(name)+"/services", v.Form, nil); err != nil {
		return err
	}
	return o.do(ctx, "POST", o.svc(name)+"/deploys", map[string]string{}, nil)
}
