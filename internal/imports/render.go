package imports

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/behzodhalil/yawl/internal/compose"
	"github.com/behzodhalil/yawl/internal/spec"
)

type renderService struct {
	Type, Name, Runtime, Env string
	Image                    struct {
		URL   string
		Creds yaml.Node
	}
	StartCommand     string `yaml:"startCommand"`
	DockerCommand    string `yaml:"dockerCommand"`
	PreDeployCommand string `yaml:"preDeployCommand"`
	HealthCheckPath  string `yaml:"healthCheckPath"`
	NumInstances     int    `yaml:"numInstances"`
	Domains          []string
	Disk             *struct {
		Name      string
		MountPath string `yaml:"mountPath"`
	}
	EnvVars        []renderVar `yaml:"envVars"`
	Schedule       string
	DockerfilePath string `yaml:"dockerfilePath"`
	RootDir        string `yaml:"rootDir"`
}

type renderVar struct {
	Key, Value   string
	FromDatabase *struct{ Name, Property string } `yaml:"fromDatabase"`
	FromService  *struct {
		Type, Name, Property string
		EnvVarKey            string `yaml:"envVarKey"`
	} `yaml:"fromService"`
	GenerateValue bool   `yaml:"generateValue"`
	Sync          *bool  `yaml:"sync"`
	FromGroup     string `yaml:"fromGroup"`
}

var renderRead = set("type", "name", "runtime", "env", "image", "startCommand", "dockerCommand", "preDeployCommand",
	"healthCheckPath", "numInstances", "domains", "disk", "envVars", "schedule", "dockerfilePath", "rootDir")

var renderWhy = map[string]string{
	"buildCommand":  "Yawl's builder or Railpack decides how to build.",
	"repo":          "Yawl uses this folder's origin.",
	"branch":        "Yawl uses this folder's branch.",
	"dockerContext": "Yawl builds the repository's root.",
	"plan":          "Yawl runs on your server.",
	"region":        "Yawl runs on your server.",
}

func Render(file []byte) (compose.Result, error) {
	var top struct {
		Services     []yaml.Node `yaml:"services"`
		Databases    []yaml.Node `yaml:"databases"`
		EnvVarGroups []struct {
			Name    string
			EnvVars []renderVar `yaml:"envVars"`
		} `yaml:"envVarGroups"`
		Projects []struct {
			Name         string
			Environments []struct {
				Name      string
				Services  []yaml.Node `yaml:"services"`
				Databases []yaml.Node `yaml:"databases"`
			}
		}
	}
	if err := yaml.Unmarshal(file, &top); err != nil {
		return compose.Result{}, fmt.Errorf("not a render.yaml: %w", err)
	}
	var r compose.Result
	if len(top.Services)+len(top.Databases) == 0 && len(top.Projects) > 0 && len(top.Projects[0].Environments) > 0 {
		p := top.Projects[0]
		e := p.Environments[0]
		top.Services, top.Databases = e.Services, e.Databases
		r.Warnings = append(r.Warnings, fmt.Sprintf("only project %s, environment %s is read; import another by moving its services to the top of render.yaml.", p.Name, e.Name))
	}
	groups := map[string][]renderVar{}
	for _, g := range top.EnvVarGroups {
		groups[g.Name] = g.EnvVars
	}
	dbs := map[string]bool{}
	for _, n := range top.Databases {
		var d struct {
			Name    string
			Version string `yaml:"postgresMajorVersion"`
		}
		var raw map[string]yaml.Node
		if err := n.Decode(&d); err != nil {
			return compose.Result{}, fmt.Errorf("databases: %w", err)
		}
		n.Decode(&raw)
		version := 0
		switch d.Version {
		case "16", "17", "18":
			version, _ = strconv.Atoi(d.Version)
		case "":
		default:
			warn(&r, d.Name, "Postgres %s becomes 17; Yawl runs 16, 17 and 18.", d.Version)
		}
		database(&r, d.Name, spec.KindPostgres, version)
		notCarried(&r, d.Name, keysOf(raw), set("name", "postgresMajorVersion"), map[string]string{
			"databaseName": "Yawl sets the database up itself.", "user": "Yawl sets the database up itself.",
			"plan": renderWhy["plan"], "region": renderWhy["region"]})
		dbs[d.Name] = true
	}
	svcs := make([]renderService, len(top.Services))
	raws := make([]map[string]yaml.Node, len(top.Services))
	for i, n := range top.Services {
		if err := n.Decode(&svcs[i]); err != nil {
			return compose.Result{}, fmt.Errorf("services: %w", err)
		}
		n.Decode(&raws[i])
		if t := svcs[i].Type; t == "keyvalue" || t == "redis" {
			dbs[svcs[i].Name] = true
		}
	}
	gen := map[string]string{}
	for i, s := range svcs {
		n := s.Name
		runtime := s.Runtime
		if runtime == "" {
			runtime = s.Env // the older name
		}
		svc := spec.Service{Name: n}
		switch s.Type {
		case "keyvalue", "redis":
			database(&r, n, spec.KindValkey, 8)
			notCarried(&r, n, keysOf(raws[i]), set("type", "name"), renderWhy)
			continue
		case "web":
			if runtime == "static" {
				warn(&r, n, "a static site is not carried; add it to yawl.yaml as a web service.")
				continue
			}
			svc.Kind, svc.Port = spec.KindWeb, 10000 // Render's own PORT
		case "pserv", "worker":
			svc.Kind = spec.KindWorker
			if s.Type == "pserv" {
				warn(&r, n, "a private service becomes a worker; other services reach it by name.")
			}
		case "cron":
			svc.Kind, svc.Schedule, svc.Timezone = spec.KindCron, s.Schedule, "UTC" // Render runs cron jobs in UTC
		default:
			warn(&r, n, "type %s is not carried; Yawl does not run it.", s.Type)
			continue
		}
		if runtime == "image" || s.Image.URL != "" {
			svc.Image = s.Image.URL
			if !s.Image.Creds.IsZero() {
				warn(&r, n, "the registry login is not carried; add it in Integrations, Registries.")
			}
		}
		// Render's rootDir is the folder built and the one whose changes deploy: root and watch
		if c := strings.TrimSuffix(strings.TrimPrefix(s.RootDir, "./"), "/"); spec.ValidRoot(c) && svc.Image == "" {
			svc.Root, svc.Watch = c, []string{c + "/**"}
		} else if c != "" && c != "." {
			warn(&r, n, "rootDir %s is not carried; Yawl builds a folder in the repository.", s.RootDir)
		}
		if s.DockerfilePath != "" && s.DockerfilePath != "Dockerfile" && s.DockerfilePath != "./Dockerfile" {
			warn(&r, n, "builds %s; Yawl builds the Dockerfile at the repository's root.", s.DockerfilePath)
		}
		start := s.StartCommand
		if start == "" {
			start = s.DockerCommand
		}
		svc.Command = shell(start)
		if svc.Kind == spec.KindCron {
			if len(svc.Command) == 0 {
				warn(&r, n, "a cron job with no startCommand is not carried; add it with its command.")
				continue
			}
		} else {
			svc.Release = shell(s.PreDeployCommand)
			svc.HealthPath = s.HealthCheckPath
			svc.Replicas = replicas(&r, n, s.NumInstances)
		}
		if svc.Kind == spec.KindWeb {
			svc.Domains = domains(&r, n, s.Domains)
		}
		if s.Disk != nil && svc.Kind != spec.KindCron {
			v := spec.Volume{Name: volumeName(s.Disk.Name), Path: s.Disk.MountPath}
			svc.Volumes = append(svc.Volumes, v)
			emptyVolume(&r, n, v, "Render's disk "+s.Disk.Name)
			if svc.Replicas > 0 {
				warn(&r, n, "has a disk, so it runs one copy.")
				svc.Replicas = 0
			}
		}
		for _, v := range s.EnvVars {
			if v.FromGroup == "" {
				renderEnv(&r, n, v, "", gen, dbs)
				continue
			}
			g, ok := groups[v.FromGroup]
			if !ok {
				warn(&r, n, "env group %s is not in render.yaml; set its variables with yawl env set.", v.FromGroup)
			}
			for _, gv := range g {
				renderEnv(&r, n, gv, v.FromGroup, gen, dbs)
			}
		}
		notCarried(&r, n, keysOf(raws[i]), renderRead, renderWhy)
		r.Services = append(r.Services, svc)
	}
	return done(r)
}

func renderEnv(r *compose.Result, service string, v renderVar, group string, gen map[string]string, dbs map[string]bool) {
	k := v.Key
	switch {
	case k == "":
		return
	case v.GenerateValue:
		id := service + "/" + k
		if group != "" {
			id = "group " + group + "/" + k
		}
		if gen[id] == "" {
			gen[id] = compose.Secret()
		}
		add(r, compose.Var{Service: service, Key: k, Value: gen[id], GeneratedSecret: true}, nil)
	case v.Sync != nil && !*v.Sync:
		warn(r, service, "%s is kept on Render; set it with yawl env set %s %s.", k, service, k)
	case v.FromDatabase != nil:
		renderRef(r, service, k, v.FromDatabase.Name, v.FromDatabase.Property, dbs)
	case v.FromService != nil:
		if v.FromService.EnvVarKey != "" {
			warn(r, service, "%s copies %s from %s; set it with yawl env set.", k, v.FromService.EnvVarKey, v.FromService.Name)
			return
		}
		renderRef(r, service, k, v.FromService.Name, v.FromService.Property, dbs)
	default:
		add(r, compose.Var{Service: service, Key: k, Value: v.Value}, nil)
	}
}

func renderRef(r *compose.Result, service, key, name, property string, dbs map[string]bool) {
	if dbs[name] && (property == "connectionString" || property == "") {
		add(r, compose.Var{Service: service, Key: key, Ref: name + ".url"}, nil)
		return
	}
	if dbs[name] {
		warn(r, service, "%s is %s's %s and is not carried; %s.url carries the whole address.", key, name, property, name)
		return
	}
	warn(r, service, "%s is %s's %s and is not carried; Yawl services reach each other by name, %s.", key, name, property, name)
}
