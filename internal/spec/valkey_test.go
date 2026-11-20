package spec

import (
	"strings"
	"testing"
)

const vbase = "project: acme\nenvironment: production\nserver: hel1\nservices:\n"

func TestValkeySpec(t *testing.T) {
	base := vbase
	f, err := Parse([]byte(base + "  - {name: cache, kind: valkey}\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := f.Services[0]
	if s.Version != 8 || s.BackupOn() || s.BackupAt != "03:00" || !IsDatabase(s.Kind) {
		t.Fatalf("%+v", s)
	}
	f, _ = Parse([]byte(base + "  - {name: cache, kind: valkey, backup: true}\n"))
	if !f.Services[0].BackupOn() {
		t.Fatal("backup: true not on")
	}
	p, _ := Parse([]byte(base + "  - {name: db, kind: postgres}\n"))
	if !p.Services[0].BackupOn() || IsDatabase(KindWeb) {
		t.Fatal("postgres backup or web database")
	}
	for in, want := range map[string]string{
		"  - {name: cache, kind: valkey, version: 7}\n":                 `cache: "version" must be 8, got 7`,
		"  - {name: cache, kind: valkey, outside_port: 6380}\n":         `cache: "outside_port" and "allow" are for Postgres only`,
		"  - {name: cache, kind: valkey, repo: https://x/y}\n":          `cache: a database has no "repo"`,
		"  - {name: cache, kind: valkey, port: 6379}\n":                 `cache: a database has no "port"; apps reach it by reference`,
		"  - {name: cache, kind: valkey, image: valkey/valkey:8}\n":     `cache: a database has no "image"`,
		"  - {name: cache, kind: valkey, release: [x]}\n":               `cache: a database has no "release"`,
		"  - {name: db, kind: postgres, backup: false}\n":               `db: "backup" is always on for Postgres`,
		"  - {name: api, kind: web, repo: https://x/y, backup: true}\n": `api: only a database has "backup"`,
		"  - {name: x, kind: redis}\n":                                  `"kind" must be web, worker, cron, postgres, valkey`,
	} {
		if _, err := Parse([]byte(base + in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", in, err, want)
		}
	}
}
