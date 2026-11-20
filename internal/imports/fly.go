package imports

import (
	"errors"
	"fmt"

	"github.com/pelletier/go-toml/v2"

	"github.com/behzodhalil/yawl/internal/compose"
	"github.com/behzodhalil/yawl/internal/spec"
)

type flyMount struct {
	Source      string   `toml:"source"`
	Destination string   `toml:"destination"`
	Processes   []string `toml:"processes"`
}

type flyFile struct {
	App    string         `toml:"app"`
	Build  map[string]any `toml:"build"`
	Deploy struct {
		ReleaseCommand string `toml:"release_command"`
	} `toml:"deploy"`
	Env         map[string]any    `toml:"env"`
	Processes   map[string]string `toml:"processes"`
	HTTPService *struct {
		InternalPort int      `toml:"internal_port"`
		Processes    []string `toml:"processes"`
		Checks       []struct {
			Path string `toml:"path"`
		} `toml:"checks"`
	} `toml:"http_service"`
	Services []struct {
		InternalPort int      `toml:"internal_port"`
		Processes    []string `toml:"processes"`
		HTTPChecks   []struct {
			Path string `toml:"path"`
		} `toml:"http_checks"`
	} `toml:"services"`
	Mounts any `toml:"mounts"` // one table or an array of them
}

var flyWhy = map[string]string{
	"primary_region": "Yawl runs on your server.",
	"vm":             "Yawl runs on your server; set cpu and memory in yawl.yaml.",
	"statics":        "Yawl serves what the app serves.",
}

func Fly(file []byte) (compose.Result, error) {
	var f flyFile
	if err := toml.Unmarshal(file, &f); err != nil {
		return compose.Result{}, fmt.Errorf("not a fly.toml: %w", err)
	}
	var raw map[string]any
	toml.Unmarshal(file, &raw)
	if f.App == "" {
		return compose.Result{}, errors.New(`fly.toml names no app; add app = "<name>" at its top`)
	}
	var r compose.Result
	app := f.App
	image, _ := f.Build["image"].(string)
	if df, _ := f.Build["dockerfile"].(string); df != "" && df != "Dockerfile" && df != "./Dockerfile" {
		warn(&r, app, "builds %s; Yawl builds the Dockerfile at the repository's root.", df)
	}
	for _, k := range keysOf(f.Build) {
		if k != "image" && k != "dockerfile" {
			warn(&r, app, "build.%s is not carried; Yawl's builder or Railpack decides how to build.", k)
		}
	}
	port, health, webProc := 0, "", ""
	if h := f.HTTPService; h != nil {
		port = h.InternalPort
		if len(h.Processes) > 0 {
			webProc = h.Processes[0]
		}
		if len(h.Checks) > 0 {
			health = h.Checks[0].Path
		}
	} else if len(f.Services) > 0 {
		s := f.Services[0]
		port = s.InternalPort
		if len(s.Processes) > 0 {
			webProc = s.Processes[0]
		}
		if len(s.HTTPChecks) > 0 {
			health = s.HTTPChecks[0].Path
		}
	}
	main := ""
	if _, ok := f.Processes[webProc]; ok {
		main = webProc
	} else if _, ok := f.Processes["app"]; ok || len(f.Processes) == 0 {
		main = "app"
	} else {
		main = keysOf(f.Processes)[0]
	}
	byProc := map[string]int{}
	procs := keysOf(f.Processes)
	if len(procs) == 0 {
		procs = []string{main}
	}
	for _, p := range procs {
		s := spec.Service{Name: app, Kind: spec.KindWorker, Image: image, Command: shell(f.Processes[p])}
		if p == main {
			if port > 0 {
				s.Kind, s.Port, s.HealthPath = spec.KindWeb, port, health
			}
			s.Release = shell(f.Deploy.ReleaseCommand)
		} else {
			s.Name = app + "-" + p
		}
		byProc[p] = len(r.Services)
		r.Services = append(r.Services, s)
	}
	mounts, err := flyMounts(f.Mounts)
	if err != nil {
		return compose.Result{}, err
	}
	for _, m := range mounts {
		on := m.Processes
		if len(on) == 0 { // Fly mounts one on every process
			on = procs
		}
		for _, p := range on {
			i, ok := byProc[p]
			if !ok {
				continue
			}
			v := spec.Volume{Name: volumeName(m.Source), Path: m.Destination}
			r.Services[i].Volumes = append(r.Services[i].Volumes, v)
			emptyVolume(&r, r.Services[i].Name, v, "Fly volume "+m.Source)
		}
	}
	for _, s := range r.Services {
		for _, k := range keysOf(f.Env) {
			add(&r, compose.Var{Service: s.Name, Key: k, Value: fmt.Sprint(f.Env[k])}, nil)
		}
	}
	notCarried(&r, app, keysOf(raw), set("app", "build", "deploy", "env", "processes", "http_service", "services", "mounts"), flyWhy)
	r.Warnings = append(r.Warnings, "secrets, Fly Postgres, regions and machine sizes are not in fly.toml; list the secrets with fly secrets list and set each with yawl env set.")
	return done(r)
}

func flyMounts(v any) ([]flyMount, error) {
	var list []any
	switch m := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		list = []any{m}
	case []any:
		list = m
	default:
		return nil, errors.New("fly.toml: mounts is not a table")
	}
	var out []flyMount
	for _, e := range list {
		b, err := toml.Marshal(e)
		if err != nil {
			return nil, err
		}
		var m flyMount
		if err := toml.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("fly.toml: mounts: %w", err)
		}
		if m.Destination == "" {
			return nil, errors.New("fly.toml: a mount has no destination")
		}
		out = append(out, m)
	}
	return out, nil
}
