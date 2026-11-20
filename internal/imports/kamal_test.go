package imports

import (
	"slices"
	"testing"

	"github.com/behzodhalil/yawl/internal/compose"
	"github.com/behzodhalil/yawl/internal/spec"
)

const kamalYAML = `
service: shop
image: acme/shop

servers:
  web:
    - 192.168.0.1
  job:
    hosts:
      - 192.168.0.1
    cmd: bin/jobs --queue 'default'
    env:
      clear:
        JOB_CONCURRENCY: 3

proxy:
  ssl: true
  host: shop.acme.uz
  app_port: 3000
  healthcheck:
    path: /healthz

registry:
  username: acme
  password:
    - KAMAL_REGISTRY_PASSWORD

builder:
  arch: amd64

env:
  clear:
    RAILS_ENV: production
    DATABASE_URL: postgres://shop@shop-db:5432/shop_production
    REDIS_URL: redis://shop-redis:6379/0
    SEARCH_URL: http://shop-search:7700
  secret:
    - RAILS_MASTER_KEY
    - DB_PASSWORD:MAIN_DB_PASSWORD

volumes:
  - "shop_storage:/rails/storage"
  - "/srv/uploads:/rails/public/uploads:ro"

asset_path: /rails/public/assets

accessories:
  db:
    image: postgres:16
    host: 192.168.0.2
    port: "127.0.0.1:5432:5432"
    env:
      clear:
        POSTGRES_USER: shop
      secret:
        - POSTGRES_PASSWORD
    directories:
      - data:/var/lib/postgresql/data
  redis:
    image: valkey/valkey:8
    host: 192.168.0.2
  search:
    image: getmeili/meilisearch:v1.10
    host: 192.168.0.2
`

func TestKamal(t *testing.T) {
	r, err := Kamal([]byte(kamalYAML))
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(r.Services, func(s spec.Service) bool { return s.Kind == spec.KindWeb }); r.Services[i+1].Name != "shop-job" {
		t.Errorf("the web role is not first: %+v", r.Services)
	}
	by := byName(r)
	vols := []spec.Volume{{Name: "shop-storage", Path: "/rails/storage"}, {Name: "uploads", Path: "/rails/public/uploads"}}
	web := by["shop"]
	if web.Kind != spec.KindWeb || web.Port != 3000 || web.HealthPath != "/healthz" || web.Image != "" || len(web.Command) > 0 ||
		!slices.Equal(web.Domains, []string{"shop.acme.uz"}) || !slices.Equal(web.Volumes, vols) {
		t.Fatalf("web %+v", web)
	}
	job := by["shop-job"]
	if job.Kind != spec.KindWorker || !slices.Equal(job.Command, []string{"sh", "-c", "bin/jobs --queue 'default'"}) || len(job.Volumes) > 0 {
		t.Fatalf("job %+v", job)
	}
	if d := by["shop-db"]; d.Kind != spec.KindPostgres || d.Version != 16 {
		t.Fatalf("db %+v", d)
	}
	if d := by["shop-redis"]; d.Kind != spec.KindValkey {
		t.Fatalf("redis %+v", d)
	}
	if _, ok := by["shop-search"]; ok {
		t.Fatal("meilisearch was carried")
	}
	for _, want := range []compose.Var{
		{Service: "shop", Key: "RAILS_ENV", Value: "production"},
		{Service: "shop", Key: "DATABASE_URL", Ref: "shop-db.url"},
		{Service: "shop", Key: "REDIS_URL", Ref: "shop-redis.url"},
		{Service: "shop", Key: "SEARCH_URL", Value: "http://shop-search:7700"},
		{Service: "shop-job", Key: "DATABASE_URL", Ref: "shop-db.url"},
		{Service: "shop-job", Key: "JOB_CONCURRENCY", Value: "3"},
	} {
		if !slices.Contains(r.Vars, want) {
			t.Errorf("no var %+v in %+v", want, r.Vars)
		}
	}
	if _, ok := varOf(r, "shop", "JOB_CONCURRENCY"); ok {
		t.Error("a role's env reached another role")
	}
	hasNotes(t, r,
		"shop: RAILS_MASTER_KEY is a Kamal secret; set it with yawl env set shop RAILS_MASTER_KEY.",
		"shop: DB_PASSWORD is a Kamal secret; set it with yawl env set shop DB_PASSWORD.",
		"shop-job: RAILS_MASTER_KEY is a Kamal secret; set it with yawl env set shop-job RAILS_MASTER_KEY.",
		".kamal/secrets is not read: it may run commands.",
		"shop-db: the data is not moved; restore a dump into shop-db once it runs (yawl db import).",
		"shop-db: POSTGRES_PASSWORD is not carried; Yawl makes the database's own password.",
		"shop-db: POSTGRES_USER is not carried; Yawl sets the database up itself.",
		"shop-search: accessory search (getmeili/meilisearch:v1.10) is not carried; add it to yawl.yaml by hand.",
		"shop: volume uploads at /rails/public/uploads starts empty; copy the data from /srv/uploads into it once it runs.",
		"shop-job: volume shop-storage is on shop only; Yawl does not share a volume between services.",
		"shop: registry is not carried; Yawl builds from the repository and reaches the server itself.",
		"shop: builder is not carried; Yawl builds from the repository and reaches the server itself.",
		"shop: image is not carried; Yawl builds from the repository and reaches the server itself.",
		"shop: asset_path is not carried; Yawl does not read it.",
	)
}

func TestKamalHostsList(t *testing.T) {
	r, err := Kamal([]byte("service: blog\nimage: acme/blog\nservers:\n  - 10.0.0.5\nenv:\n  SITE_NAME: Blog\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s := byName(r)["blog"]; len(r.Services) != 1 || s.Kind != spec.KindWeb || s.Port != 80 || s.HealthPath != "/up" {
		t.Fatalf("%+v", r.Services)
	}
	if !slices.Contains(r.Vars, compose.Var{Service: "blog", Key: "SITE_NAME", Value: "Blog"}) {
		t.Fatalf("%+v", r.Vars)
	}
}

func TestKamalVersionNote(t *testing.T) {
	r, err := Kamal([]byte("service: blog\nimage: acme/blog\nservers:\n  - 10.0.0.5\naccessories:\n  db:\n    image: mysql:5.7\n    host: 10.0.0.5\n"))
	if err != nil {
		t.Fatal(err)
	}
	hasNotes(t, r, "blog-db: mysql:5.7 runs as version 8, the one Yawl runs; check the app works with it.")
}

func TestKamalRenames(t *testing.T) {
	r, err := Kamal([]byte("service: my_blog\nimage: acme/blog\nservers:\n  - 10.0.0.5\nenv:\n  clear:\n    DATABASE_URL: postgres://u:p@my_blog-db:5432/app\naccessories:\n  db:\n    image: postgres:17\n    host: 10.0.0.5\n"))
	if err != nil {
		t.Fatal(err)
	}
	by := byName(r)
	if _, ok := by["my-blog"]; !ok {
		t.Fatalf("%+v", r.Services)
	}
	if !slices.Contains(r.Vars, compose.Var{Service: "my-blog", Key: "DATABASE_URL", Ref: "my-blog-db.url"}) {
		t.Fatalf("%+v", r.Vars)
	}
	hasNotes(t, r, "my_blog is named my-blog; Yawl's names are lowercase letters, digits and dashes.")
}
