package spec

import (
	"strings"
	"testing"
)

func TestMongoSpec(t *testing.T) {
	base := vbase
	f, err := Parse([]byte(base + "  - {name: docs, kind: mongodb}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s := f.Services[0]; s.Version != 8 || !s.BackupOn() || !IsDatabase(s.Kind) {
		t.Fatalf("%+v", s)
	}
	for in, want := range map[string]string{
		"  - {name: docs, kind: mongodb, backup: false}\n":       `docs: "backup" is always on for MongoDB`,
		"  - {name: docs, kind: mongodb, outside_port: 27018}\n": `docs: "outside_port" and "allow" are for Postgres only`,
		"  - {name: docs, kind: mongodb, version: 7}\n":          `docs: "version" must be 8, got 7`,
		"  - {name: docs, kind: mongodb, memory: 256MB}\n":       `docs: MongoDB needs a "memory" of at least 512MB; its cache alone takes 256MB`,
		"  - {name: x, kind: redis}\n":                           `postgres, valkey, mysql, mariadb, mongodb or clickhouse, got "redis"`,
	} {
		if _, err := Parse([]byte(base + in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", in, err, want)
		}
	}
}
