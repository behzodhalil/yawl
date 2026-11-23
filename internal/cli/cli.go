package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/behzodhalil/yawl/internal/spec"
)

var (
	stdout io.Writer = os.Stdout
	stdin  io.Reader = os.Stdin
)

func SetIO(out io.Writer, in io.Reader) { stdout, stdin = out, in }

type config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func configPath() string {
	if p := os.Getenv("YAWL_CONFIG"); p != "" {
		return p
	}
	dir, _ := os.UserConfigDir()
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, ".config") // the same place on macOS and Linux
	}
	return filepath.Join(dir, "yawl", "config.json")
}

var ErrUnknown = errors.New("unknown command")

func Run(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "login":
		return login(args)
	case "apply":
		return apply(ctx, args)
	case "export":
		return exportCmd(ctx, args)
	case "deploy":
		return deployCmd(ctx, args)
	case "status":
		return status(ctx)
	case "env":
		return env(ctx, args)
	case "recovery-key":
		return recoveryKey(ctx, args)
	case "github":
		return githubCmd(ctx, args)
	case "mcp":
		return mcp(ctx)
	case "compose":
		return composeCmd(ctx, args)
	case "import":
		return importCmd(ctx, args)
	case "db":
		return dbCmd(ctx, args)
	case "restart", "stop", "start":
		return powerCmd(ctx, cmd, args)
	case "rollback":
		return rollbackCmd(ctx, args)
	case "domains":
		return domainsCmd(ctx, args)
	case "logs":
		return logsCmd(ctx, args)
	case "update":
		return updateCmd(ctx, args)
	}
	return fmt.Errorf("%w %q", ErrUnknown, cmd)
}

func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos, args = append(pos, args[0]), args[1:]
	}
}

func login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	token := fs.String("token", "", "API token from `yawl server token create` (default: approve in the browser)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: yawl login <panel url> [--token <token>]")
	}
	url := strings.TrimRight(pos[0], "/")
	if *token == "" {
		if *token, err = browserLogin(url); err != nil {
			return err
		}
	}
	b, _ := json.MarshalIndent(config{URL: url, Token: *token}, "", "  ")
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Saved to", p)
	return nil
}

var checkRe = regexp.MustCompile(`^[A-Z2-7]{8}$`)

func browserLogin(url string) (string, error) {
	raw := make([]byte, 10)
	rand.Read(raw)
	code := make([]byte, 10)
	for i, b := range raw {
		code[i] = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"[b%32]
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "this computer"
	}
	body, _ := json.Marshal(map[string]string{"code": string(code), "host": host})
	hc := &http.Client{Timeout: 15 * time.Second}
	res, err := hc.Post(url+"/api/cli-login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	why, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("the panel refused the login (%s): %s", res.Status, strings.TrimSpace(string(why)))
	}
	fmt.Fprintf(stdout, "Open %s/cli-login#%s and approve this computer.\n", url, code)
	in := bufio.NewReader(stdin)
	for {
		fmt.Fprint(stdout, "Then type the code the browser shows: ")
		line, err := in.ReadString('\n')
		check := strings.ToUpper(strings.TrimSpace(line))
		if !checkRe.MatchString(check) {
			if err != nil {
				return "", errors.New("No code typed; run yawl login again.")
			}
			fmt.Fprintln(stdout, "That code is 8 letters and digits.")
			continue
		}
		res, err := hc.Get(url + "/api/cli-login/" + string(code) + "?check=" + check)
		if err != nil {
			return "", err
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		res.Body.Close()
		switch res.StatusCode {
		case http.StatusOK:
			var out struct{ Token string }
			if err := json.Unmarshal(b, &out); err != nil || out.Token == "" {
				return "", errors.New("the panel answered without a token")
			}
			return out.Token, nil
		case http.StatusAccepted:
			fmt.Fprintln(stdout, "Not approved yet.")
		default:
			return "", errors.New(strings.TrimSpace(string(b)))
		}
	}
}

type client struct{ cfg config }

func newClient() (*client, error) {
	b, err := os.ReadFile(configPath())
	if err != nil {
		return nil, errors.New("not logged in; run `yawl login <server url> --token <token>`")
	}
	c := &client{}
	return c, json.Unmarshal(b, &c.cfg)
}

func (c *client) do(ctx context.Context, method, path string, body []byte, out any) error {
	return c.send(ctx, method, path, bytes.NewReader(body), out)
}

func (c *client) send(ctx context.Context, method, path string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.URL+path, body)
	if err != nil {
		return err
	}
	if f, ok := body.(*os.File); ok { // a file would go chunked, and the panel checks its disk against the length (run-3 L11)
		if st, err := f.Stat(); err == nil && st.Mode().IsRegular() {
			req.ContentLength = st.Size()
		}
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return errors.New(strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}

type change struct{ Op, Kind, Name, Detail string }

func (c change) String() string {
	s := fmt.Sprintf("%-8s%-9s%s", c.Op, c.Kind, c.Name)
	if c.Detail != "" {
		s += "  " + c.Detail
	}
	return s
}

func apply(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	file := fs.String("f", "yawl.yaml", "spec file")
	yes := fs.Bool("yes", false, "apply without asking")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	body, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	if _, err := spec.Parse(body); err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	var changes []change
	if err := c.do(ctx, "POST", "/api/plan", body, &changes); err != nil {
		return err
	}
	pending := 0
	for _, ch := range changes {
		fmt.Fprintln(stdout, ch)
		if ch.Op != "unchanged" && ch.Op != "warn" {
			pending++
		}
	}
	if pending == 0 {
		fmt.Fprintln(stdout, "Nothing to change.")
		return nil
	}
	if !*yes {
		fmt.Fprint(stdout, "Apply? [y/N] ")
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(stdout, "Nothing applied.")
			return nil
		}
	}
	if err := c.do(ctx, "POST", "/api/apply", body, &changes); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Applied %d change(s).\n", pending)
	return nil
}

type deployView struct {
	ID, Service, State, Cause, Commit string
	Steps                             []struct{ Step, State, Output string }
}

func deployCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	file := fs.String("f", "yawl.yaml", "spec file naming the project and environment")
	commit := fs.String("commit", "", "commit to deploy (default: the branch head)")
	noCache := fs.Bool("no-cache", false, "build every step again")
	tag := fs.String("tag", "", "deploy every service with this tag, one after another")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if (*tag == "") == (len(pos) != 1) {
		return errors.New("usage: yawl deploy <service> [--commit sha], or yawl deploy --tag <tag>")
	}
	if *tag != "" && *commit != "" {
		return errors.New("--commit names one service's commit; deploy --tag takes each branch's head")
	}
	b, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	f, err := spec.Parse(b)
	if err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	names := pos
	if *tag != "" {
		var rows []struct {
			Project, Env, Service string
			Tags                  []string
		}
		if err := c.do(ctx, "GET", "/api/status", nil, &rows); err != nil {
			return err
		}
		tagged := map[string]bool{}
		for _, r := range rows {
			if r.Project == f.Project && r.Env == f.Environment && slices.Contains(r.Tags, *tag) {
				tagged[r.Service] = true
			}
		}
		for _, s := range f.Services {
			if tagged[s.Name] {
				names, tagged[s.Name] = append(names, s.Name), false
			}
		}
		for _, r := range rows {
			if r.Project == f.Project && r.Env == f.Environment && tagged[r.Service] {
				names = append(names, r.Service)
			}
		}
		if len(names) == 0 {
			return fmt.Errorf("no service in %s/%s has the tag %q", f.Project, f.Environment, *tag)
		}
	}
	body, _ := json.Marshal(map[string]any{"commit": *commit, "no_cache": *noCache})
	for _, name := range names {
		var out struct {
			DeployID string `json:"deploy_id"`
		}
		if err := c.do(ctx, "POST", fmt.Sprintf("/api/services/%s/%s/%s/deploys", f.Project, f.Environment, name), body, &out); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		fmt.Fprintf(stdout, "Deploy %s queued.\n", out.DeployID)
		if err := follow(ctx, c, out.DeployID); err != nil {
			return err
		}
	}
	return nil
}

func rollbackCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	file := fs.String("f", "yawl.yaml", "spec file naming the project and environment")
	to := fs.String("to", "", "deploy id to go back to (default: the version before the current one)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: yawl rollback <service> [--to deploy]")
	}
	f, err := readSpec(*file)
	if err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"to": *to})
	var out struct {
		DeployID string `json:"deploy_id"`
		To       string `json:"to"`
		Note     string `json:"note"`
	}
	if err := c.do(ctx, "POST", fmt.Sprintf("/api/services/%s/%s/%s/rollback", f.Project, f.Environment, pos[0]), body, &out); err != nil {
		return err
	}
	if err := follow(ctx, c, out.DeployID); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Rolled back %s-%s to %s.\n", f.Project, pos[0], out.To)
	if out.Note != "" {
		fmt.Fprintln(stdout, out.Note)
	}
	return nil
}

func readSpec(path string) (spec.File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return spec.File{}, err
	}
	return spec.Parse(b)
}

func follow(ctx context.Context, c *client, id string) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.cfg.URL+"/api/deploys/"+id+"/stream", nil)
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	seen := map[string]string{}
	var last deployView
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok || json.Unmarshal([]byte(data), &last) != nil {
			continue
		}
		for _, s := range last.Steps {
			if seen[s.Step] != s.State {
				seen[s.Step] = s.State
				fmt.Fprintf(stdout, "  %-9s%s\n", s.Step, s.State)
				if s.Step == "build" && s.State == "ok" {
					for _, l := range strings.Split(s.Output, "\n") {
						if strings.HasPrefix(l, "warning: ") {
							fmt.Fprintf(stdout, "  %s\n", l)
						}
					}
				}
			}
		}
	}
	switch last.State {
	case "live":
		fmt.Fprintf(stdout, "%s is live.\n", last.Service)
		return nil
	case "":
		return errors.New("lost the deploy stream; check `yawl status`")
	default:
		return fmt.Errorf("deploy %s: %s", strings.ReplaceAll(last.State, "_", " "), last.Cause)
	}
}

func status(ctx context.Context) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	var rows []struct{ Project, Env, Service, Kind, URL, State, Commit, Cause string }
	if err := c.do(ctx, "GET", "/api/status", nil, &rows); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tKIND\tSTATE\tURL")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s/%s/%s\t%s\t%s\t%s\n", r.Project, r.Env, r.Service, r.Kind, r.State, r.URL)
	}
	return tw.Flush()
}

func exportCmd(ctx context.Context, args []string) error {
	if len(args) != 1 || strings.Count(args[0], "/") != 1 {
		return errors.New("usage: yawl export project/environment > yawl.yaml")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	project, env, _ := strings.Cut(args[0], "/")
	req, _ := http.NewRequestWithContext(ctx, "GET", c.cfg.URL+"/api/environments/"+url.PathEscape(project)+"/"+url.PathEscape(env)+"/export", nil)
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return errors.New(strings.TrimSpace(string(b)))
	}
	_, err = io.Copy(stdout, res.Body)
	return err
}
