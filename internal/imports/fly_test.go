package imports

import (
	"slices"
	"testing"

	"github.com/behzodhalil/yawl/internal/compose"
	"github.com/behzodhalil/yawl/internal/spec"
)

const flyTOML = `
app = "shop"
primary_region = "ams"
kill_signal = "SIGINT"

[build]
  dockerfile = "Dockerfile"

[deploy]
  release_command = "bin/rails db:migrate"
  strategy = "rolling"

[env]
  RAILS_ENV = "production"
  PORT = "3000"
  WEB_CONCURRENCY = 2

[processes]
  app = "bin/rails server -b 0.0.0.0"
  worker = "bundle exec sidekiq"

[http_service]
  internal_port = 3000
  force_https = true
  auto_stop_machines = "stop"
  processes = ["app"]

  [[http_service.checks]]
    grace_period = "10s"
    interval = "30s"
    method = "GET"
    path = "/up"

[[mounts]]
  source = "shop_data"
  destination = "/data"
  processes = ["app"]

[[vm]]
  size = "shared-cpu-1x"
`

func TestFly(t *testing.T) {
	r, err := Fly([]byte(flyTOML))
	if err != nil {
		t.Fatal(err)
	}
	by := byName(r)
	web := by["shop"]
	if web.Kind != spec.KindWeb || web.Port != 3000 || web.HealthPath != "/up" || web.Image != "" ||
		!slices.Equal(web.Command, []string{"sh", "-c", "bin/rails server -b 0.0.0.0"}) ||
		!slices.Equal(web.Release, []string{"sh", "-c", "bin/rails db:migrate"}) ||
		!slices.Equal(web.Volumes, []spec.Volume{{Name: "shop-data", Path: "/data"}}) {
		t.Fatalf("shop %+v", web)
	}
	w := by["shop-worker"]
	if w.Kind != spec.KindWorker || !slices.Equal(w.Command, []string{"sh", "-c", "bundle exec sidekiq"}) || len(w.Release) > 0 || len(w.Volumes) > 0 {
		t.Fatalf("worker %+v", w)
	}
	for _, want := range []compose.Var{
		{Service: "shop", Key: "RAILS_ENV", Value: "production"}, {Service: "shop", Key: "WEB_CONCURRENCY", Value: "2"},
		{Service: "shop-worker", Key: "RAILS_ENV", Value: "production"},
	} {
		if !slices.Contains(r.Vars, want) {
			t.Errorf("no var %+v in %+v", want, r.Vars)
		}
	}
	hasNotes(t, r,
		"shop: PORT is left out; Yawl sets it to the service's port.",
		"shop: volume shop-data at /data starts empty; copy the data from Fly volume shop_data into it once it runs.",
		"shop: primary_region is not carried; Yawl runs on your server.",
		"shop: kill_signal is not carried; Yawl does not read it.",
		"shop: vm is not carried; Yawl runs on your server; set cpu and memory in yawl.yaml.",
		"secrets, Fly Postgres, regions and machine sizes are not in fly.toml; list the secrets with fly secrets list and set each with yawl env set.",
	)
}

func TestFlyImageAndServices(t *testing.T) {
	r, err := Fly([]byte(`app = "hello"
[build]
  image = "flyio/hellofly:latest"
[mounts]
  source = "data"
  destination = "/var/lib/hello"
[[services]]
  internal_port = 8080
  protocol = "tcp"
  [[services.http_checks]]
    path = "/health"
`))
	if err != nil {
		t.Fatal(err)
	}
	if s := byName(r)["hello"]; s.Kind != spec.KindWeb || s.Port != 8080 || s.Image != "flyio/hellofly:latest" || s.HealthPath != "/health" ||
		len(s.Command) > 0 || !slices.Equal(s.Volumes, []spec.Volume{{Name: "data", Path: "/var/lib/hello"}}) {
		t.Fatalf("%+v", r.Services)
	}
	r, err = Fly([]byte(`app = "cruncher"`))
	if err != nil || len(r.Services) != 1 || r.Services[0].Kind != spec.KindWorker {
		t.Fatalf("%v %+v", err, r.Services)
	}
	if _, err := Fly([]byte(`primary_region = "ams"`)); err == nil {
		t.Fatal("a fly.toml with no app was read")
	}
}
