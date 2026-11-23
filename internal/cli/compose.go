package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/go-git/go-git/v5"
	"gopkg.in/yaml.v3"

	"github.com/behzodhalil/yawl/internal/compose"
	"github.com/behzodhalil/yawl/internal/spec"
)

func composeCmd(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "import" {
		return errors.New("usage: yawl compose import [compose file] [-f yawl.yaml] [--server <name>] [--project <name>] [--apply] [--yes]")
	}
	fs := flag.NewFlagSet("compose", flag.ContinueOnError)
	fl := importFlags(fs)
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	src := "compose.yaml"
	if len(pos) > 0 {
		src = pos[0]
	} else { // the names Compose itself looks for, in its order
		for _, n := range []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"} {
			if _, err := os.Stat(n); err == nil {
				src = n
				break
			}
		}
	}
	body, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	dir := filepath.Dir(src)
	res, err := compose.ConvertWith(body, func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, name)) }, composeHost(ctx, *fl.file, *fl.server))
	if err != nil {
		return err
	}
	return addImport(ctx, res, dir, "the Compose file", fl)
}

type flags struct {
	file, server, project *string
	apply, yes            *bool
}

func importFlags(fs *flag.FlagSet) flags {
	return flags{
		file:    fs.String("f", "yawl.yaml", "the spec file to add the services to"),
		server:  fs.String("server", "", "the server, when yawl.yaml is new"),
		project: fs.String("project", "", "the project, when yawl.yaml is new (default: the folder's name)"),
		apply:   fs.Bool("apply", false, "apply yawl.yaml and set the variables"),
		yes:     fs.Bool("yes", false, "with --apply: apply also changes yawl.yaml makes beyond the import"),
	}
}

func addImport(ctx context.Context, res compose.Result, dir, from string, fl flags) error {
	file, server, project, doApply, yes := fl.file, fl.server, fl.project, fl.apply, fl.yes
	var c *client
	var err error
	if *doApply {
		if c, err = newClient(); err != nil {
			return err
		}
	}
	repo, branch, err := origin(dir)
	for i, s := range res.Services {
		if s.Image == "" && !spec.IsDatabase(s.Kind) {
			if err != nil {
				return fmt.Errorf("%s builds here, and this folder has no git remote origin (%v); push it first", s.Name, err)
			}
			res.Services[i].Repo, res.Services[i].Branch = repo, branch
		}
	}
	out, f, err := addServices(*file, res.Services, *server, *project, from)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*file, out, 0o644); err != nil {
		return err
	}
	for _, s := range res.Services {
		fmt.Fprintf(stdout, "Added %s (%s).\n", s.Name, s.Kind)
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(stdout, "Note: "+w)
	}
	if !*doApply {
		for _, v := range res.Vars {
			if v.GeneratedSecret {
				fmt.Fprintf(stdout, "Note: %s %s is generated only with --apply; or set your own with yawl env set.\n", v.Service, v.Key)
			}
		}
		if len(res.Vars) == 0 {
			fmt.Fprintf(stdout, "Wrote %s. Next: yawl apply.\n", *file)
			return nil
		}
		fmt.Fprintf(stdout, "Wrote %s. Next: yawl apply, then set the variables:\n", *file)
		envLines(res.Vars)
		return nil
	}
	var changes []change
	if err := c.do(ctx, "POST", "/api/plan", out, &changes); err != nil {
		return fmt.Errorf("the import is written to %s; the plan failed: %w", *file, err)
	}
	added := map[string]bool{}
	for _, s := range res.Services {
		added[s.Name] = true
	}
	var beyond []string
	for _, ch := range changes {
		fmt.Fprintln(stdout, ch)
		if ch.Op != "unchanged" && !(ch.Op == "create" && (ch.Kind != "service" || added[ch.Name])) {
			beyond = append(beyond, ch.Name)
		}
	}
	if len(beyond) > 0 && !*yes {
		return fmt.Errorf("%s also changes %s; the import is written, review it with yawl apply", filepath.Base(*file), strings.Join(beyond, ", "))
	}
	if err := c.do(ctx, "POST", "/api/apply", out, nil); err != nil {
		return fmt.Errorf("the import is written to %s; applying it failed: %w", *file, err)
	}
	var left []compose.Var
	for _, v := range res.Vars {
		path := fmt.Sprintf("/api/services/%s/%s/%s/env/%s", f.Project, f.Environment, v.Service, v.Key)
		var put []byte
		var err error
		if v.Ref != "" {
			put, _ = json.Marshal(map[string]string{"ref": v.Ref})
		} else {
			var agent, rec string
			if agent, rec, err = sealValue(ctx, c, fmt.Sprintf("/api/services/%s/%s/%s", f.Project, f.Environment, v.Service), v.Value); err == nil {
				put, _ = json.Marshal(map[string]any{"agent_box": agent, "recovery_box": rec})
			}
		}
		if err == nil {
			err = c.do(ctx, "PUT", path, put, nil)
		}
		if err != nil {
			fmt.Fprintf(stdout, "%s %s: %v\n", v.Service, v.Key, err)
			left = append(left, v)
		}
	}
	if len(left) > 0 {
		fmt.Fprintln(stdout, "Applied. These variables were not set; set them by hand:")
		envLines(left)
		return fmt.Errorf("%d variable(s) not set", len(left))
	}
	fmt.Fprintf(stdout, "Applied, and %d variable(s) set. Deploy each: yawl deploy <name>\n", len(res.Vars))
	return nil
}

func envLines(vars []compose.Var) {
	for _, v := range vars {
		if v.Ref != "" {
			fmt.Fprintf(stdout, "  yawl env set %s %s --ref %s\n", v.Service, v.Key, v.Ref)
		} else {
			fmt.Fprintf(stdout, "  yawl env set %s %s\n", v.Service, v.Key)
		}
	}
}

func origin(dir string) (string, string, error) {
	r, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "", "", err
	}
	rem, err := r.Remote("origin")
	if err != nil {
		return "", "", err
	}
	head, err := r.Head()
	if err != nil {
		return "", "", err
	}
	repo := rem.Config().URLs[0]
	if u, err := url.Parse(repo); err == nil && u.User != nil {
		u.User = nil
		repo = u.String()
	}
	return repo, head.Name().Short(), nil
}

func folder() string {
	wd, _ := os.Getwd()
	return strings.ToLower(filepath.Base(wd))
}

type serviceYAML struct {
	Name       string        `yaml:"name"`
	Kind       string        `yaml:"kind"`
	Repo       string        `yaml:"repo,omitempty"`
	Branch     string        `yaml:"branch,omitempty"`
	Image      string        `yaml:"image,omitempty"`
	Port       int           `yaml:"port,omitempty"`
	HealthPath string        `yaml:"health_path,omitempty"`
	Version    int           `yaml:"version,omitempty"`
	Backup     *bool         `yaml:"backup,omitempty"`
	Replicas   int           `yaml:"replicas,omitempty"`
	Release    []string      `yaml:"release,omitempty,flow"`
	Domains    []string      `yaml:"domains,omitempty,flow"`
	Volumes    []spec.Volume `yaml:"volumes,omitempty"`
	Schedule   string        `yaml:"schedule,omitempty"`
	Timezone   string        `yaml:"timezone,omitempty"`
	Command    []string      `yaml:"command,omitempty,flow"`
}

func addServices(file string, svcs []spec.Service, server, project, from string) ([]byte, *spec.File, error) {
	old, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		if server == "" {
			return nil, nil, fmt.Errorf("%s is new: give the server with --server <name>", file)
		}
		if project == "" {
			project = folder()
		}
		old = []byte(fmt.Sprintf("project: %s\nenvironment: production\nserver: %s\nservices: []\n", project, server))
	} else if err != nil {
		return nil, nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(old, &doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%s does not read as a spec file", file)
	}
	top := doc.Content[0]
	var list *yaml.Node
	next := 0
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value == "services" {
			list = top.Content[i+1]
			if i+2 < len(top.Content) {
				next = top.Content[i+2].Line
			}
			if list.LineComment != "" { // a flow line's comment would go when the list turns block
				top.Content[i].LineComment, list.LineComment = list.LineComment, ""
			}
		}
	}
	if list == nil {
		list = &yaml.Node{Kind: yaml.SequenceNode}
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "services"}, list)
	} else if list.Kind == yaml.ScalarNode && (list.Tag == "!!null" || list.Value == "") {
		*list = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	} else if list.Kind != yaml.SequenceNode {
		return nil, nil, fmt.Errorf("services in %s is not a list", file)
	}
	before := len(list.Content)
	list.Style = 0
	have := map[string]*yaml.Node{}
	for _, e := range list.Content {
		for i := 0; i+1 < len(e.Content); i += 2 {
			if e.Content[i].Value == "name" {
				have[e.Content[i+1].Value] = e
			}
		}
	}
	var adds []serviceYAML
	for _, s := range svcs {
		a := serviceYAML{Name: s.Name, Kind: string(s.Kind), Repo: s.Repo, Branch: s.Branch, Image: s.Image,
			Port: s.Port, HealthPath: s.HealthPath, Version: s.Version, Backup: s.Backup, Replicas: s.Replicas, Release: s.Release,
			Domains: s.Domains, Volumes: s.Volumes, Schedule: s.Schedule, Timezone: s.Timezone, Command: s.Command}
		if e := have[s.Name]; e != nil {
			var cur serviceYAML
			if e.Decode(&cur) == nil && reflect.DeepEqual(cur, a) {
				continue
			}
			return nil, nil, fmt.Errorf("%s is already in %s; rename it in %s first", s.Name, filepath.Base(file), from)
		}
		adds = append(adds, a)
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if len(adds) == 0 {
		b.Write(old)
	} else if before > 0 && list.Style&yaml.FlowStyle == 0 {
		if err := enc.Encode(adds); err != nil {
			return nil, nil, err
		}
		b = spliceEntries(old, b.Bytes(), list, next)
	} else {
		for _, a := range adds {
			var n yaml.Node
			if err := n.Encode(a); err != nil {
				return nil, nil, err
			}
			list.Content = append(list.Content, &n)
		}
		if err := enc.Encode(&doc); err != nil {
			return nil, nil, err
		}
	}
	f, err := spec.Parse(b.Bytes())
	if err != nil {
		return nil, nil, fmt.Errorf("the services would not make a valid %s: %w", filepath.Base(file), err)
	}
	if len(f.Services) != before+len(adds) {
		return nil, nil, fmt.Errorf("%s did not take the services; add them by hand", filepath.Base(file))
	}
	return b.Bytes(), &f, nil
}

// spliceEntries puts entries, a YAML list, after the last entry of list in
// old, at its dash's column: the file's indents and comments stay as they
// are, where encoding it again would redo them. next is the line of the key
// after the list (0: it ends the file); comments before that key stay its.
func spliceEntries(old, entries []byte, list *yaml.Node, next int) bytes.Buffer {
	lines := strings.SplitAfter(string(old), "\n")
	end := len(lines)
	if next > 0 {
		end = next - 1
	}
	for last := list.Content[len(list.Content)-1].Line; end > last; end-- {
		if l := strings.TrimSpace(lines[end-1]); l != "" && !strings.HasPrefix(l, "#") {
			break
		}
	}
	var b bytes.Buffer
	b.WriteString(strings.Join(lines[:end], ""))
	if b.Len() > 0 && !bytes.HasSuffix(b.Bytes(), []byte("\n")) {
		b.WriteByte('\n')
	}
	pad := strings.Repeat(" ", max(list.Content[0].Column-3, 0))
	for _, l := range strings.SplitAfter(strings.TrimSuffix(string(entries), "\n"), "\n") {
		b.WriteString(pad + l)
	}
	b.WriteByte('\n')
	b.WriteString(strings.Join(lines[end:], ""))
	return b
}

func composeHost(ctx context.Context, file, server string) func(string) string {
	env := "production"
	if b, err := os.ReadFile(file); err == nil {
		if f, err := spec.Parse(b); err == nil {
			server, env = f.Server, f.Environment
		}
	}
	c, err := newClient()
	if server == "" || err != nil {
		return nil
	}
	var servers []struct {
		Name     string `json:"name"`
		PublicIP string `json:"public_ip"`
	}
	if c.do(ctx, "GET", "/api/servers", nil, &servers) != nil {
		return nil
	}
	for _, s := range servers {
		if s.Name == server && s.PublicIP != "" {
			ip := strings.NewReplacer(".", "-", ":", "-").Replace(s.PublicIP)
			return func(svc string) string { return svc + "-" + env + "." + ip + ".sslip.io" }
		}
	}
	return nil
}
