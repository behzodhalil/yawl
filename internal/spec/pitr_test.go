package spec

import (
	"strings"
	"testing"
)

func TestPITRSpec(t *testing.T) {
	f, err := Parse([]byte(vbase + "  - {name: db, kind: postgres, pitr: true}\n"))
	if err != nil || !f.Services[0].PITR {
		t.Fatalf("%v %+v", err, f.Services)
	}
	if _, err := Parse([]byte(vbase + "  - {name: cache, kind: valkey, pitr: true}\n")); err == nil || !strings.Contains(err.Error(), `cache: "pitr" is for Postgres only`) {
		t.Fatalf("%v", err)
	}
}

func TestBuilderSpec(t *testing.T) {
	f, err := Parse([]byte(vbase + "  - {name: shop, kind: web, repo: https://github.com/a/b, port: 8080, builder: railpack}\n"))
	if err != nil || f.Services[0].Builder != "railpack" {
		t.Fatalf("%v %+v", err, f.Services)
	}
	if _, err := Parse([]byte(vbase + "  - {name: db, kind: postgres, builder: railpack}\n")); err == nil || !strings.Contains(err.Error(), `db: "builder" is for apps built from a repo`) {
		t.Fatalf("%v", err)
	}
	if _, err := Parse([]byte(vbase + "  - {name: shop, kind: web, repo: https://github.com/a/b, port: 8080, builder: heroku}\n")); err == nil || !strings.Contains(err.Error(), `shop: "builder" is railpack or left out`) {
		t.Fatalf("%v", err)
	}
}

func TestRouteSpec(t *testing.T) {
	f, err := Parse([]byte(vbase + "  - {name: site, kind: web, repo: https://github.com/a/b, port: 8080, noindex: true, www: bare}\n"))
	if err != nil || !f.Services[0].NoIndex || f.Services[0].WWW != "bare" {
		t.Fatalf("%v %+v", err, f.Services)
	}
	if _, err := Parse([]byte(vbase + "  - {name: site, kind: web, repo: https://github.com/a/b, port: 8080, www: apex}\n")); err == nil || !strings.Contains(err.Error(), `site: "www" is bare or www`) {
		t.Fatalf("%v", err)
	}
	if _, err := Parse([]byte(vbase + "  - {name: job, kind: worker, repo: https://github.com/a/b, noindex: true}\n")); err == nil || !strings.Contains(err.Error(), `job: "noindex" and "www" are a web service's`) {
		t.Fatalf("%v", err)
	}
}

func TestReplicasSpec(t *testing.T) {
	f, err := Parse([]byte(vbase + "  - {name: api, kind: web, repo: https://github.com/a/b, port: 8080, replicas: 3}\n"))
	if err != nil || f.Services[0].Replicas != 3 {
		t.Fatalf("%v %+v", err, f.Services)
	}
	for in, want := range map[string]string{
		"  - {name: db, kind: postgres, replicas: 2}\n":                                                                    `db: "replicas" is for a web service or a worker`,
		"  - {name: api, kind: web, repo: https://github.com/a/b, port: 8080, replicas: 11}\n":                             `api: "replicas" is 1 to 10`,
		"  - {name: api, kind: worker, repo: https://github.com/a/b, replicas: 2, volumes: [{name: data, path: /data}]}\n": `api has a volume; replicas share nothing, so it can have only one copy`,
	} {
		if _, err := Parse([]byte(vbase + in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", in, err)
		}
	}
}

func TestTagsSpec(t *testing.T) {
	f, err := Parse([]byte(vbase + "  - {name: api, kind: web, repo: https://github.com/a/b, port: 8080, tags: [frontend, eu-1]}\n"))
	if err != nil || len(f.Services[0].Tags) != 2 {
		t.Fatalf("%v %+v", err, f.Services)
	}
	for in, want := range map[string]string{
		"[Frontend]":              `api: tag "Frontend" is lowercase letters, digits and dashes`,
		"[a, a]":                  `api: tag "a" appears twice`,
		"[a,b,c,d,e,f,g,h,i,j,k]": `api: at most 10 tags`,
	} {
		if _, err := Parse([]byte(vbase + "  - {name: api, kind: web, repo: https://github.com/a/b, port: 8080, tags: " + in + "}\n")); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", in, err)
		}
	}
}

func TestKeepSpec(t *testing.T) {
	f, err := Parse([]byte(vbase + "  - {name: db, kind: postgres, keep: {daily: 14, weekly: 8, monthly: 6}}\n"))
	if err != nil || f.Services[0].Keep == nil || *f.Services[0].Keep != (Keep{Daily: 14, Weekly: 8, Monthly: 6}) {
		t.Fatalf("%v %+v", err, f.Services)
	}
	if f, err := Parse([]byte(vbase + "  - {name: db, kind: postgres, keep: {daily: 14}}\n")); err != nil || *f.Services[0].Keep != (Keep{Daily: 14, Weekly: 4}) {
		t.Fatalf("%v %+v", err, f.Services[0].Keep)
	}
	for in, want := range map[string]string{
		"  - {name: api, kind: web, repo: https://github.com/a/b, port: 8080, keep: {daily: 3}}\n": `api: "keep" is for a database or a service with volumes`,
		"  - {name: db, kind: postgres, keep: {daily: 0}}\n":                                       `db: keep daily is 1 to 90`,
		"  - {name: db, kind: postgres, keep: {daily: 7, monthly: 25}}\n":                          `db: keep monthly is 0 to 24`,
	} {
		if _, err := Parse([]byte(vbase + in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", in, err)
		}
	}
}
