package spec

import (
	"strings"
	"testing"
)

func TestImageService(t *testing.T) {
	base := "project: acme\nenvironment: production\nserver: hel1\nservices:\n"
	f, err := Parse([]byte(base + "  - {name: grafana, kind: web, image: grafana/grafana:11}\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := f.Services[0]
	if s.Image != "grafana/grafana:11" || s.Branch != "" || s.AutoDeploy || s.Port != 10000 || s.HealthPath != "/" {
		t.Fatalf("%+v", s)
	}
	for _, y := range []string{
		"  - {name: w, kind: worker, image: ghcr.io/acme/jobs:1}\n",
		"  - {name: c, kind: cron, image: registry.acme.uz:5000/report@sha256:" + strings.Repeat("a", 64) + ", schedule: \"0 3 * * *\", command: [report]}\n",
		// Docker's grammar allows __ and runs of dashes in a path
		"  - {name: w, kind: worker, image: acme/foo__bar:1}\n",
		"  - {name: w, kind: worker, image: acme/foo--bar}\n",
		"  - {name: w, kind: worker, image: my-registry.acme.uz:5000/a.b_c/d}\n",
	} {
		if _, err := Parse([]byte(base + y)); err != nil {
			t.Errorf("%s: %v", y, err)
		}
	}
}

func TestImageRejects(t *testing.T) {
	base := "project: acme\nenvironment: production\nserver: hel1\nservices:\n"
	for y, want := range map[string]string{
		"  - {name: grafana, kind: web, repo: https://x/y, image: grafana/grafana:11}\n": `grafana: "repo" and "image" cannot both be set`,
		"  - {name: acme-db, kind: postgres, image: postgres:17}\n":                      `acme-db: a database has no "image"`,
		"  - {name: grafana, kind: web, image: \"Grafana Latest\"}\n":                    `"image"`,
		"  - {name: grafana, kind: web, image: -a.b/x}\n":                                `"image"`,
		"  - {name: grafana, kind: web, image: a-.b/x}\n":                                `"image"`,
		"  - {name: grafana, kind: web, image: foo___bar}\n":                             `"image"`,
		"  - {name: grafana, kind: web, image: foo._bar}\n":                              `"image"`,
		"  - {name: api, kind: web}\n":                                                   `"repo"`,
	} {
		if _, err := Parse([]byte(base + y)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", y, want, err)
		}
	}
}

func TestRegistryHost(t *testing.T) {
	for ref, want := range map[string]string{"grafana/grafana:11": "docker.io", "nginx": "docker.io", "ghcr.io/acme/api:1": "ghcr.io",
		"registry.acme.uz:5000/api:1": "registry.acme.uz:5000", "localhost:5000/x": "localhost:5000", "localhost/x": "localhost"} {
		if got := RegistryHost(ref); got != want {
			t.Errorf("%s: %s", ref, got)
		}
	}
}

func TestImageName(t *testing.T) {
	for ref, want := range map[string]string{"grafana/grafana:11": "grafana/grafana", "nginx": "nginx", "registry.acme.uz:5000/api:1": "registry.acme.uz:5000/api",
		"ghcr.io/acme/api@sha256:" + strings.Repeat("a", 64): "ghcr.io/acme/api", "localhost:5000/x": "localhost:5000/x"} {
		if got := ImageName(ref); got != want {
			t.Errorf("%s: %s", ref, got)
		}
	}
}
