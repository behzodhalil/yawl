package imports

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/behzodhalil/yawl/internal/compose"
	"github.com/behzodhalil/yawl/internal/spec"
)

var dokkuHosts = []struct {
	prefix string
	kind   spec.Kind
}{
	{"dokku-postgres-", spec.KindPostgres}, {"dokku-mysql-", spec.KindMySQL}, {"dokku-mariadb-", spec.KindMariaDB},
	{"dokku-mongo-", spec.KindMongoDB}, {"dokku-redis-", spec.KindValkey},
}

func Dokku(exports, storage []byte, name string) (compose.Result, error) {
	env, keys, err := exportLines(string(exports))
	if err != nil {
		return compose.Result{}, err
	}
	var r compose.Result
	app := spec.Service{Name: name, Kind: spec.KindWeb, Port: 5000}
	warn(&r, name, "port 5000 is Dokku's default for buildpack apps; an app from a Dockerfile may listen on another, so check port in yawl.yaml.")
	dbs := map[string]string{}
	taken := map[string]bool{name: true}
	for _, k := range keys {
		v := env[k]
		if strings.HasPrefix(k, "DOKKU_") || k == "GIT_REV" {
			continue
		}
		if u, err := url.Parse(v); err == nil && u.Scheme != "" {
			if db, ok := dokkuDatabase(&r, u.Hostname(), dbs, taken); ok {
				add(&r, compose.Var{Service: name, Key: k, Ref: db + ".url"}, nil)
				continue
			}
		}
		add(&r, compose.Var{Service: name, Key: k, Value: v}, nil)
	}
	for _, l := range strings.Split(string(storage), "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "/") { // the "-----> app volume bind-mounts:" header
			continue
		}
		host, rest, _ := strings.Cut(l, ":")
		dst, _, _ := strings.Cut(rest, ":") // a mode, like :ro
		if dst == "" {
			continue
		}
		v := spec.Volume{Name: volumeName(path.Base(dst)), Path: dst}
		app.Volumes = append(app.Volumes, v)
		emptyVolume(&r, name, v, host)
	}
	r.Services = append([]spec.Service{app}, r.Services...)
	return done(r)
}

func dokkuDatabase(r *compose.Result, host string, dbs map[string]string, taken map[string]bool) (string, bool) {
	if db, ok := dbs[host]; ok {
		return db, true
	}
	for _, h := range dokkuHosts {
		n, ok := strings.CutPrefix(host, h.prefix)
		if !ok || n == "" {
			continue
		}
		if taken[n] {
			n += "-" + string(h.kind)
		}
		taken[n], dbs[host] = true, n
		database(r, n, h.kind, 0)
		return n, true
	}
	return "", false
}

// exportLines reads `export KEY='value'` lines as a shell would: quotes,
// '\” for a quote inside, and values over several lines. Without
// "export", the --format shell form reads the same.
func exportLines(s string) (map[string]string, []string, error) {
	env := map[string]string{}
	var keys []string
	i := 0
	space := func() {
		for i < len(s) && strings.ContainsRune(" \t\r\n;", rune(s[i])) {
			i++
		}
	}
	for {
		space()
		if i >= len(s) {
			return env, keys, nil
		}
		if s[i] == '#' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		if strings.HasPrefix(s[i:], "export ") {
			i += len("export ")
			space()
		}
		eq := strings.IndexByte(s[i:], '=')
		if eq < 0 {
			return nil, nil, fmt.Errorf("config:export: %q is not KEY='value'", strings.SplitN(s[i:], "\n", 2)[0])
		}
		k := s[i : i+eq]
		if strings.ContainsAny(k, " \t\n") {
			return nil, nil, fmt.Errorf("config:export: %q is not a variable name", k)
		}
		i += eq + 1
		var v strings.Builder
	word:
		for i < len(s) {
			switch c := s[i]; c {
			case ' ', '\t', '\r', '\n', ';':
				break word
			case '\'':
				end := strings.IndexByte(s[i+1:], '\'')
				if end < 0 {
					return nil, nil, errors.New("config:export: a quote is not closed")
				}
				v.WriteString(s[i+1 : i+1+end])
				i += end + 2
			case '"':
				i++
				for i < len(s) && s[i] != '"' {
					if s[i] == '\\' && i+1 < len(s) && strings.ContainsRune(`"\$`+"`", rune(s[i+1])) {
						i++
					}
					v.WriteByte(s[i])
					i++
				}
				if i >= len(s) {
					return nil, nil, errors.New("config:export: a quote is not closed")
				}
				i++
			case '\\':
				if i+1 < len(s) {
					v.WriteByte(s[i+1])
				}
				i += 2
			default:
				v.WriteByte(c)
				i++
			}
		}
		if _, ok := env[k]; !ok {
			keys = append(keys, k)
		}
		env[k] = v.String()
	}
}
