package spec

import (
	"reflect"
	"strings"
	"testing"
)

func TestMarshalRoundTrip(t *testing.T) {
	in := `project: acme
environment: production
server: hel1
preview: true
preview_domain: preview.acme.uz
services:
  - name: api
    kind: web
    repo: https://github.com/acme/api
    port: 8080
    auto_deploy: false
    drain_timeout: 45s
    release: [/app/migrate, up]
    domains: [api.acme.uz]
    tags: [front]
    memory: 512MB
    cpu: 0.5
    volumes: [{name: uploads, path: /u}]
    keep: {daily: 14, weekly: 2}
  - {name: db, kind: postgres, version: 17, pitr: true, backup_at: "03:00"}
  - {name: jobs, kind: worker, image: ghcr.io/acme/jobs:1, replicas: 3}
  - {name: report, kind: cron, image: ghcr.io/acme/report:1, schedule: "0 3 * * *", command: [report, --all]}
`
	f, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !reflect.DeepEqual(f, back) {
		t.Fatalf("round trip differs:\n%s\n%+v\n%+v", out, f.Services[0], back.Services[0])
	}
	for _, want := range []string{"auto_deploy: false", "drain_timeout: 45s", "keep:", "preview: true", "preview_domain: preview.acme.uz"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "outside_port") || strings.Contains(string(out), "noindex") {
		t.Errorf("empty fields written:\n%s", out)
	}
}
