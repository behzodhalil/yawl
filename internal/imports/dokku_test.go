package imports

import (
	"slices"
	"testing"

	"github.com/behzodhalil/yawl/internal/compose"
	"github.com/behzodhalil/yawl/internal/spec"
)

// What `dokku config:export shop` prints: Dokku's own, links, and a value
// with a quote and one over two lines.
const dokkuExport = `export DATABASE_URL='postgres://postgres:9f2c@dokku-postgres-shop:5432/shop'
export DOKKU_APP_RESTORE='1'
export DOKKU_APP_TYPE='herokuish'
export DOKKU_PROXY_PORT='80'
export GIT_REV='2b9d1c4'
export GREETING='it'\''s here'
export MONGO_URL='mongodb://shop:pw@dokku-mongo-catalog:27017/catalog'
export PRIVATE_KEY='-----BEGIN KEY-----
abc
-----END KEY-----'
export REDIS_URL='redis://:pw@dokku-redis-cache:6379'
export SECRET_KEY_BASE='a$b"c'
`

const dokkuStorage = `-----> shop volume bind-mounts:
       /var/lib/dokku/data/storage/shop:/app/storage
       /var/lib/dokku/data/storage/shop-logs:/app/log:ro
`

func TestDokku(t *testing.T) {
	r, err := Dokku([]byte(dokkuExport), []byte(dokkuStorage), "shop")
	if err != nil {
		t.Fatal(err)
	}
	by := byName(r)
	if a := by["shop"]; a.Kind != spec.KindWeb || a.Port != 5000 || a.Image != "" ||
		!slices.Equal(a.Volumes, []spec.Volume{{Name: "storage", Path: "/app/storage"}, {Name: "log", Path: "/app/log"}}) {
		t.Fatalf("app %+v", a)
	}
	for n, k := range map[string]spec.Kind{"shop-postgres": spec.KindPostgres, "catalog": spec.KindMongoDB, "cache": spec.KindValkey} {
		if by[n].Kind != k {
			t.Errorf("%s: %+v", n, by[n])
		}
	}
	want := []compose.Var{
		{Service: "shop", Key: "DATABASE_URL", Ref: "shop-postgres.url"},
		{Service: "shop", Key: "GREETING", Value: "it's here"},
		{Service: "shop", Key: "MONGO_URL", Ref: "catalog.url"},
		{Service: "shop", Key: "PRIVATE_KEY", Value: "-----BEGIN KEY-----\nabc\n-----END KEY-----"},
		{Service: "shop", Key: "REDIS_URL", Ref: "cache.url"},
		{Service: "shop", Key: "SECRET_KEY_BASE", Value: `a$b"c`},
	}
	if !slices.Equal(r.Vars, want) {
		t.Fatalf("vars %+v", r.Vars)
	}
	hasNotes(t, r,
		"shop: volume storage at /app/storage starts empty; copy the data from /var/lib/dokku/data/storage/shop into it once it runs.",
		"shop-postgres: the data is not moved; restore a dump into shop-postgres once it runs (yawl db import).",
	)
	if _, err := Dokku([]byte("export BROKEN='no end\n"), nil, "shop"); err == nil {
		t.Fatal("an open quote was read")
	}
	if r, err := Dokku([]byte("APP_ENV='prod' COMPILE_ASSETS='1'\n"), nil, "blog"); err != nil || len(r.Vars) != 2 || r.Vars[0].Value != "prod" {
		t.Fatalf("%v %+v", err, r.Vars)
	}
}
