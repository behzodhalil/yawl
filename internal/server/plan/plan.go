package plan

import (
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"

	"github.com/behzodhalil/yawl/internal/spec"
)

type Op string

const (
	OpCreate    Op = "create"
	OpUpdate    Op = "update"
	OpUnchanged Op = "unchanged"
	OpWarn      Op = "warn"
)

type Change struct {
	Op     Op     `json:"op"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

func (c Change) String() string {
	s := fmt.Sprintf("%-8s%-9s%s", c.Op, c.Kind, c.Name)
	if c.Detail != "" {
		s += "  " + c.Detail
	}
	return s
}

func previewDetail(f spec.File) string {
	switch {
	case !f.Preview:
		return "previews off"
	case f.PreviewDomain != "":
		return "previews at *." + f.PreviewDomain
	}
	return "previews on"
}

type Current struct {
	EnvExists     bool
	Server        string
	Preview       bool
	PreviewDomain string
	Services      []spec.Service
}

func Diff(cur Current, want spec.File) []Change {
	var out []Change
	if !cur.EnvExists {
		out = append(out, Change{Op: OpCreate, Kind: "environment", Name: want.Environment, Detail: "on " + want.Server})
	} else if cur.Preview != want.Preview || cur.PreviewDomain != want.PreviewDomain {
		out = append(out, Change{Op: OpUpdate, Kind: "environment", Name: want.Environment, Detail: previewDetail(want)})
	}
	have := map[string]spec.Service{}
	for _, s := range cur.Services {
		have[s.Name] = s
	}
	for _, w := range want.Services {
		h, ok := have[w.Name]
		switch {
		case !ok:
			out = append(out, Change{Op: OpCreate, Kind: "service", Name: w.Name, Detail: volumeNotes(nil, w, open(w, ""))})
		case reflect.DeepEqual(norm(h), norm(w)):
			out = append(out, Change{Op: OpUnchanged, Kind: "service", Name: w.Name})
		default:
			out = append(out, Change{Op: OpUpdate, Kind: "service", Name: w.Name, Detail: volumeNotes(&h, w, open(w, fields(h, w)))})
		}
	}
	return out
}

func norm(s spec.Service) spec.Service {
	s.Replicas = max(s.Replicas, 1)
	if s.PreviewOn() {
		s.Preview = nil
	}
	if len(s.Volumes) == 0 {
		s.Volumes = nil
	}
	for _, l := range []*[]string{&s.Release, &s.Domains, &s.Allow, &s.Command, &s.Watch, &s.Tags} {
		if len(*l) == 0 {
			*l = nil
		}
	}
	return s
}

func volumeNotes(h *spec.Service, w spec.Service, detail string) string {
	add := func(s string) {
		if detail != "" {
			detail += ". "
		}
		detail += s
	}
	if len(w.Volumes) > 0 {
		add(w.Name + " has a volume: it is down for a few seconds while the new version starts.")
	}
	if h != nil {
		for _, v := range h.Volumes {
			if !slices.ContainsFunc(w.Volumes, func(x spec.Volume) bool { return x.Name == v.Name }) {
				add(fmt.Sprintf("%s: volume %s is no longer mounted; its data is kept until %s is deleted", w.Name, v.Name, w.Name))
			}
		}
	}
	return detail
}

func open(s spec.Service, detail string) string {
	if !everyAddress(s.Allow) {
		return detail
	}
	if detail != "" {
		detail += ". "
	}
	return detail + s.Name + " is open to every address."
}

func everyAddress(allow []string) bool {
	var ps []netip.Prefix
	for _, a := range allow {
		if p, err := netip.ParsePrefix(a); err == nil {
			if p.Addr().Is6() {
				if p.Bits() == 0 {
					return true
				}
				continue
			}
			ps = append(ps, p.Masked())
		}
	}
	var sum uint64
	for i, p := range ps {
		if !slices.ContainsFunc(ps[:i], func(q netip.Prefix) bool { return q.Bits() <= p.Bits() && q.Contains(p.Addr()) }) &&
			!slices.ContainsFunc(ps[i+1:], func(q netip.Prefix) bool { return q.Bits() < p.Bits() && q.Contains(p.Addr()) }) {
			sum += 1 << (32 - p.Bits())
		}
	}
	return sum >= 1<<32
}

func fields(a, b spec.Service) string {
	a, b = norm(a), norm(b)
	var d []string
	add := func(name string, x, y any) {
		if !reflect.DeepEqual(x, y) {
			d = append(d, fmt.Sprintf("%s %v -> %v", name, x, y))
		}
	}
	add("kind", a.Kind, b.Kind)
	add("repo", a.Repo, b.Repo)
	add("image", a.Image, b.Image)
	add("branch", a.Branch, b.Branch)
	add("port", a.Port, b.Port)
	add("health_path", a.HealthPath, b.HealthPath)
	add("health_timeout", a.HealthTimeout, b.HealthTimeout)
	add("drain_timeout", a.DrainTimeout, b.DrainTimeout)
	add("auto_deploy", a.AutoDeploy, b.AutoDeploy)
	add("version", a.Version, b.Version)
	add("backup_at", a.BackupAt, b.BackupAt)
	add("release", a.Release, b.Release)
	add("release_timeout", a.ReleaseTimeout, b.ReleaseTimeout)
	add("domains", a.Domains, b.Domains)
	add("outside_port", a.OutsidePort, b.OutsidePort)
	add("allow", a.Allow, b.Allow)
	add("schedule", a.Schedule, b.Schedule)
	add("timezone", a.Timezone, b.Timezone)
	if !reflect.DeepEqual(a.Volumes, b.Volumes) {
		d = append(d, fmt.Sprintf("volumes %v -> %v", a.Volumes, b.Volumes))
	}
	if !reflect.DeepEqual(a.Command, b.Command) {
		d = append(d, fmt.Sprintf("command %q -> %q", a.Command, b.Command))
	}
	add("timeout", a.Timeout, b.Timeout)
	add("cpu", a.CPU, b.CPU)
	add("memory", a.Memory, b.Memory)
	add("pitr", a.PITR, b.PITR)
	add("builder", a.Builder, b.Builder)
	add("root", a.Root, b.Root)
	add("noindex", a.NoIndex, b.NoIndex)
	add("preview", a.PreviewOn(), b.PreviewOn())
	add("www", a.WWW, b.WWW)
	add("watch", a.Watch, b.Watch)
	add("replicas", a.Replicas, b.Replicas)
	add("tags", a.Tags, b.Tags)
	if !reflect.DeepEqual(a.Keep, b.Keep) {
		d = append(d, fmt.Sprintf("keep %s -> %s", a.Keep, b.Keep))
	}
	return strings.Join(d, ", ")
}
