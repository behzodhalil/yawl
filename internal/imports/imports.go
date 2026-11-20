package imports

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/behzodhalil/yawl/internal/compose"
	"github.com/behzodhalil/yawl/internal/spec"
)

func warn(r *compose.Result, service, format string, a ...any) {
	r.Warnings = append(r.Warnings, service+": "+fmt.Sprintf(format, a...))
}

func add(r *compose.Result, v compose.Var, dbs map[string]bool) {
	switch {
	case v.Key == "PORT":
		warn(r, v.Service, "PORT is left out; Yawl sets it to the service's port.")
		return
	case !compose.ValidKey(v.Key):
		warn(r, v.Service, "%s is left out; Yawl's variable names are UPPER_SNAKE_CASE.", v.Key)
		return
	}
	if v.Ref == "" && !v.GeneratedSecret && dbs != nil {
		if ref, bare := compose.Reference(v.Value, dbs); ref != "" {
			if bare {
				warn(r, v.Service, "%s names %s; it becomes %s, the whole address. Check the app reads a URL there.", v.Key, strings.TrimSuffix(ref, ".url"), ref)
			}
			v.Value, v.Ref = "", ref
		}
	}
	r.Vars = append(r.Vars, v)
}

func shell(s string) []string {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return []string{"sh", "-c", s}
}

func volumeName(s string) string {
	b := []byte(strings.ToLower(path.Base(s)))
	for i, c := range b {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			b[i] = '-'
		}
	}
	n := strings.Trim(string(b), "-")
	if len(n) > 31 {
		n = strings.TrimRight(n[:31], "-")
	}
	if n == "" {
		return "data"
	}
	return n
}

func replicas(r *compose.Result, service string, n int) int {
	if n > 10 {
		warn(r, service, "runs %d copies; Yawl runs at most 10.", n)
		return 10
	}
	if n < 2 {
		return 0
	}
	return n
}

func domains(r *compose.Result, service string, hosts []string) []string {
	var out []string
	for _, h := range hosts {
		if h = strings.ToLower(strings.TrimSpace(h)); spec.ValidHost(h) && !slices.Contains(out, h) {
			out = append(out, h)
		} else if h != "" {
			warn(r, service, "domain %s is not carried; Yawl takes plain host names.", h)
		}
	}
	return out
}

func database(r *compose.Result, name string, kind spec.Kind, version int) {
	s := spec.Service{Name: name, Kind: kind, Version: version}
	if kind == spec.KindValkey {
		on := true
		s.Backup = &on
	}
	r.Services = append(r.Services, s)
	warn(r, name, "the data is not moved; restore a dump into %s once it runs (yawl db import).", name)
}

func emptyVolume(r *compose.Result, service string, v spec.Volume, from string) {
	warn(r, service, "volume %s at %s starts empty; copy the data from %s into it once it runs.", v.Name, v.Path, from)
}

func notCarried(r *compose.Result, service string, keys []string, read map[string]bool, why map[string]string) {
	for _, k := range keys {
		if read[k] {
			continue
		}
		w := why[k]
		if w == "" {
			w = "Yawl does not read it."
		}
		warn(r, service, "%s is not carried; %s", k, w)
	}
}

func done(r compose.Result) (compose.Result, error) {
	if err := compose.Rename(&r); err != nil {
		return compose.Result{}, err
	}
	slices.SortStableFunc(r.Vars, func(a, b compose.Var) int {
		return strings.Compare(a.Service+"\x00"+a.Key, b.Service+"\x00"+b.Key)
	})
	return r, nil
}

func set(keys ...string) map[string]bool {
	m := map[string]bool{}
	for _, k := range keys {
		m[k] = true
	}
	return m
}

func keysOf[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
