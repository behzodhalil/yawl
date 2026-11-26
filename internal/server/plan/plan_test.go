package plan

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/spec"
)

func svc(name string, port int) spec.Service {
	return spec.Service{Name: name, Kind: spec.KindWeb, Repo: "https://x/y", Branch: "main", Port: port,
		HealthPath: "/", HealthTimeout: time.Minute, DrainTimeout: 30 * time.Second}
}

func TestDiff(t *testing.T) {
	cur := Current{EnvExists: true, Server: "hel1", Services: []spec.Service{svc("api", 10000)}}
	want := spec.File{Project: "acme", Environment: "production", Server: "hel1", Services: []spec.Service{svc("api", 8080), svc("web", 10000)}}
	got := Diff(cur, want)
	exp := []Change{
		{Op: "update", Kind: "service", Name: "api", Detail: "port 10000 -> 8080"},
		{Op: "create", Kind: "service", Name: "web"},
	}
	if !slices.Equal(got, exp) {
		t.Fatalf("got %+v", got)
	}
}

func TestDiffNewEnvironment(t *testing.T) {
	got := Diff(Current{}, spec.File{Project: "acme", Environment: "staging", Server: "hk1", Services: []spec.Service{svc("api", 10000)}})
	if got[0] != (Change{Op: "create", Kind: "environment", Name: "staging", Detail: "on hk1"}) || got[1].Op != "create" {
		t.Fatalf("got %+v", got)
	}
}

func TestDiffUnchanged(t *testing.T) {
	cur := Current{EnvExists: true, Server: "hel1", Services: []spec.Service{svc("api", 10000)}}
	got := Diff(cur, spec.File{Environment: "production", Server: "hel1", Services: []spec.Service{svc("api", 10000)}})
	if len(got) != 1 || got[0].Op != "unchanged" {
		t.Fatalf("got %+v", got)
	}
}

func TestFormat(t *testing.T) {
	if s := (Change{Op: "create", Kind: "service", Name: "web"}).String(); s != "create  service  web" {
		t.Fatalf("%q", s)
	}
}

func TestPlanShowsAutoDeploy(t *testing.T) {
	on := svc("api", 10000)
	on.AutoDeploy = true
	off := on
	off.AutoDeploy = false
	got := Diff(Current{EnvExists: true, Services: []spec.Service{on}}, spec.File{Services: []spec.Service{off}})
	if len(got) != 1 || got[0].Detail != "auto_deploy true -> false" {
		t.Fatalf("got %+v", got)
	}
}

func TestPlanShowsSlices(t *testing.T) {
	a, b := svc("api", 10000), svc("api", 10000)
	a.Release, b.Release = []string{"a"}, []string{"a", "b"}
	got := Diff(Current{EnvExists: true, Services: []spec.Service{a}}, spec.File{Services: []spec.Service{b}})
	if len(got) != 1 || got[0].Detail != "release [a] -> [a b]" {
		t.Fatalf("got %+v", got)
	}
}

func TestPlanWarnsOpen(t *testing.T) {
	db := spec.Service{Name: "acme-db", Kind: spec.KindPostgres, Version: 17, BackupAt: "03:00", OutsidePort: 15432, Allow: []string{"0.0.0.0/0"}}
	for _, cur := range []Current{{EnvExists: true}, {EnvExists: true, Services: []spec.Service{{Name: "acme-db", Kind: spec.KindPostgres, Version: 17, BackupAt: "03:00"}}}} {
		got := Diff(cur, spec.File{Services: []spec.Service{db}})
		if len(got) != 1 || !strings.HasSuffix(got[0].Detail, "acme-db is open to every address.") {
			t.Fatalf("got %+v", got)
		}
	}
}

func TestCronFieldsInPlan(t *testing.T) {
	a := spec.Service{Name: "report", Kind: spec.KindCron, Schedule: "0 3 * * *", Command: []string{"x"}}
	b := a
	b.Schedule, b.Command = "0 4 * * *", []string{"y"}
	if got := fields(a, b); got != `schedule 0 3 * * * -> 0 4 * * *, command ["x"] -> ["y"]` {
		t.Fatalf("%q", got)
	}
	a.Command, b.Command = nil, []string{}
	b.Schedule = a.Schedule
	if got := fields(a, b); got != "" {
		t.Fatalf("empty and no command differ: %q", got)
	}
}

func TestCronCommandQuoted(t *testing.T) {
	a := spec.Service{Name: "r", Kind: spec.KindCron, Command: []string{"sh", "-c", "pg_dump | gzip"}}
	b := a
	b.Command = []string{"sh", "-c", "pg_dump", "|", "gzip"}
	if got := fields(a, b); got != `command ["sh" "-c" "pg_dump | gzip"] -> ["sh" "-c" "pg_dump" "|" "gzip"]` {
		t.Fatalf("%q", got)
	}
}

func TestPlanSaysDowntime(t *testing.T) {
	vol := []spec.Volume{{Name: "uploads", Path: "/app/uploads"}}
	w := spec.Service{Name: "api", Kind: spec.KindWeb, Volumes: vol}
	f := spec.File{Environment: "production", Server: "hel1", Services: []spec.Service{w}}
	ch := Diff(Current{EnvExists: true}, f)
	if !strings.Contains(ch[0].Detail, "api has a volume: it is down for a few seconds while the new version starts.") {
		t.Fatalf("%+v", ch)
	}
	h := w
	w2 := w
	w2.Volumes = nil
	ch = Diff(Current{EnvExists: true, Services: []spec.Service{h}}, spec.File{Environment: "production", Server: "hel1", Services: []spec.Service{w2}})
	if !strings.Contains(ch[0].Detail, "api: volume uploads is no longer mounted; its data is kept until api is deleted") {
		t.Fatalf("%+v", ch)
	}
}

func TestDiffLimits(t *testing.T) {
	a, b := svc("api", 10000), svc("api", 10000)
	a.CPU, b.Memory = 0.5, 512e6
	got := Diff(Current{EnvExists: true, Server: "hel1", Services: []spec.Service{a}}, spec.File{Project: "acme", Environment: "production", Server: "hel1", Services: []spec.Service{b}})
	if len(got) != 1 || got[0].Detail != "cpu 0.5 -> none, memory none -> 512MB" {
		t.Fatalf("%+v", got)
	}
}

func TestDiffImage(t *testing.T) {
	a, b := svc("grafana", 10000), svc("grafana", 10000)
	a.Repo, b.Repo, a.Image, b.Image = "", "", "grafana/grafana:10", "grafana/grafana:11"
	got := Diff(Current{EnvExists: true, Server: "hel1", Services: []spec.Service{a}}, spec.File{Project: "acme", Environment: "production", Server: "hel1", Services: []spec.Service{b}})
	if len(got) != 1 || got[0].Detail != "image grafana/grafana:10 -> grafana/grafana:11" {
		t.Fatalf("%+v", got)
	}
}

func TestPlanShowsReplicas(t *testing.T) {
	one, three := svc("api", 10000), svc("api", 10000)
	three.Replicas = 3
	if got := Diff(Current{EnvExists: true, Services: []spec.Service{one}}, spec.File{Services: []spec.Service{three}}); got[0].Detail != "replicas 1 -> 3" {
		t.Fatalf("got %+v", got)
	}
	written := svc("api", 10000)
	written.Replicas = 1
	if got := Diff(Current{EnvExists: true, Services: []spec.Service{one}}, spec.File{Services: []spec.Service{written}}); got[0].Op != "unchanged" {
		t.Fatalf("got %+v", got)
	}
}

func TestPlanShowsTags(t *testing.T) {
	a, b := svc("api", 10000), svc("api", 10000)
	b.Tags = []string{"frontend"}
	if got := Diff(Current{EnvExists: true, Services: []spec.Service{a}}, spec.File{Services: []spec.Service{b}}); got[0].Detail != "tags [] -> [frontend]" {
		t.Fatalf("got %+v", got)
	}
	b.Tags = []string{}
	if got := Diff(Current{EnvExists: true, Services: []spec.Service{a}}, spec.File{Services: []spec.Service{b}}); got[0].Op != "unchanged" {
		t.Fatalf("got %+v", got)
	}
}

func TestPlanShowsKeep(t *testing.T) {
	a := spec.Service{Name: "db", Kind: spec.KindPostgres}
	b := a
	b.Keep = &spec.Keep{Daily: 14, Weekly: 8, Monthly: 6}
	if got := Diff(Current{EnvExists: true, Services: []spec.Service{a}}, spec.File{Services: []spec.Service{b}}); got[0].Detail != "keep 7 daily, 4 weekly -> 14 daily, 8 weekly, 6 monthly" {
		t.Fatalf("got %+v", got)
	}
}

func TestEveryAddressSplit(t *testing.T) {
	for _, c := range []struct {
		allow []string
		want  bool
	}{
		{[]string{"0.0.0.0/0"}, true},
		{[]string{"0.0.0.0/1", "128.0.0.0/1"}, true},
		{[]string{"0.0.0.0/1", "128.0.0.0/2", "192.0.0.0/2", "10.0.0.0/8"}, true},
		{[]string{"0.0.0.0/1", "0.0.0.0/1"}, false},
		{[]string{"0.0.0.0/1", "128.0.0.0/2"}, false},
		{[]string{"203.0.113.0/24"}, false},
	} {
		if got := everyAddress(c.allow); got != c.want {
			t.Errorf("%v: %v", c.allow, got)
		}
	}
}

func TestPlanWarnsOpenSplit(t *testing.T) {
	db := spec.Service{Name: "acme-db", Kind: spec.KindPostgres, Version: 17, BackupAt: "03:00", OutsidePort: 15432, Allow: []string{"0.0.0.0/1", "128.0.0.0/1"}}
	if got := Diff(Current{EnvExists: true}, spec.File{Services: []spec.Service{db}}); len(got) != 1 || !strings.HasSuffix(got[0].Detail, "acme-db is open to every address.") {
		t.Fatalf("got %+v", got)
	}
}

func TestDiffPreview(t *testing.T) {
	want := spec.File{Environment: "staging", Preview: true, PreviewDomain: "preview.acme.uz"}
	got := Diff(Current{EnvExists: true, Preview: true}, want)
	if len(got) != 1 || got[0].String() != "update  environmentstaging  previews at *.preview.acme.uz" {
		t.Fatalf("%q", got)
	}
	if got := Diff(Current{EnvExists: true, Preview: true, PreviewDomain: "preview.acme.uz"}, want); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestNeedsDeploy(t *testing.T) {
	base := spec.Service{Name: "web", Kind: spec.KindWeb, Repo: "https://x/web", Port: 8080}
	routed := base
	routed.Domains, routed.WWW, routed.NoIndex, routed.AutoDeploy, routed.Tags = []string{"a.uz"}, "bare", true, true, []string{"x"}
	if NeedsDeploy(base, routed) {
		t.Fatal("routes, auto-deploy and tags apply without a deploy")
	}
	ported := base
	ported.Port = 9000
	if !NeedsDeploy(base, ported) {
		t.Fatal("a new port waits for a deploy")
	}
}
