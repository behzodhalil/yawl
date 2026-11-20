package imports

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/behzodhalil/yawl/internal/compose"
	"github.com/behzodhalil/yawl/internal/spec"
)

type kamalEnv struct {
	Clear  map[string]string
	Secret []string
}

// kamalEnvOf reads env with clear and secret, or a plain map, which Kamal takes as clear.
func kamalEnvOf(n yaml.Node) (kamalEnv, error) {
	var e kamalEnv
	if n.IsZero() {
		return e, nil
	}
	var probe map[string]yaml.Node
	if err := n.Decode(&probe); err != nil {
		return e, fmt.Errorf("env: %w", err)
	}
	_, clear := probe["clear"]
	_, secret := probe["secret"]
	if !clear && !secret {
		return e, n.Decode(&e.Clear)
	}
	return e, n.Decode(&e)
}

var kamalWhy = map[string]string{
	"image":    "Yawl builds from the repository and reaches the server itself.",
	"registry": "Yawl builds from the repository and reaches the server itself.",
	"builder":  "Yawl builds from the repository and reaches the server itself.",
	"ssh":      "Yawl builds from the repository and reaches the server itself.",
}

func Kamal(file []byte) (compose.Result, error) {
	var k struct {
		Service     string
		Servers     yaml.Node
		PrimaryRole string `yaml:"primary_role"`
		Proxy       struct {
			Host        string
			Hosts       []string
			AppPort     int `yaml:"app_port"`
			Healthcheck struct{ Path string }
		}
		Env         yaml.Node
		Accessories map[string]struct {
			Image string
			Env   yaml.Node
		}
		Volumes []string
	}
	if err := yaml.Unmarshal(file, &k); err != nil {
		return compose.Result{}, fmt.Errorf("not a Kamal deploy.yml: %w", err)
	}
	var raw map[string]yaml.Node
	yaml.Unmarshal(file, &raw)
	if k.Service == "" {
		return compose.Result{}, fmt.Errorf("deploy.yml names no service")
	}
	var r compose.Result
	top, err := kamalEnvOf(k.Env)
	if err != nil {
		return compose.Result{}, err
	}

	dbs := map[string]bool{} // an accessory is reached as <service>-<accessory>
	for _, a := range keysOf(k.Accessories) {
		acc, name := k.Accessories[a], k.Service+"-"+a
		kind, version, ok := compose.Database(acc.Image)
		if !ok {
			warn(&r, name, "accessory %s (%s) is not carried; add it to yawl.yaml by hand.", a, acc.Image)
			continue
		}
		database(&r, name, kind, version)
		if note := compose.VersionNote(acc.Image); note != "" {
			warn(&r, name, "%s", note)
		}
		dbs[name] = true
		env, err := kamalEnvOf(acc.Env)
		if err != nil {
			return compose.Result{}, fmt.Errorf("accessories.%s: %w", a, err)
		}
		for _, key := range append(keysOf(env.Clear), env.Secret...) {
			key, _, _ = strings.Cut(key, ":")
			if strings.Contains(key, "PASSWORD") {
				warn(&r, name, "%s is not carried; Yawl makes the database's own password.", key)
			} else {
				warn(&r, name, "%s is not carried; Yawl sets the database up itself.", key)
			}
		}
	}

	type role struct {
		Cmd string
		Env yaml.Node
	}
	roles := map[string]role{}
	switch k.Servers.Kind {
	case yaml.SequenceNode: // a list of hosts: one web role
		roles["web"] = role{}
	case yaml.MappingNode:
		var m map[string]yaml.Node
		k.Servers.Decode(&m)
		for name, n := range m {
			var ro role
			if n.Kind == yaml.MappingNode {
				if err := n.Decode(&ro); err != nil {
					return compose.Result{}, fmt.Errorf("servers.%s: %w", name, err)
				}
			}
			roles[name] = ro
		}
	default:
		return compose.Result{}, fmt.Errorf("deploy.yml has no servers")
	}
	web := k.PrimaryRole
	if web == "" {
		web = "web"
	}
	order := keysOf(roles)
	if _, ok := roles[web]; ok { // the web role first, as Kamal boots it
		order = append([]string{web}, slices.DeleteFunc(order, func(n string) bool { return n == web })...)
	}
	secrets := false
	for _, name := range order {
		ro := roles[name]
		s := spec.Service{Name: k.Service + "-" + name, Kind: spec.KindWorker, Command: shell(ro.Cmd)}
		if name == web {
			s.Name, s.Kind, s.Port = k.Service, spec.KindWeb, k.Proxy.AppPort
			if s.Port == 0 {
				s.Port = 80 // Kamal's default
			}
			s.HealthPath = k.Proxy.Healthcheck.Path
			if s.HealthPath == "" {
				s.HealthPath = "/up" // kamal-proxy's default
			}
			hosts := k.Proxy.Hosts
			if k.Proxy.Host != "" {
				hosts = append([]string{k.Proxy.Host}, hosts...)
			}
			s.Domains = domains(&r, s.Name, hosts)
		}
		for _, v := range k.Volumes {
			host, rest, _ := strings.Cut(v, ":")
			dst, _, _ := strings.Cut(rest, ":") // a mode, like :ro
			if dst == "" {
				continue
			}
			vol := spec.Volume{Name: volumeName(host), Path: dst}
			if strings.HasPrefix(host, "/") || strings.HasPrefix(host, ".") {
				vol.Name = volumeName(path.Base(dst))
			}
			if name != web { // Kamal mounts it on every role; a Yawl volume is one service's
				warn(&r, s.Name, "volume %s is on %s only; Yawl does not share a volume between services.", vol.Name, k.Service)
				continue
			}
			s.Volumes = append(s.Volumes, vol)
			emptyVolume(&r, s.Name, vol, host)
		}
		env, err := kamalEnvOf(ro.Env)
		if err != nil {
			return compose.Result{}, fmt.Errorf("servers.%s: %w", name, err)
		}
		clear := maps.Clone(top.Clear)
		if clear == nil {
			clear = map[string]string{}
		}
		maps.Copy(clear, env.Clear) // a role's own env wins, as in Kamal
		for _, key := range keysOf(clear) {
			add(&r, compose.Var{Service: s.Name, Key: key, Value: clear[key]}, dbs)
		}
		for _, key := range append(append([]string{}, top.Secret...), env.Secret...) {
			key, _, _ = strings.Cut(key, ":")
			warn(&r, s.Name, "%s is a Kamal secret; set it with yawl env set %s %s.", key, s.Name, key)
			secrets = true
		}
		r.Services = append(r.Services, s)
	}
	if secrets {
		r.Warnings = append(r.Warnings, ".kamal/secrets is not read: it may run commands.")
	}
	notCarried(&r, k.Service, keysOf(raw), set("service", "servers", "primary_role", "proxy", "env", "accessories", "volumes"), kamalWhy)
	return done(r)
}
