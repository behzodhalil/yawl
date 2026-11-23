package compose

import (
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/behzodhalil/yawl/internal/spec"
)

type Var struct {
	Service, Key, Value, Ref string
	GeneratedSecret          bool
}

type Result struct {
	Services []spec.Service
	Vars     []Var
	Warnings []string

	names  *namer
	web    map[string]int
	dbUses map[string]string
}

var ignored = map[string]string{
	"depends_on":  "Yawl starts each service on its own.",
	"healthcheck": "Yawl checks a web service at its health path.",
	"networks":    "Yawl gives each environment its own network.",
	"command":     "Yawl runs the image's own command.",
	"entrypoint":  "Yawl runs the image's own command.",
	"restart":     "Yawl restarts what stops.",
}

var read = map[string]bool{"image": true, "build": true, "ports": true, "environment": true, "env_file": true, "volumes": true}

var appRead = map[string]bool{"command": true, "entrypoint": true}

func Convert(file []byte, envFile func(name string) ([]byte, error)) (Result, error) {
	return ConvertWith(file, envFile, nil)
}

func ConvertWith(file []byte, envFile func(name string) ([]byte, error), host func(service string) string) (Result, error) {
	var top struct {
		Services map[string]map[string]yaml.Node `yaml:"services"`
	}
	if err := yaml.Unmarshal(file, &top); err != nil {
		return Result{}, fmt.Errorf("not a Compose file: %w", err)
	}
	names := slices.Sorted(maps.Keys(top.Services))
	if h := host; h != nil {
		host = func(s string) string { return h(yawlName(s)) }
	}
	r := Result{names: newNamer(names, host), web: map[string]int{}, dbUses: map[string]string{}}
	for _, n := range names {
		env, _ := environment(top.Services[n])
		r.names.declared(env, r.web)
	}
	dbs := map[string]bool{}
	secrets := map[string]string{}
	for _, n := range names {
		var img string
		if node, ok := top.Services[n]["image"]; ok {
			node.Decode(&img)
		}
		if _, _, ok := Database(img); ok {
			dbs[n] = true
			env, _ := environment(top.Services[n])
			for k, v := range env {
				if strings.Contains(k, "PASSWORD") && v != nil && *v != "" {
					secrets[*v] = n
				}
				if v != nil {
					for _, name := range namesIn(*v) {
						r.dbUses[name] = n
					}
				}
			}
		}
	}
	r.names.dbs = dbs
	skipped := map[string]bool{}
	for _, n := range names {
		raw := top.Services[n]
		for k := range raw {
			if !read[k] && (dbs[n] || !appRead[k]) {
				skipped[k] = true
			}
		}
		var err error
		if dbs[n] {
			err = r.database(n, raw)
		} else {
			err = r.app(n, raw, dbs, secrets, envFile)
		}
		if err != nil {
			return Result{}, err
		}
	}
	if r.names.filled {
		r.Warnings = append(r.Warnings, "SERVICE_URL_ and SERVICE_FQDN_ names are set to Yawl's sslip.io addresses; after adding a domain, change them with yawl env set.")
	}
	for _, k := range slices.Sorted(maps.Keys(skipped)) {
		why := ignored[k]
		if why == "" {
			why = "Yawl does not read it."
		}
		r.Warnings = append(r.Warnings, k+" is ignored; "+why)
	}
	if err := Rename(&r); err != nil {
		return Result{}, err
	}
	slices.SortStableFunc(r.Vars, func(a, b Var) int { return strings.Compare(a.Service+"\x00"+a.Key, b.Service+"\x00"+b.Key) })
	return r, nil
}

func Rename(r *Result) error {
	by := map[string]string{}
	for i, s := range r.Services {
		y := yawlName(s.Name)
		if o, ok := by[y]; ok {
			return fmt.Errorf("%s and %s are both %s in Yawl; rename one", o, s.Name, y)
		}
		by[y] = s.Name
		if y != s.Name {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s is named %s; Yawl's names are lowercase letters, digits and dashes.", s.Name, y))
			r.Services[i].Name = y
		}
	}
	for i, v := range r.Vars {
		r.Vars[i].Service = yawlName(v.Service)
		if d, ok := strings.CutSuffix(v.Ref, ".url"); ok {
			r.Vars[i].Ref = yawlName(d) + ".url"
		}
	}
	return nil
}

func yawlName(n string) string {
	b := []byte(strings.ToLower(n))
	for i, c := range b {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

var databaseImages = map[string]spec.Kind{
	"postgres": spec.KindPostgres, "pgvector/pgvector": spec.KindPostgres,
	"mysql": spec.KindMySQL, "mysql/mysql-server": spec.KindMySQL, "bitnami/mysql": spec.KindMySQL,
	"mariadb": spec.KindMariaDB, "bitnami/mariadb": spec.KindMariaDB,
	"clickhouse/clickhouse-server": spec.KindClickHouse, "yandex/clickhouse-server": spec.KindClickHouse,
	"mongo": spec.KindMongoDB,
	"redis": spec.KindValkey, "bitnami/redis": spec.KindValkey, "valkey/valkey": spec.KindValkey, "bitnami/valkey": spec.KindValkey,
}

var yawlVersion = map[spec.Kind]int{spec.KindMySQL: 8, spec.KindMariaDB: 11, spec.KindClickHouse: 25, spec.KindMongoDB: 8, spec.KindValkey: 8}

func imageParts(image string) (repo, tag string) {
	ref := strings.Split(image, "@")[0]
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref, tag = ref[:i], ref[i+1:]
	}
	ref = strings.TrimPrefix(strings.TrimPrefix(ref, "docker.io/"), "index.docker.io/")
	if host, _, ok := strings.Cut(ref, "/"); ok && strings.ContainsAny(host, ".:") {
		return "", tag
	}
	return strings.TrimPrefix(ref, "library/"), tag
}

var tagMajor = regexp.MustCompile(`^(?:.*-)?pg(\d+)|^(\d+)`)

func imageMajor(tag string) int {
	m := tagMajor.FindStringSubmatch(tag)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1] + m[2])
	return n
}

func Database(image string) (spec.Kind, int, bool) {
	repo, tag := imageParts(image)
	kind, ok := databaseImages[repo]
	if !ok {
		return "", 0, false
	}
	if kind == spec.KindPostgres {
		if v := imageMajor(tag); v == 16 || v == 18 {
			return kind, v, true
		}
		return kind, 17, true
	}
	return kind, yawlVersion[kind], true
}

func VersionNote(image string) string {
	_, tag := imageParts(image)
	_, v, ok := Database(image)
	if n := imageMajor(tag); ok && n != 0 && n != v {
		return fmt.Sprintf("%s runs as version %d, the one Yawl runs; check the app works with it.", image, v)
	}
	return ""
}

func argvOrShellLine(entry, cmd yaml.Node) []string {
	var ep, c []string
	epList, cList := entry.Decode(&ep) == nil, cmd.Decode(&c) == nil
	if epList && cList {
		return append(ep, c...)
	}
	var line []string
	for _, n := range []yaml.Node{entry, cmd} {
		var l []string
		if n.Kind == yaml.ScalarNode && n.Tag != "!!null" {
			line = append(line, n.Value)
		} else if n.Decode(&l) == nil {
			for _, w := range l {
				line = append(line, quote(w))
			}
		}
	}
	if l := strings.TrimSpace(strings.Join(line, " ")); l != "" {
		return []string{"sh", "-c", l}
	}
	return nil
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)

func quote(w string) string {
	if plainWord.MatchString(w) {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}

func (r *Result) warn(service, format string, a ...any) {
	r.Warnings = append(r.Warnings, service+": "+fmt.Sprintf(format, a...))
}

func (r *Result) database(n string, raw map[string]yaml.Node) error {
	var img string
	node := raw["image"]
	node.Decode(&img)
	kind, version, _ := Database(img)
	s := spec.Service{Name: n, Kind: kind, Version: version}
	if note := VersionNote(img); note != "" {
		r.warn(n, "%s", note)
	}
	if _, ok := raw["build"]; ok {
		r.warn(n, "build is ignored; Yawl runs its own %s database.", kind)
	}
	if kind == spec.KindValkey {
		on := true
		s.Backup = &on
	}
	env, err := environment(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", n, err)
	}
	for _, k := range slices.Sorted(maps.Keys(env)) {
		if strings.Contains(k, "PASSWORD") {
			r.warn(n, "%s is not carried; Yawl makes the database's own password.", k)
		} else {
			r.warn(n, "%s is not carried; Yawl sets the database up itself.", k)
		}
	}
	vols, err := volumes(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", n, err)
	}
	for _, v := range vols {
		r.warn(n, "the data in volume %s is not moved; restore a dump into %s once it runs.", strings.Split(v, ":")[0], n)
	}
	if _, ok := raw["ports"]; ok {
		r.warn(n, "its ports are not published; a Yawl database is reached by reference.")
	}
	r.Services = append(r.Services, s)
	return nil
}

func (r *Result) app(n string, raw map[string]yaml.Node, dbs map[string]bool, secrets map[string]string, envFile func(string) ([]byte, error)) error {
	s := spec.Service{Name: n, Kind: spec.KindWorker}
	b, hasBuild := raw["build"]
	if hasBuild {
		var ctx, df string
		if b.Kind == yaml.ScalarNode {
			b.Decode(&ctx)
		} else {
			var o struct {
				Context, Dockerfile string
				Args                yaml.Node
			}
			b.Decode(&o)
			ctx, df = o.Context, o.Dockerfile
			var args []string
			for i, a := range o.Args.Content {
				if o.Args.Kind == yaml.SequenceNode {
					k, _, _ := strings.Cut(a.Value, "=")
					args = append(args, k)
				} else if i%2 == 0 {
					args = append(args, a.Value)
				}
			}
			for _, a := range args {
				r.warn(n, "build arg %s is not carried; set it with yawl env set %s %s --build.", a, yawlName(n), a)
			}
		}
		if c := strings.TrimSuffix(strings.TrimPrefix(ctx, "./"), "/"); spec.ValidRoot(c) {
			s.Root = c
		} else if c != "" && c != "." {
			r.warn(n, "builds from %s, outside the repository; Yawl builds a folder in it.", ctx)
		}
		if df != "" && df != "Dockerfile" {
			r.warn(n, "builds %s; Yawl builds the Dockerfile at the top of its root.", df)
		}
	} else if node, ok := raw["image"]; ok {
		node.Decode(&s.Image)
	}
	if !hasBuild && s.Image == "" {
		return fmt.Errorf("%s: no image and no build; nothing to run", n)
	}
	s.Command = argvOrShellLine(raw["entrypoint"], raw["command"])
	if _, ok := raw["entrypoint"]; ok && len(s.Command) > 0 {
		r.warn(n, "entrypoint runs as the start of command; an ENTRYPOINT in the image itself still runs first.")
	}
	ports, err := r.portsOf(n, raw)
	if err != nil {
		return fmt.Errorf("%s: %w", n, err)
	}
	web := slices.DeleteFunc(slices.Clone(ports), func(p int) bool { return notHTTP[p] })
	if p, ok := r.web[n]; ok {
		s.Kind, s.Port = spec.KindWeb, p
	} else if len(web) > 0 {
		s.Kind, s.Port = spec.KindWeb, web[0]
		if len(ports) > 1 {
			r.warn(n, "publishes %d ports; Yawl serves the first, %d.", len(ports), web[0])
		}
	} else {
		for _, p := range ports {
			r.warn(n, "port %d is not HTTP; it runs as a worker, which Yawl does not publish.", p)
		}
	}
	vols, err := volumes(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", n, err)
	}
	for _, v := range vols {
		src, dst, named := strings.Cut(v, ":") // a path alone is a volume with no name
		dst, _, _ = strings.Cut(dst, ":")      // a mode, like :ro
		if !named {
			r.warn(n, "anonymous volume %s is skipped; name it in the Compose file to keep its data.", src)
			continue
		}
		if dst == "" || strings.HasPrefix(src, ".") || strings.HasPrefix(src, "/") || strings.HasPrefix(src, "~") {
			r.warn(n, "bind mount %s is skipped; a server's folders are not this machine's.", v)
			continue
		}
		if yawlName(src) != src {
			r.warn(n, "volume %s is named %s.", src, yawlName(src))
		}
		s.Volumes = append(s.Volumes, spec.Volume{Name: yawlName(src), Path: dst})
	}
	env := map[string]*string{}
	var files []string
	if node, ok := raw["env_file"]; ok {
		if node.Kind == yaml.ScalarNode {
			files = []string{node.Value}
		}
		for _, e := range node.Content {
			if e.Kind != yaml.ScalarNode {
				var o struct{ Path string }
				e.Decode(&o)
				e = &yaml.Node{Value: o.Path}
			}
			files = append(files, e.Value)
		}
	}
	for _, f := range files {
		body, err := []byte(nil), fmt.Errorf("not read")
		if envFile != nil {
			body, err = envFile(f)
		}
		if err != nil {
			r.warn(n, "env_file %s is not read: %v.", f, err)
			continue
		}
		for _, l := range strings.Split(string(body), "\n") {
			if l = strings.TrimSpace(l); l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			if k, v, ok := strings.Cut(l, "="); ok {
				v := strings.Trim(v, `"'`)
				env[strings.TrimSpace(k)] = &v
			}
		}
	}
	inline, err := environment(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", n, err)
	}
	maps.Copy(env, inline) // environment wins over env_file, as in Compose
	for _, k := range slices.Sorted(maps.Keys(env)) {
		v := env[k]
		if v == nil && r.names.known(k) {
			self := "$" + k
			v = &self
		}
		ref, bare, addr, adb := "", false, "", ""
		if v != nil {
			ref, bare = Reference(*v, dbs)
			addr, adb = r.names.dbAddress(*v)
		}
		switch {
		case k == "PORT":
			r.warn(n, "PORT is left out; Yawl sets it to the service's port.")
		case !keyRe.MatchString(k):
			r.warn(n, "%s is left out; Yawl's variable names are UPPER_SNAKE_CASE.", k)
		case v == nil:
			r.warn(n, "%s has no value here; set it with yawl env set.", k)
		case secrets[*v] != "":
			r.warn(n, "%s is the Compose password of %s and is left out; %s.url carries Yawl's.", k, secrets[*v], secrets[*v])
		case ref != "":
			if bare {
				r.warn(n, "%s names %s; it becomes %s, the whole address. Check the app reads a URL there.", k, strings.TrimSuffix(ref, ".url"), ref)
			}
			r.Vars = append(r.Vars, Var{Service: n, Key: k, Ref: ref})
		case addr != "":
			r.warn(n, "%s uses %s, the address of database %s, which has none; reference %s.url instead.", k, addr, adb, yawlName(adb))
		case r.dbUsed(*v) != "":
			db := r.dbUsed(*v)
			r.warn(n, "%s uses %s's generated credentials and is left out; %s.url carries Yawl's.", k, db, db)
		default:
			val, generated, left := r.names.fill(*v)
			if len(left) > 0 {
				r.warn(n, "%s uses %s, filled in only when the import can ask the panel for the server's address (log in first).", k, strings.Join(left, ", "))
			} else if refs := interpolation.FindAllString(val, -1); len(refs) > 0 {
				r.warn(n, "%s uses %s, set as written; give it its value with yawl env set.", k, strings.Join(refs, ", "))
			}
			r.Vars = append(r.Vars, Var{Service: n, Key: k, Value: val, GeneratedSecret: generated})
		}
	}
	r.Services = append(r.Services, s)
	return nil
}

func Reference(v string, dbs map[string]bool) (ref string, bare bool) {
	for _, d := range slices.Sorted(maps.Keys(dbs)) {
		if v == d {
			return d + ".url", true
		}
		if p, ok := strings.CutPrefix(v, d+":"); ok {
			if _, err := strconv.Atoi(p); err == nil {
				return d + ".url", true
			}
		}
		if strings.Contains(v, "://") && (strings.Contains(v, "@"+d+":") || strings.Contains(v, "@"+d+"/") || strings.HasSuffix(v, "@"+d)) {
			return d + ".url", false
		}
	}
	if u, err := url.Parse(v); err == nil && u.Scheme != "" && dbs[u.Hostname()] {
		return u.Hostname() + ".url", false
	}
	return "", false
}

var keyRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

func ValidKey(k string) bool { return keyRe.MatchString(k) }

var interpolation = regexp.MustCompile(`\$\{[^}]*\}`)

func environment(raw map[string]yaml.Node) (map[string]*string, error) {
	out := map[string]*string{}
	node, ok := raw["environment"]
	if !ok {
		return out, nil
	}
	if node.Kind == yaml.MappingNode {
		var m map[string]*string
		if err := node.Decode(&m); err != nil {
			return nil, fmt.Errorf("environment: %w", err)
		}
		return m, nil
	}
	var list []string
	if err := node.Decode(&list); err != nil {
		return nil, fmt.Errorf("environment: %w", err)
	}
	for _, l := range list {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			out[k] = nil
			continue
		}
		out[k] = &v
	}
	return out, nil
}

func volumes(raw map[string]yaml.Node) ([]string, error) {
	node, ok := raw["volumes"]
	if !ok {
		return nil, nil
	}
	var out []string
	for _, e := range node.Content {
		if e.Kind == yaml.ScalarNode {
			out = append(out, e.Value)
			continue
		}
		var o struct{ Source, Target string }
		if err := e.Decode(&o); err != nil {
			return nil, fmt.Errorf("volumes: %w", err)
		}
		if o.Source == "" { // anonymous, or tmpfs: the path alone, as the short form writes it
			out = append(out, o.Target)
			continue
		}
		out = append(out, o.Source+":"+o.Target)
	}
	return out, nil
}

var notHTTP = map[int]bool{21: true, 22: true, 25: true, 53: true, 110: true, 143: true, 465: true, 587: true, 993: true, 995: true,
	1883: true, 3306: true, 5432: true, 5672: true, 6379: true, 8883: true, 9042: true, 9092: true, 11211: true, 27017: true}

func (r *Result) portsOf(n string, raw map[string]yaml.Node) ([]int, error) {
	node, ok := raw["ports"]
	if !ok {
		return nil, nil
	}
	var out []int
	for _, e := range node.Content {
		target, proto := "", ""
		if e.Kind == yaml.ScalarNode {
			parts := strings.Split(e.Value, ":")
			target, proto, _ = strings.Cut(parts[len(parts)-1], "/")
		} else {
			var o struct{ Target, Protocol string }
			e.Decode(&o)
			target, proto = o.Target, o.Protocol
		}
		if proto == "udp" {
			r.warn(n, "port %s/udp is skipped; Yawl serves HTTP.", target)
			continue
		}
		if first, _, ok := strings.Cut(target, "-"); ok {
			r.warn(n, "publishes the range %s; Yawl serves its first port, %s.", target, first)
			target = first
		}
		p, err := strconv.Atoi(target)
		if err != nil || p < 1 || p > 65535 {
			return nil, fmt.Errorf("ports: %q is not one container port", target)
		}
		out = append(out, p)
	}
	return out, nil
}

func (r *Result) dbUsed(v string) string {
	for _, name := range namesIn(v) {
		if db := r.dbUses[name]; db != "" {
			return db
		}
	}
	return ""
}
